package settings

import (
	"fmt"
	"slices"

	"github.com/spf13/pflag"
)

// FlagOption adjusts a flag made from a key.
type FlagOption func(*flagOptions)

type flagOptions struct{ short, usage string }

// Usage replaces the key's Help as the flag's description.
func Usage(text string) FlagOption { return func(o *flagOptions) { o.usage = text } }

// Short gives the flag a one-letter name.
func Short(s string) FlagOption { return func(o *flagOptions) { o.short = s } }

type flagVar struct {
	key  *Key
	name string
}

func (f *flagVar) String() string { return "" }
func (f *flagVar) Type() string {
	if f.key.kind.list {
		return "stringArray"
	}
	return f.key.kind.name
}
func (f *flagVar) Set(text string) error {
	stored, err := f.key.Parse(text)
	if err != nil {
		return err
	}
	if f.key.kind.list {
		addFlagElement(f.key.Name, f.name, stored)
		return nil
	}
	return setFlag(f.key.Name, f.name, stored)
}

// switchVar is a flag with no argument that sets a fixed value.
type switchVar struct {
	key   *Key
	name  string
	value string
}

func (s *switchVar) String() string   { return "" }
func (s *switchVar) Type() string     { return "bool" }
func (s *switchVar) IsBoolFlag() bool { return true }
func (s *switchVar) Set(text string) error {
	if text == "false" { // --flag=false is the same as leaving it out
		return nil
	}
	stored, err := s.key.Parse(s.value)
	if err != nil {
		return err
	}
	return setFlag(s.key.Name, s.name, stored)
}

func mustKey(name string) *Key {
	k, ok := Lookup(name)
	if !ok {
		panic(fmt.Sprintf("settings: flag for unregistered key %q", name))
	}
	return k
}

// AddFlag makes a flag that takes the key's value. Its usage is the key's Help.
// A value that the key's kind rejects fails when the arguments are parsed.
func AddFlag(fs *pflag.FlagSet, key, name string, opts ...FlagOption) {
	var o flagOptions
	for _, opt := range opts {
		opt(&o)
	}
	k := mustKey(key)
	k.addFlagName(name)
	f := fs.VarPF(&flagVar{key: k, name: name}, name, o.short, o.usageOr(k))
	if k.IsBool() {
		f.NoOptDefVal = "true"
	}
}

// AddSwitch makes a flag with no argument that sets the key to value.
// Several switches may share a key, but two of them in one command are refused.
func AddSwitch(fs *pflag.FlagSet, key, name, value string, opts ...FlagOption) {
	var o flagOptions
	for _, opt := range opts {
		opt(&o)
	}
	k := mustKey(key)
	k.addFlagName(name)
	f := fs.VarPF(&switchVar{key: k, name: name, value: value}, name, o.short, o.usageOr(k))
	f.NoOptDefVal = "true"
}

func (o flagOptions) usageOr(k *Key) string {
	if o.usage != "" {
		return o.usage
	}
	return k.Help
}

// FlagKey returns the key a flag made by AddFlag or AddSwitch sets.
func FlagKey(f *pflag.Flag) (*Key, bool) {
	switch v := f.Value.(type) {
	case *flagVar:
		return v.key, true
	case *switchVar:
		return v.key, true
	}
	return nil, false
}

// ChangedFlags returns the arguments for the flags the user passed that set one of keys, in flag-name order,
// so a command that re-runs itself on another node can repeat them.
func ChangedFlags(fs *pflag.FlagSet, keys ...string) []string {
	var out []string
	fs.Visit(func(f *pflag.Flag) {
		k, ok := FlagKey(f)
		if !ok || !slices.Contains(keys, k.Name) {
			return
		}
		switch v := f.Value.(type) {
		case *switchVar:
			out = append(out, "--"+f.Name)
		case *flagVar:
			stateMu.Lock()
			fv := flags[v.key.Name]
			stateMu.Unlock()
			if v.key.kind.list {
				for _, e := range fv.list {
					out = append(out, "--"+f.Name, e)
				}
			} else {
				out = append(out, "--"+f.Name, fv.text)
			}
		}
	})
	return out
}

func (k *Key) addFlagName(name string) {
	regMu.Lock()
	defer regMu.Unlock()
	if !slices.Contains(k.flagNames, name) {
		k.flagNames = append(k.flagNames, name)
	}
}
