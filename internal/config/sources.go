package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/logging"
)

// defaultSource is the collection every resolution ends at. It is a default
// value rather than a fallback the resolver reaches for, so a site replaces it
// by writing its own `cnt` entry, positioned like any other.
var defaultSource = catalog.Spec{
	Name: "cnt",
	Base: "https://raw.githubusercontent.com/condatainer/cnt/main",
}

// LayeredSource is a source and the config layer that names it, DefaultLayer
// for the default `cnt`.
type LayeredSource struct {
	catalog.Spec
	Layer string
}

// DefaultLayer is the Layer of the default `cnt` when no layer names it.
const DefaultLayer = "default"

// ResolvedSources is the search order: every layer's `sources`, strongest first,
// a name already taken skipped, then the default `cnt` unless a layer names it.
func ResolvedSources() []LayeredSource {
	var out []LayeredSource
	seen := map[string]bool{}
	for _, l := range loadedLayers {
		if !l.InConfig("sources") {
			continue
		}
		for _, spec := range decodeSourceList(l.v.Get("sources")) {
			if !seen[spec.Name] {
				seen[spec.Name] = true
				out = append(out, LayeredSource{Spec: spec, Layer: l.Type})
			}
		}
	}
	if !seen[defaultSource.Name] {
		out = append(out, LayeredSource{Spec: defaultSource, Layer: DefaultLayer})
	}
	return out
}

// layerSources is ResolvedSources without the layers.
func layerSources() []catalog.Spec {
	resolved := ResolvedSources()
	out := make([]catalog.Spec, len(resolved))
	for i, s := range resolved {
		out[i] = s.Spec
	}
	return out
}

// layerBinds merges the `bind` key across every layer, highest priority first,
// once each. CNT_BIND replaces the list and is "|"-separated.
func layerBinds() []string {
	var raw []string
	if ev := os.Getenv("CNT_BIND"); ev != "" {
		raw = strings.Split(ev, "|")
	} else {
		for _, v := range configLayers {
			raw = append(raw, v.GetStringSlice("bind")...)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, b := range raw {
		if b = strings.TrimSpace(b); b != "" && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}

// DefaultSource is the collection every resolution ends at unless a layer
// names `cnt` itself.
func DefaultSource() catalog.Spec { return defaultSource }

// decodeSourceList turns viper's view of the YAML sequence into specs. Each
// entry is a single-key mapping, `- lab: /shared/lab/recipes`.
func decodeSourceList(raw any) []catalog.Spec {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []catalog.Spec
	for _, item := range items {
		switch v := item.(type) {
		case map[string]any:
			for name, base := range v {
				if s, ok := base.(string); ok {
					out = append(out, catalog.Spec{Name: name, Base: strings.TrimRight(s, "/")})
				}
			}
		case map[any]any:
			for name, base := range v {
				n, okN := name.(string)
				s, okS := base.(string)
				if okN && okS {
					out = append(out, catalog.Spec{Name: n, Base: strings.TrimRight(s, "/")})
				}
			}
		}
	}
	return out
}

// SelectSources restricts recipe resolution to the named configured source handles, in the order given.
//   - An empty selection leaves the configured source list unchanged.
//   - Duplicate handles are ignored after their first occurrence.
func SelectSources(names []string) error {
	if len(names) == 0 {
		return nil
	}

	configured := make(map[string]catalog.Spec, len(Global.Sources))
	available := make([]string, 0, len(Global.Sources))
	for _, source := range Global.Sources {
		configured[source.Name] = source
		available = append(available, source.Name)
	}

	selected := make([]catalog.Spec, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		source, ok := configured[name]
		if !ok || name == "" {
			return fmt.Errorf("unknown source %q (configured: %s)",
				name, strings.Join(available, ", "))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		selected = append(selected, source)
	}

	Global.Sources = selected
	ResetCatalog()
	return nil
}

var (
	catalogOnce sync.Once
	catalogVal  catalog.Catalog
	catalogErr  error
)

// OpenCatalog opens the configured sources, once per process.
//
// Config owns which sources exist and where the cache lives; the catalog owns
// everything under that directory.
func OpenCatalog(ctx context.Context) (catalog.Catalog, error) {
	catalogOnce.Do(func() {
		// Defensive: layerSources always leaves defaultSource in. An empty
		// catalog provides nothing, which callers already handle.
		if len(Global.Sources) == 0 {
			return
		}
		cache := catalog.Cache{Dir: CatalogCacheDir(), TTL: Global.MetadataCacheTTL}
		catalogVal, catalogErr = catalog.Open(ctx, withSourceTokens(Global.Sources), cache)
	})
	return catalogVal, catalogErr
}

// withSourceTokens copies specs, giving each HTTP source the token stored for
// its URL, if any. Only when the catalog opens, so a listed source never
// carries one.
func withSourceTokens(specs []catalog.Spec) []catalog.Spec {
	files := CredentialFiles()
	out := make([]catalog.Spec, len(specs))
	for i, spec := range specs {
		out[i] = spec
		if !strings.HasPrefix(spec.Base, "https://") && !strings.HasPrefix(spec.Base, "http://") {
			continue
		}
		if found, ok := credential.Lookup(files, credential.Source, spec.Base); ok {
			out[i].Token = catalog.NewToken(found.Secret, found.Key, found.Layer)
		}
	}
	return out
}

var warnSourcesOnce sync.Once

// WarnUnreachableSources reports sources that could not be read, once per
// process however many names get resolved. Call it after the catalog has been
// consulted.
func WarnUnreachableSources(ctx context.Context, cat catalog.Catalog) {
	warnSourcesOnce.Do(func() {
		log := logging.FromContext(ctx)
		for _, s := range cat {
			switch {
			case s.Err != nil:
				log.Warn("Source unreachable, skipping it", "source", s.Name, "err", s.Err)
			case s.Stale:
				log.Warn("Source not refreshed, using the cached index", "source", s.Name)
			}
			if s.TokenRefused {
				log.Warn("Source refused its stored token, reading it without one", "source", s.Name, "token", s.Token.String())
			}
		}
	})
}

// BaseRecipeName returns the module name of the base recipe, e.g. "ubuntu24/base"
// for recipes/ubuntu24/base.def. Empty when no default distro is configured.
func BaseRecipeName() string {
	if distro := ResolvedDefaultDistro(); distro != "" {
		return distro + "/base"
	}
	return ""
}

// BaseRecipeNameFrom is BaseRecipeName with the default_distro fallback, for
// callers that already hold an open catalog. Config `default_distro` still wins.
func BaseRecipeNameFrom(cat catalog.Catalog) string {
	if name := BaseRecipeName(); name != "" {
		return name
	}
	if def := cat.DefaultDistro(); def != "" {
		return def + "/base"
	}
	return ""
}

// EnsureDefaultDistro records the default distro in config the first time one
// is needed, taking it from the first source declaring a default_distro, and
// never revises it. Returns the resolved distro, "" when nothing supplies
// one; a failed write is not an error.
func EnsureDefaultDistro(cat catalog.Catalog) string {
	if Global.DefaultDistro != "" {
		return Global.DefaultDistro
	}
	def := cat.DefaultDistro()
	if def == "" {
		return ""
	}
	Global.DefaultDistro = def
	if path, _, err := ResolveWritableConfigPath(""); err == nil {
		_ = SetConfigKey(path, "default_distro", def)
	}
	return def
}

// SourceDefaultDistro returns the default distro the first source recommends,
// which may differ from the recorded one after an upstream change.
func SourceDefaultDistro(cat catalog.Catalog) string { return cat.DefaultDistro() }

// ResolvedDefaultDistro returns the configured default distro, e.g. "ubuntu24"
// — the bare-name prefix for installed overlays. Config only: it never opens
// the catalog, so offline paths like list and info stay offline.
func ResolvedDefaultDistro() string { return Global.DefaultDistro }

// CatalogCacheDir is where fetched index and recipe bytes are kept.
func CatalogCacheDir() string {
	dir, err := GetWritableCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "catalog")
}

// RefreshCatalogCache discards everything cached for every source, so the next
// read refetches. Removing the whole directory also drops entries for sources no
// longer configured.
func RefreshCatalogCache() error {
	dir := CatalogCacheDir()
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	ResetCatalog()
	return nil
}

// ResetCatalog drops the memoized catalog. Tests reconfigure sources between
// cases; nothing in a command run needs it.
func ResetCatalog() {
	catalogOnce = sync.Once{}
	warnSourcesOnce = sync.Once{}
	catalogVal, catalogErr = nil, nil
}
