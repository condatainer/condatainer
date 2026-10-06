package settings

import (
	"fmt"
	"os"
	"strings"
)

// DeprecationOption adjusts a rename, a deprecation or a removal.
type DeprecationOption func(*deprecation)

type deprecation struct {
	since, removeIn string
	migrate         func(string) (string, error)
}

// Since is the version that made the change. It is required.
func Since(version string) DeprecationOption { return func(d *deprecation) { d.since = version } }

// RemoveIn is the version at which the alias is deleted.
func RemoveIn(version string) DeprecationOption { return func(d *deprecation) { d.removeIn = version } }

// Migrate converts a value written under the old name into one the new key accepts.
func Migrate(fn func(old string) (string, error)) DeprecationOption {
	return func(d *deprecation) { d.migrate = fn }
}

func buildDeprecation(opts []DeprecationOption) deprecation {
	var d deprecation
	for _, opt := range opts {
		opt(&d)
	}
	return d
}

// Alias is an old name that still works as another key.
type Alias struct {
	Old      string
	New      string
	Since    string
	RemoveIn string
	migrate  func(string) (string, error)
}

// Migrate converts a value written under the old name.
func (a Alias) Migrate(text string) (string, error) {
	if a.migrate == nil {
		return text, nil
	}
	return a.migrate(text)
}

// Deprecation says a key still works but should not be used.
type Deprecation struct {
	Message  string
	Since    string
	RemoveIn string
}

// RemovedKey is a key that no longer exists. Its value is ignored.
type RemovedKey struct {
	Name     string
	Message  string
	Since    string
	RemoveIn string
}

// Replaces declares that old is the former name of this key. It is read as this key, in its own layer.
func Replaces(old string, opts ...DeprecationOption) Option {
	return func(o *options) {
		d := buildDeprecation(opts)
		o.aliases = append(o.aliases, Alias{Old: old, Since: d.since, RemoveIn: d.removeIn, migrate: d.migrate})
	}
}

// Deprecated marks the key as still working but discouraged.
func Deprecated(message string, opts ...DeprecationOption) Option {
	return func(o *options) {
		d := buildDeprecation(opts)
		o.deprecated = &Deprecation{Message: message, Since: d.since, RemoveIn: d.removeIn}
	}
}

// Removed declares a key that no longer exists, with what to do instead.
func Removed(name, message string, opts ...DeprecationOption) RemovedKey {
	d := buildDeprecation(opts)
	r := RemovedKey{Name: name, Message: message, Since: d.since, RemoveIn: d.removeIn}
	regMu.Lock()
	defer regMu.Unlock()
	checkNameFree(name, "")
	removed[name] = r
	return r
}

var removed = map[string]RemovedKey{}

// checkNameFree panics when name is already a key, an alias or a removed key, or shares an environment variable with one.
// except is the key that may own it.
func checkNameFree(name, except string) {
	env := EnvName(name)
	taken := func(other string) bool { return other != except && (other == name || EnvName(other) == env) }
	for other, k := range registry {
		if taken(other) {
			panic(fmt.Sprintf("settings: %q collides with the key %q", name, other))
		}
		for _, a := range k.opts.aliases {
			if k.Name != except && taken(a.Old) {
				panic(fmt.Sprintf("settings: %q collides with the old name %q of %q", name, a.Old, k.Name))
			}
		}
	}
	for other := range removed {
		if taken(other) {
			panic(fmt.Sprintf("settings: %q collides with the removed key %q", name, other))
		}
	}
}

// Deprecation returns the key's deprecation, or nil.
func (k *Key) Deprecation() *Deprecation { return k.opts.deprecated }

// Aliases returns the old names that read as this key.
func (k *Key) Aliases() []Alias {
	return append([]Alias(nil), k.opts.aliases...)
}

// LookupAlias finds the key an old name reads as.
func LookupAlias(old string) (*Key, Alias, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	for _, k := range registry {
		for _, a := range k.Aliases() {
			if a.Old == old {
				return k, a, true
			}
		}
	}
	return nil, Alias{}, false
}

// LookupRemoved finds a removed key by name.
func LookupRemoved(name string) (RemovedKey, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := removed[name]
	return r, ok
}

// AllAliases returns every alias, sorted by old name.
func AllAliases() []Alias {
	var out []Alias
	for _, k := range Keys() {
		out = append(out, k.Aliases()...)
	}
	return out
}

// RemovedKeys returns every removed key.
func RemovedKeys() []RemovedKey {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]RemovedKey, 0, len(removed))
	for _, r := range removed {
		out = append(out, r)
	}
	return out
}

// RenameMessage is the sentence that says an old name has a new one.
func (a Alias) RenameMessage() string {
	return fmt.Sprintf("%s is deprecated%s; use %s", a.Old, sinceText(a.Since), a.New)
}

// Message is the sentence that says a key is deprecated.
func (d Deprecation) text(name string) string {
	return fmt.Sprintf("%s is deprecated%s: %s", name, sinceText(d.Since), d.Message)
}

// Message is the sentence that says a key was removed.
func (r RemovedKey) Text() string {
	return fmt.Sprintf("%s was removed%s: %s", r.Name, sinceText(r.Since), r.Message)
}

func sinceText(v string) string {
	if v == "" {
		return ""
	}
	return " since " + v
}

// DeprecationText is the sentence that says a key is deprecated.
func (k *Key) DeprecationText() string {
	if k.opts.deprecated == nil {
		return ""
	}
	return k.opts.deprecated.text(k.Name)
}

// warnOnce prints a message the first time it is given in a run. Callers hold stateMu.
func warnOnce(id, message string) {
	if warned[id] {
		return
	}
	warned[id] = true
	warnFn(message)
}

// warnRemoved reports a removed key found in a layer or the environment. Callers hold stateMu.
func warnRemoved() {
	regMu.RLock()
	names := make([]RemovedKey, 0, len(removed))
	for _, r := range removed {
		names = append(names, r)
	}
	regMu.RUnlock()
	for _, r := range names {
		for _, l := range layers {
			_, isText := l.Text(r.Name)
			_, isList := l.List(r.Name)
			if isText || isList {
				warnOnce("removed|"+r.Name+"|"+l.Name(), fmt.Sprintf("%s in the %s config; its value is ignored", r.Text(), l.Name()))
			}
		}
		if os.Getenv(EnvName(r.Name)) != "" {
			warnOnce("removed|env|"+r.Name, fmt.Sprintf("%s is set; %s", EnvName(r.Name), strings.TrimPrefix(r.Text(), r.Name+" ")))
		}
	}
}

// Unregister removes keys and removed names. It is for tests that declare their own.
func Unregister(names ...string) {
	regMu.Lock()
	defer regMu.Unlock()
	for _, n := range names {
		delete(registry, n)
		delete(removed, n)
	}
}

// Names returns every registered key name and removed name.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry)+len(removed))
	for n := range registry {
		out = append(out, n)
	}
	for n := range removed {
		out = append(out, n)
	}
	return out
}
