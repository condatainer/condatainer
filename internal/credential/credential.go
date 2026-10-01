// Package credential stores secrets per config layer, keyed by a host or a URL
// path under it: registry logins and recipe-source tokens alike.
package credential

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// FileName is the credential file beside each config layer's config file.
const FileName = "credentials.json"

// TokenUser is the username stored beside a token that carries its own identity.
const TokenUser = "x-access-token"

// Kind is what a credential opens. Each kind has its own section of the file, so
// a key is only ever looked up for, and sent to, its own kind of server.
type Kind int

const (
	// Registry is an OCI registry login.
	Registry Kind = iota
	// Source is a recipe-source token.
	Source
)

// ErrNotStored reports that a layer holds nothing under a key.
var ErrNotStored = errors.New("no stored credential")

// File is one config layer's credential file.
type File struct {
	Layer string // "user", "extra-root" or "app-root"
	Path  string
}

// Credential is a username and its password or token.
type Credential struct {
	Username string
	Secret   string
}

// Found is a credential and where it came from.
type Found struct {
	Credential
	Key   string
	Layer string
	// Source names the recipe source a registry token is stored with; empty
	// for a registry login.
	Source string
}

// document holds registry logins under "auths" and recipe sources under
// "sources", keyed alike. "auth" is base64 of "user:secret". A source entry may
// also hold its registry and that registry's token, in the same encoding.
type document struct {
	Auths   map[string]entry `json:"auths,omitempty"`
	Sources map[string]entry `json:"sources,omitempty"`
}

// section is the map holding kind, created when create is set.
func (d *document) section(kind Kind, create bool) map[string]entry {
	m := &d.Auths
	if kind == Source {
		m = &d.Sources
	}
	if *m == nil && create {
		*m = map[string]entry{}
	}
	return *m
}

type entry struct {
	Auth         string `json:"auth"`
	Registry     string `json:"registry,omitempty"`
	RegistryAuth string `json:"registry_auth,omitempty"`
}

func (e entry) credential() (Credential, bool) { return decode(e.Auth) }

func (e entry) registryCredential() (Credential, bool) { return decode(e.RegistryAuth) }

func decode(auth string) (Credential, bool) {
	raw, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return Credential{}, false
	}
	user, secret, _ := strings.Cut(string(raw), ":")
	return Credential{Username: user, Secret: secret}, secret != ""
}

func encode(cred Credential) string {
	return base64.StdEncoding.EncodeToString([]byte(cred.Username + ":" + cred.Secret))
}

// TrimScheme drops a URL scheme and trailing slashes, leaving the key form.
func TrimScheme(raw string) string {
	raw = strings.TrimSpace(raw)
	if _, rest, ok := strings.Cut(raw, "://"); ok {
		raw = rest
	}
	return strings.TrimRight(raw, "/")
}

// Keys lists the keys that may hold a credential for scope, most specific
// first: scope itself, each parent path, then the bare host.
func Keys(scope string) []string {
	scope = TrimScheme(scope)
	var keys []string
	for path := scope; strings.Contains(path, "/"); path = path[:strings.LastIndex(path, "/")] {
		keys = append(keys, path)
	}
	host, _, _ := strings.Cut(scope, "/")
	return append(keys, host)
}

// Lookup finds the kind credential for scope in files, given nearest first: the
// most specific key wins, then the nearest layer. An unreadable file holds nothing.
func Lookup(files []File, kind Kind, scope string) (Found, bool) {
	docs := make([]document, len(files))
	for i, f := range files {
		docs[i], _ = read(f.Path)
	}
	for _, key := range Keys(scope) {
		for i, doc := range docs {
			if e, ok := doc.section(kind, false)[key]; ok {
				if cred, ok := e.credential(); ok {
					return Found{Credential: cred, Key: key, Layer: files[i].Layer}, true
				}
			}
		}
	}
	return Found{}, false
}

// Save stores cred as kind under key in f, keeping a source entry's registry.
//   - The user layer's directory is created private; a shared layer's must exist.
//   - The file is written 0600, with group read-write when its directory is group-writable.
func Save(f File, kind Kind, key string, cred Credential) error {
	return update(f, kind, key, func(e *entry) { e.Auth = encode(cred) })
}

// SaveSourceRegistry stores, in the source entry under key in f, the source's
// registry and the token for it.
func SaveSourceRegistry(f File, key, registry string, cred Credential) error {
	return update(f, Source, key, func(e *entry) {
		e.Registry, e.RegistryAuth = registry, encode(cred)
	})
}

func update(f File, kind Kind, key string, change func(*entry)) error {
	if err := ensureDir(f); err != nil {
		return err
	}
	doc, err := read(f.Path)
	if err != nil {
		return err
	}
	section := doc.section(kind, true)
	e := section[key]
	change(&e)
	section[key] = e
	return write(f.Path, doc)
}

// LookupSourceRegistry finds the registry token stored with the source at base,
// whose entry is found as Lookup finds it. The Found's Key is the registry.
func LookupSourceRegistry(files []File, base string) (Found, bool) {
	for _, key := range Keys(base) {
		for _, f := range files {
			doc, _ := read(f.Path)
			e, ok := doc.Sources[key]
			if !ok {
				continue
			}
			cred, ok := e.registryCredential()
			if !ok || e.Registry == "" {
				return Found{}, false
			}
			return Found{Credential: cred, Key: e.Registry, Layer: f.Layer}, true
		}
	}
	return Found{}, false
}

// Remove deletes the kind credential under key from f, or reports ErrNotStored.
func Remove(f File, kind Kind, key string) error {
	doc, err := read(f.Path)
	if err != nil {
		return err
	}
	section := doc.section(kind, false)
	if _, ok := section[key]; !ok {
		return fmt.Errorf("%w for %s in the %s layer", ErrNotStored, key, f.Layer)
	}
	delete(section, key)
	return write(f.Path, doc)
}

// Stored is one saved credential. The secret is never read into it.
type Stored struct {
	Key      string
	Layer    string
	Username string
	Path     string
	// Source is the source key a registry token is stored with; empty for a
	// registry login.
	Source string
	// Perm is the file's permission bits, such as "0600", or empty when it
	// cannot be inspected.
	Perm string
}

// ListSourceRegistries returns every registry token stored with a source in
// files, keyed by registry, by registry and then in the order of files.
func ListSourceRegistries(files []File) ([]Stored, error) {
	var out []Stored
	for _, f := range files {
		doc, err := read(f.Path)
		if err != nil {
			return nil, err
		}
		for key, e := range doc.Sources {
			cred, ok := e.registryCredential()
			if !ok || e.Registry == "" {
				continue
			}
			out = append(out, Stored{
				Key: e.Registry, Layer: f.Layer, Username: cred.Username, Path: f.Path,
				Source: key, Perm: permOf(f.Path),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// List returns every saved kind credential in files, by key and then in the
// order of files.
func List(files []File, kind Kind) ([]Stored, error) {
	var out []Stored
	rank := map[string]int{}
	for i, f := range files {
		rank[f.Layer] = i
		doc, err := read(f.Path)
		if err != nil {
			return nil, err
		}
		for key, e := range doc.section(kind, false) {
			cred, _ := e.credential()
			out = append(out, Stored{
				Key: key, Layer: f.Layer, Username: cred.Username, Path: f.Path,
				Perm: permOf(f.Path),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return rank[out[i].Layer] < rank[out[j].Layer]
	})
	return out, nil
}

// read reads a credential file; a missing file holds nothing.
func read(path string) (document, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return document{}, nil
	}
	if err != nil {
		return document{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return document{}, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return doc, nil
}

// write replaces path atomically with mode 0600, adding group read-write when
// the directory is group-writable, so a shared layer's members can read it.
func write(path string, doc document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	utils.ShareWithParentGroup(path)
	return nil
}

// ensureDir creates the user layer's directory private, and refuses a shared
// layer's that does not exist: it is not ours to create.
func ensureDir(f File) error {
	dir := filepath.Dir(f.Path)
	if f.Layer == "user" {
		return os.MkdirAll(dir, 0o700)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s does not exist", dir)
	}
	return nil
}

func permOf(path string) string {
	perm, _, ok := Mode(path)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%04o", perm)
}
