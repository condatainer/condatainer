package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/settings"
)

// touch writes a config file and moves its time forward, so a same-size edit is still seen.
func touch(t *testing.T, path, content string, offset time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(offset)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestReloadIfChangedPicksUpEditsAndKeepsTheOldConfigOnABadFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CNT_ROOT", t.TempDir())
	t.Setenv("CNT_EXTRA_ROOT", "")
	path, err := GetUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.SetLayers(nil); configLayers = nil; ResetCatalog() })

	if err := LoadLayers(); err != nil {
		t.Fatal(err)
	}
	if got, err := ReloadIfChanged(); got || err != nil {
		t.Fatalf("nothing changed: %v, %v", got, err)
	}
	if MetadataCacheTTL() != 24*time.Hour {
		t.Fatalf("default ttl = %v", MetadataCacheTTL())
	}

	touch(t, path, "metadata_cache_ttl: 3\n", 0)
	if got, err := ReloadIfChanged(); !got || err != nil {
		t.Fatalf("a created file: %v, %v", got, err)
	}
	if MetadataCacheTTL() != 72*time.Hour {
		t.Errorf("ttl after reload = %v", MetadataCacheTTL())
	}
	if got, _ := ReloadIfChanged(); got {
		t.Error("reloaded again with no change")
	}

	touch(t, path, "metadata_cache_ttl: 7\n", 2*time.Second)
	if got, _ := ReloadIfChanged(); !got || MetadataCacheTTL() != 7*24*time.Hour {
		t.Errorf("an edit of the same size: ttl = %v", MetadataCacheTTL())
	}

	touch(t, path, "metadata_cache_ttl: [unclosed\n", 4*time.Second)
	if got, err := ReloadIfChanged(); got || err == nil {
		t.Errorf("a file that does not parse: %v, %v", got, err)
	}
	if MetadataCacheTTL() != 7*24*time.Hour {
		t.Errorf("the previous config was lost: %v", MetadataCacheTTL())
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReloadIfChanged(); !got || MetadataCacheTTL() != 24*time.Hour {
		t.Errorf("a removed file: ttl = %v", MetadataCacheTTL())
	}
}

func TestReloadIfChangedReopensTheSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CNT_ROOT", t.TempDir())
	t.Setenv("CNT_EXTRA_ROOT", "")
	path, _ := GetUserConfigPath()
	prev := Global.Sources
	t.Cleanup(func() { settings.SetLayers(nil); configLayers = nil; Global.Sources = prev; ResetCatalog() })

	if err := LoadLayers(); err != nil {
		t.Fatal(err)
	}
	LoadSources()
	touch(t, path, "sources:\n  - lab: /shared/lab\n", 0)
	if got, err := ReloadIfChanged(); !got || err != nil {
		t.Fatalf("%v, %v", got, err)
	}
	if len(Global.Sources) == 0 || Global.Sources[0] != (catalog.Spec{Name: "lab", Base: "/shared/lab"}) {
		t.Errorf("sources = %+v", Global.Sources)
	}
}
