// Package settings is the registry of config keys. A package declares the keys it reads, and
// reads them through typed handles; the resolver finds each value across flag, environment,
// config files and default.
package settings

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// EnvPrefix starts the name of every variable that overrides a key.
const EnvPrefix = "CNT_CONFIG_"

// EnvName returns the variable that overrides key: "build.ncpus" is CNT_CONFIG_BUILD_NCPUS.
func EnvName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// Key is one registered setting.
type Key struct {
	Name string
	Help string
	// Default is the default as text, parsed by the key's kind.
	Default string
	// DefaultList is the default of a list key.
	DefaultList []string
	Order       int

	kind kind
	opts options

	flagNames []string // flags made from this key, in registration order

	cached   bool
	cacheGen uint64
	cacheRes Resolution
}

// FlagNames returns the command-line flags that set the key.
func (k *Key) FlagNames() []string { return slices.Clone(k.flagNames) }

// OmitsEmpty reports whether `config list` hides the key while its value is empty.
func (k *Key) OmitsEmpty() bool { return k.opts.omitEmpty }

// DefaultText is the default as stored, computed now for a DefaultFunc key.
func (k *Key) DefaultText() string {
	if k.opts.defaultFn == nil {
		return k.Default
	}
	text := fmt.Sprint(k.opts.defaultFn())
	if stored, err := k.Parse(text); err == nil {
		return stored
	}
	return text
}

// IsList reports whether the key holds a list.
func (k *Key) IsList() bool { return k.kind.list }

// IsBool reports whether the key is a boolean.
func (k *Key) IsBool() bool { return k.kind.name == "bool" }

// Kind is the name of the key's kind: "bool", "int", "list" and so on.
func (k *Key) Kind() string { return k.kind.name }

// Parse validates text and returns the form that is stored.
func (k *Key) Parse(text string) (string, error) {
	out, err := k.kind.parse(k, strings.TrimSpace(text))
	if err != nil {
		return "", fmt.Errorf("%s: %w", k.Name, err)
	}
	if k.opts.validate != nil {
		if err := k.opts.validate(out); err != nil {
			return "", fmt.Errorf("%s: %w", k.Name, err)
		}
	}
	if k.opts.normalize != nil {
		out = k.opts.normalize(out)
	}
	return out, nil
}

// Check runs the key's machine check, if it has one.
func (k *Key) Check(stored string) error {
	if k.opts.check == nil {
		return nil
	}
	return k.opts.check(stored)
}

// Accepts describes the values the key takes, for help text.
func (k *Key) Accepts() string {
	if k.opts.accepts != "" {
		return k.opts.accepts
	}
	return k.kind.accepts(k)
}

// Suggest returns completion values for the key.
func (k *Key) Suggest() []string {
	out := append([]string(nil), k.kind.suggest(k)...)
	return append(out, k.opts.suggest...)
}

// Show formats a stored value for display.
func (k *Key) Show(stored string) string {
	switch {
	case k.opts.show != nil:
		return k.opts.show(stored)
	case k.kind.show != nil && stored != "":
		return k.kind.show(stored)
	}
	return stored
}

// Note returns the extra line shown under the value, if any.
func (k *Key) Note(stored string) string {
	if k.opts.note == nil {
		return ""
	}
	return k.opts.note(stored)
}

// Detect returns the value `config init` should write, or "".
func (k *Key) Detect() string {
	if k.opts.detect == nil {
		return ""
	}
	return k.opts.detect()
}

// CanDetect reports whether `config init` writes the key.
func (k *Key) CanDetect() bool { return k.opts.detect != nil }

var (
	regMu    sync.RWMutex
	registry = map[string]*Key{}
)

// register adds a key, and panics on a declaration that can never work.
func register(k *Key) *Key {
	switch {
	case k.Name == "" || strings.HasPrefix(k.Name, ".") || strings.HasSuffix(k.Name, ".") || strings.Contains(k.Name, ".."):
		panic(fmt.Sprintf("settings: bad key name %q", k.Name))
	case k.Name != strings.ToLower(k.Name):
		panic(fmt.Sprintf("settings: key %q is not lower case", k.Name))
	case k.Help == "":
		panic(fmt.Sprintf("settings: key %q has no Help", k.Name))
	}
	regMu.Lock()
	defer regMu.Unlock()
	for name := range registry {
		if name == k.Name {
			panic(fmt.Sprintf("settings: key %q declared twice", k.Name))
		}
		if strings.HasPrefix(name, k.Name+".") || strings.HasPrefix(k.Name, name+".") {
			panic(fmt.Sprintf("settings: %q and %q: a key cannot also be a section", name, k.Name))
		}
	}
	checkNameFree(k.Name, "")
	for i := range k.opts.aliases {
		a := &k.opts.aliases[i]
		a.New = k.Name
		if a.Since == "" {
			panic(fmt.Sprintf("settings: the old name %q of %q has no Since", a.Old, k.Name))
		}
		checkNameFree(a.Old, "")
		if a.Old == k.Name {
			panic(fmt.Sprintf("settings: %q replaces itself", k.Name))
		}
	}
	if d := k.opts.deprecated; d != nil && d.Since == "" {
		panic(fmt.Sprintf("settings: the deprecation of %q has no Since", k.Name))
	}
	registry[k.Name] = k
	return k
}

// Lookup returns the registered key with that name.
func Lookup(name string) (*Key, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	k, ok := registry[name]
	return k, ok
}

// Keys returns every registered key, sorted by name.
func Keys() []*Key {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]*Key, 0, len(registry))
	for _, k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// TopLevel returns the keys with no section, in the order Section uses.
func TopLevel() []*Key {
	var out []*Key
	for _, k := range Keys() {
		if !strings.Contains(k.Name, ".") {
			out = append(out, k)
		}
	}
	sortKeys(out)
	return out
}

func sortKeys(out []*Key) {
	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := out[i].Order, out[j].Order
		if (oi == 0) != (oj == 0) {
			return oj == 0
		}
		return oi < oj
	})
}

// Section returns the keys under a dotted prefix: those with an Order first, lowest first, then the rest by name.
func Section(prefix string) []*Key {
	var out []*Key
	for _, k := range Keys() {
		if strings.HasPrefix(k.Name, prefix+".") {
			out = append(out, k)
		}
	}
	sortKeys(out)
	return out
}
