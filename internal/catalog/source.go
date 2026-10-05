package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	recipesDir     = "recipes"
	indexDir       = "index"
	descriptorFile = "source.json"
)

// ErrNotProvided reports that no source offers a name. It is an outcome rather
// than a failure: the caller has a fallback this package must not know about.
var ErrNotProvided = errors.New("catalog: no source provides this")

// ErrUnreadable reports a source that refused a read, or has nothing at the
// path: a wrong URL or directory, or a private repository read without a token.
var ErrUnreadable = errors.New("catalog: source refused the read or has nothing there")

// ErrTokenRefused reports that a source refused its stored token, and refused
// the read without it too.
var ErrTokenRefused = errors.New("catalog: the stored token was refused")

// Spec is one configured source: a local handle, a base, and the token stored
// for it, if any. This is how config gets in without being imported.
type Spec struct {
	Name, Base string
	Token      *Token
}

// Token is a recipe-source token and where it is stored. The secret is
// unexported so printing a Token or a Spec never shows it.
type Token struct {
	Key    string // the credential key it is stored under
	Layer  string // the config layer that holds it
	secret string
}

// NewToken wraps a secret with the key and layer it was found under.
func NewToken(secret, key, layer string) *Token {
	return &Token{Key: key, Layer: layer, secret: secret}
}

// String names the token by where it is stored, never by its value.
func (t *Token) String() string { return fmt.Sprintf("%s (%s layer)", t.Key, t.Layer) }

// Descriptor is a source's source.json.
type Descriptor struct {
	Schema int `json:"schema"`
	// Source is the collection's own code repository, recorded into every
	// artifact it builds as manifest build.source and published as
	// org.opencontainers.image.source.
	//
	// Named for what it becomes rather than what it looks like: `repository`
	// means an OCI repository coordinate everywhere else in the tree
	// (lock.Remote, oras' Reference), and one word cannot carry both.
	Source        string `json:"source"`
	DefaultDistro string `json:"default_distro"`
	OCI           OCI    `json:"oci,omitzero"`
}

// OCI is the registry a source's artifacts are published to and pulled from.
type OCI struct {
	Registry string `json:"registry,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// Source is one collection of recipes and helpers.
type Source struct {
	Name string // the local handle from config
	Base string // HTTP base URL or filesystem path
	Desc Descriptor
	// DescriptorErr reports that source.json existed but was not usable. Recipes
	// remain available: a broken optional descriptor must not erase a collection,
	// but callers must not trust its registry or provenance defaults.
	DescriptorErr error

	// Stale is set when an index was served from cache after a fetch failed.
	// What that means — a note, or a stop — is the caller's.
	Stale bool
	// Err is set when the source could not be reached and had nothing cached.
	// It does not remove the source from the catalog, so list can say so.
	Err error

	// Token is what reads send, or nil for a source read without one.
	Token *Token
	// TokenRefused is set when the source refused Token and answered without it.
	// Later reads skip the token; reporting it is the caller's.
	TokenRefused bool

	b backend

	mu     sync.Mutex
	loaded bool
	cached map[string]*Entry
}

// Entries reads what this source offers, once. A walk or an index fetch is
// repeated for every lookup otherwise, and a collection does not change under a
// running command.
func (s *Source) Entries(ctx context.Context) (map[string]*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.cached, s.Err
	}
	s.loaded = true
	s.cached, s.Err = s.b.entries(ctx)
	return s.cached, s.Err
}

// Catalog is the configured sources in order. Earlier entries shadow later
// ones, matching image search, the config chain, and PATH.
type Catalog []*Source

// backend is how a source is read. Open picks one from the shape of Base and
// nothing switches on it afterwards.
type backend interface {
	entries(ctx context.Context) (map[string]*Entry, error)
	read(ctx context.Context, path string) ([]byte, error)
}

// Open prepares the configured sources. A source that cannot be reached keeps
// its place with Err set, since dropping it would silently promote the next
// source's recipes under first-wins ordering.
func Open(ctx context.Context, specs []Spec, cache Cache) (Catalog, error) {
	if len(specs) == 0 {
		return nil, errors.New("catalog: no sources configured")
	}
	cat := make(Catalog, 0, len(specs))
	for _, spec := range specs {
		if spec.Base == "" {
			return nil, fmt.Errorf("catalog: source %q has no base", spec.Name)
		}
		s := &Source{Name: spec.Name, Base: spec.Base}
		if isURL(spec.Base) {
			s.Token = spec.Token
			s.b = &httpBackend{src: s, cache: cache}
		} else {
			s.b = &dirBackend{src: s}
		}
		s.loadDescriptor(ctx)
		cat = append(cat, s)
	}
	return cat, nil
}

// Probe checks that spec can be read as given, without a cache: a directory
// needs its recipes/ directory, an HTTP source its index. The Source returned
// carries its descriptor and TokenRefused.
func Probe(ctx context.Context, spec Spec) (*Source, error) {
	s := &Source{Name: spec.Name, Base: spec.Base}
	if !isURL(spec.Base) {
		if info, err := os.Stat(filepath.Join(spec.Base, recipesDir)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%w: %s has no %s directory", ErrUnreadable, spec.Base, recipesDir)
		}
		s.b = &dirBackend{src: s}
		s.loadDescriptor(ctx)
		return s, nil
	}
	s.Token = spec.Token
	h := &httpBackend{src: s}
	s.b = h
	if _, err := h.entries(ctx); err != nil {
		return nil, err
	}
	s.loadDescriptor(ctx)
	return s, nil
}

// loadDescriptor reads source.json. A source without one still works — a plain
// directory of recipes is the simplest collection there is — so only the
// provenance and default base are lost.
func (s *Source) loadDescriptor(ctx context.Context) {
	data, err := s.b.read(ctx, descriptorFile)
	if err != nil {
		return
	}
	desc, err := ParseDescriptor(data)
	if err != nil {
		s.DescriptorErr = err
		return
	}
	s.Desc = desc
}

// ParseDescriptor decodes and validates source.json.
//   - A descriptor with no schema field (zero) is accepted; a non-zero unsupported schema is rejected.
//   - The OCI registry is normalized to a registry/repository root.
func ParseDescriptor(data []byte) (Descriptor, error) {
	var desc Descriptor
	if err := json.Unmarshal(data, &desc); err != nil {
		return Descriptor{}, fmt.Errorf("catalog: decode source descriptor: %w", err)
	}
	if desc.Schema != 0 && desc.Schema != 1 {
		return Descriptor{}, fmt.Errorf("catalog: unsupported source descriptor schema %d", desc.Schema)
	}
	if err := ValidSourceURL(desc.Source); err != nil {
		return Descriptor{}, fmt.Errorf("catalog: source descriptor: %w", err)
	}
	if err := desc.OCI.normalize(); err != nil {
		return Descriptor{}, err
	}
	return desc, nil
}

// ValidSourceURL reports whether raw is usable as org.opencontainers.image.source.
//   - Empty is valid and omits the annotation.
//   - It checks shape only: whether the repository exists cannot be answered offline.
//   - The scheme matters, because a registry links a package to a repository by exact URL match and anything else links nothing.
func ValidSourceURL(raw string) error {
	source := strings.TrimSpace(raw)
	if source == "" {
		return nil
	}
	if strings.ContainsAny(source, " \t\r\n") {
		return fmt.Errorf("source %q contains whitespace", source)
	}
	if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
		return fmt.Errorf("source %q is not an http(s) URL", source)
	}
	return nil
}

func (o *OCI) normalize() error {
	o.Audience = strings.ToLower(strings.TrimSpace(o.Audience))
	if o.Audience == "" {
		o.Audience = "public"
	}
	if o.Audience != "public" && o.Audience != "restricted" {
		return fmt.Errorf("catalog: OCI audience must be public or restricted, got %q", o.Audience)
	}
	if strings.TrimSpace(o.Registry) == "" {
		o.Registry = ""
		return nil
	}
	var err error
	if o.Registry, err = normalizeOCIEndpoint(o.Registry); err != nil {
		return fmt.Errorf("catalog: OCI registry: %w", err)
	}
	return nil
}

func normalizeOCIEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	endpoint = strings.TrimPrefix(endpoint, "oci://")
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" || strings.Contains(endpoint, "://") ||
		strings.ContainsAny(endpoint, " \t\r\n") || !strings.Contains(endpoint, "/") {
		return "", fmt.Errorf("%q must be a registry/repository root", endpoint)
	}
	return endpoint, nil
}

// Entries returns every entry the catalog offers, merged by name with the first source winning.
//   - An unreachable source contributes nothing and is skipped, as in Lookup, even when that leaves the result empty: that is a catalog that offers nothing, not a failure.
//   - Why a source came back empty is on the source (Err, Stale), so a caller reports it once.
func (c Catalog) Entries(ctx context.Context) map[string]*Entry {
	out := map[string]*Entry{}
	for _, s := range c {
		found, err := s.Entries(ctx)
		if err != nil {
			continue
		}
		for name, e := range found {
			if _, taken := out[name]; !taken {
				out[name] = e
			}
		}
	}
	return out
}

// DefaultDistro is the default distro named by the first source declaring one.
func (c Catalog) DefaultDistro() string {
	for _, s := range c {
		if s.Desc.DefaultDistro != "" {
			return s.Desc.DefaultDistro
		}
	}
	return ""
}

func isURL(base string) bool {
	return strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://")
}
