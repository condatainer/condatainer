package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/condatainer/condatainer/internal/utils"
)

// Source says where a resolved value came from.
type Source int

const (
	SourceDefault Source = iota
	SourceLayer
	SourceEnv
	SourceFlag
)

// Layer is one config file, in priority order.
type Layer interface {
	Name() string
	// Text returns a scalar key as text, and false when the key is unset or holds a list.
	Text(key string) (string, bool)
	// List returns a list key, and false when the key is unset.
	List(key string) ([]string, bool)
}

// Entry is a value a higher source hides.
type Entry struct {
	Layer string
	Value string
}

// Resolution is the value in effect for a key and where it came from.
type Resolution struct {
	Key    string
	Value  string   // as stored; a list is joined with commas
	List   []string // set for a list key
	Source Source
	Layer  string // the layer, for SourceLayer
	Via    string // the old name the value was read through, if any
	// Origins names where each element of a list came from: a layer, "env" or "flag".
	Origins []string
	Env     string // the variable, for SourceEnv
	Flag    string // the flag, for SourceFlag
	// Overridden lists the layers that set the key but lose to a higher source.
	Overridden []Entry
}

var (
	stateMu sync.Mutex
	layers  []Layer
	gen     uint64 = 1
	flags          = map[string]flagValue{}
	warned         = map[string]bool{}
)

type flagValue struct {
	text, flag string
	list       []string
}

// warnFn prints a warning. Tests replace it.
var warnFn = func(message string) { utils.PrintWarning("%s", message) }

// SetLayers installs the config files, highest priority first.
func SetLayers(l []Layer) {
	stateMu.Lock()
	defer stateMu.Unlock()
	layers = l
	gen++
	warnRemoved()
}

// Resolve returns the value in effect for a registered key.
func Resolve(name string) (Resolution, bool) {
	k, ok := Lookup(name)
	if !ok {
		return Resolution{}, false
	}
	return k.resolve(), true
}

func (k *Key) resolve() Resolution {
	stateMu.Lock()
	defer stateMu.Unlock()
	if k.opts.defaultFn != nil { // the default can change under the cache, so it is never cached
		return k.compute()
	}
	if k.cached && k.cacheGen == gen {
		return k.cacheRes
	}
	k.cacheRes, k.cached, k.cacheGen = k.compute(), true, gen
	return k.cacheRes
}

type candidate struct {
	source Source
	name   string // layer, variable or flag
	via    *Alias // set when the value was written under an old name
	text   string
	list   []string
}

// compute walks flag, environment and layers; the first value its kind accepts wins.
// A value the kind rejects is reported once and skipped. Callers hold stateMu.
func (k *Key) compute() Resolution {
	var cands []candidate
	if f, ok := flags[k.Name]; ok {
		cands = append(cands, candidate{source: SourceFlag, name: f.flag, text: f.text, list: f.list})
	}
	if c, ok := k.envCandidate(EnvName(k.Name), nil); ok {
		cands = append(cands, c)
		for _, a := range k.opts.aliases {
			if os.Getenv(EnvName(a.Old)) != "" {
				warnOnce("both|"+EnvName(a.Old), fmt.Sprintf("%s and %s are both set; using %s", EnvName(k.Name), EnvName(a.Old), EnvName(k.Name)))
			}
		}
	} else {
		for i := range k.opts.aliases {
			a := &k.opts.aliases[i]
			if c, ok := k.envCandidate(EnvName(a.Old), a); ok {
				cands = append(cands, c)
				break
			}
		}
	}
	for _, l := range layers {
		if c, ok := k.layerCandidate(l, k.Name, nil); ok {
			cands = append(cands, c)
			continue
		}
		for i := range k.opts.aliases {
			a := &k.opts.aliases[i]
			if c, ok := k.layerCandidate(l, a.Old, a); ok {
				cands = append(cands, c)
				break
			}
		}
	}

	res := Resolution{Key: k.Name}
	won, merged := false, false
	for _, c := range cands {
		if merged {
			break
		}
		if !won {
			if stored, list, ok := k.accept(c); ok {
				res.Value, res.List, res.Source = stored, list, c.source
				k.noteUse(c, &res)
				for range list {
					res.Origins = append(res.Origins, originName(c))
				}
				if k.opts.merge && c.source == SourceLayer {
					res.List, res.Origins = k.mergeLayers(cands)
					res.Value = strings.Join(res.List, ",")
					merged = true
				}
				switch c.source {
				case SourceLayer:
					res.Layer = c.name
				case SourceEnv:
					res.Env = c.name
				case SourceFlag:
					res.Flag = c.name
				}
				won = true
				continue
			}
		}
		if c.source == SourceLayer {
			res.Overridden = append(res.Overridden, Entry{Layer: c.name, Value: candidateText(c)})
		}
	}
	if !won {
		res.Value, res.List, res.Source, res.Origins = k.DefaultText(), k.DefaultList, SourceDefault, nil
		if k.kind.list {
			res.Value = strings.Join(k.DefaultList, ",")
		}
	}
	return res
}

func originName(c candidate) string {
	if c.source == SourceFlag {
		return "flag"
	}
	if c.source == SourceEnv {
		return "env"
	}
	return c.name
}

// mergeLayers is the union of every layer's list, highest layer first, with the layer each element came from.
func (k *Key) mergeLayers(cands []candidate) (list, origins []string) {
	seen := map[string]bool{}
	for _, c := range cands {
		if c.source != SourceLayer {
			continue
		}
		_, elems, ok := k.accept(c)
		if !ok {
			continue
		}
		for _, e := range elems {
			if !seen[e] {
				seen[e] = true
				list = append(list, e)
				origins = append(origins, c.name)
			}
		}
	}
	return list, origins
}

// envCandidate reads an environment variable as a value of the key. via is set for an old name.
func (k *Key) envCandidate(name string, via *Alias) (candidate, bool) {
	ev := os.Getenv(name)
	if ev == "" {
		return candidate{}, false
	}
	c := candidate{source: SourceEnv, name: name, text: ev, via: via}
	if k.kind.list {
		if c.list = k.splitEnv(ev); len(c.list) == 0 {
			return candidate{}, false
		}
		c.text = ""
	}
	return c, true
}

// layerCandidate reads the key in one layer under name, which is an old name when via is set.
func (k *Key) layerCandidate(l Layer, name string, via *Alias) (candidate, bool) {
	if k.kind.list {
		list, ok := l.List(name)
		return candidate{source: SourceLayer, name: l.Name(), list: list, via: via}, ok
	}
	text, ok := l.Text(name)
	if !ok || (text == "" && !k.opts.allowEmpty) {
		return candidate{}, false
	}
	return candidate{source: SourceLayer, name: l.Name(), text: text, via: via}, true
}

// noteUse records a value read through an old name, and reports a deprecated key being used.
func (k *Key) noteUse(c candidate, res *Resolution) {
	if c.via != nil {
		res.Via = c.via.Old
		msg := c.via.RenameMessage()
		if c.source == SourceEnv {
			msg = fmt.Sprintf("%s is deprecated%s; use %s", c.name, sinceText(c.via.Since), EnvName(k.Name))
		}
		warnOnce("alias|"+c.name, msg)
	}
	if d := k.opts.deprecated; d != nil && c.source != SourceFlag {
		warnOnce("deprecated|"+k.Name, d.text(k.Name))
	}
}

// splitEnv splits the text of an environment variable into list elements.
func (k *Key) splitEnv(text string) []string {
	if k.opts.split != nil {
		return k.opts.split(text)
	}
	sep := ":"
	if strings.Contains(text, "|") {
		sep = "|"
	}
	var out []string
	for _, e := range strings.Split(text, sep) {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func candidateText(c candidate) string {
	if c.list != nil {
		return strings.Join(c.list, ",")
	}
	return c.text
}

// parseVia parses one value of a candidate, converting it first when it was written under an old name.
func (k *Key) parseVia(c candidate, text string) (string, error) {
	if c.via != nil {
		migrated, err := c.via.Migrate(text)
		if err != nil {
			return "", fmt.Errorf("%s: %w", k.Name, err)
		}
		text = migrated
	}
	return k.Parse(text)
}

// accept parses a candidate. A rejected value is warned about once per key and source.
func (k *Key) accept(c candidate) (stored string, list []string, ok bool) {
	var err error
	if k.kind.list {
		for _, e := range c.list {
			var s string
			if s, err = k.parseVia(c, e); err != nil {
				break
			}
			list = append(list, s)
		}
		stored = strings.Join(list, ",")
	} else {
		stored, err = k.parseVia(c, c.text)
	}
	if err != nil {
		warnOnce("bad|"+k.Name+"|"+c.name, fmt.Sprintf("%s (%s); ignoring it", err, c.name))
		return "", nil, false
	}
	return stored, list, true
}

// addFlagElement appends one element to a list key's flag value. The first element in a command replaces what the config says.
func addFlagElement(key, flag, stored string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	f := flags[key]
	f.flag = flag
	f.list = append(f.list, stored)
	flags[key] = f
	gen++
}

// setFlag records a value a command-line flag carries. Two different flags for one key are an error.
func setFlag(key, flag, stored string) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	if prev, ok := flags[key]; ok && prev.flag != flag {
		return &FlagConflictError{Key: key, First: prev.flag, Second: flag}
	}
	flags[key] = flagValue{text: stored, flag: flag}
	gen++
	return nil
}

// OverrideValue sets key to text as if a flag had, and returns a func that puts the previous state back.
// It is for tests; use settingstest.Override.
func OverrideValue(key, text string) (restore func(), err error) {
	k, ok := Lookup(key)
	if !ok {
		return nil, &UnknownKeyError{Key: key}
	}
	stored, err := k.Parse(text)
	if err != nil {
		return nil, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	prev, had := flags[key]
	flags[key] = flagValue{text: stored, flag: "override"}
	gen++
	return func() {
		stateMu.Lock()
		defer stateMu.Unlock()
		if had {
			flags[key] = prev
		} else {
			delete(flags, key)
		}
		gen++
	}, nil
}

// ClearFlags drops every value a flag or an override set. It is for tests.
func ClearFlags() {
	stateMu.Lock()
	defer stateMu.Unlock()
	flags = map[string]flagValue{}
	gen++
}

// OverrideList is OverrideValue for a list key.
func OverrideList(key string, elements []string) (restore func(), err error) {
	k, ok := Lookup(key)
	if !ok || !k.kind.list {
		return nil, &UnknownKeyError{Key: key}
	}
	list := make([]string, 0, len(elements))
	for _, e := range elements {
		stored, err := k.Parse(e)
		if err != nil {
			return nil, err
		}
		list = append(list, stored)
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	prev, had := flags[key]
	flags[key] = flagValue{list: list, flag: "override"}
	gen++
	return func() {
		stateMu.Lock()
		defer stateMu.Unlock()
		if had {
			flags[key] = prev
		} else {
			delete(flags, key)
		}
		gen++
	}, nil
}

func expandPath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(os.ExpandEnv(p)); err == nil {
		return abs
	}
	return os.ExpandEnv(p)
}
