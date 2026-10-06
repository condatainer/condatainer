package config

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// fileStamp identifies a version of a config file. A missing file has the zero stamp.
type fileStamp struct {
	mod  time.Time
	size int64
}

var (
	reloadMu sync.Mutex // one reload at a time
	stampMu  sync.Mutex
	stamps   map[string]fileStamp
)

// stampConfigFiles records every config file location, including the ones that do not exist yet.
func stampConfigFiles() map[string]fileStamp {
	out := map[string]fileStamp{}
	for _, sp := range GetConfigSearchPaths() {
		var st fileStamp
		if info, err := os.Stat(sp.Path); err == nil {
			st = fileStamp{info.ModTime(), info.Size()}
		}
		out[sp.Path] = st
	}
	return out
}

// recordStamps notes the state of the config files. LoadLayers calls it before it reads them, so an edit during the read is seen by the next check.
func recordStamps() {
	now := stampConfigFiles()
	stampMu.Lock()
	stamps = now
	stampMu.Unlock()
}

// ReloadIfChanged re-reads the config files when one was created, edited or removed since they were last read.
//   - It reports whether it reloaded.
//   - A file that does not parse leaves the previous config in place and returns the error.
//   - Recipe sources are reopened. home_override and the scheduler are not re-applied.
func ReloadIfChanged() (bool, error) {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	stampMu.Lock()
	known := stamps
	stampMu.Unlock()
	if known == nil || sameStamps(known, stampConfigFiles()) {
		return false, nil
	}
	if err := LoadLayers(); err != nil {
		return false, fmt.Errorf("config not reloaded: %w", err)
	}
	reloadSources()
	return true, nil
}

// reloadSources re-resolves the recipe sources and drops the open catalog.
func reloadSources() {
	catalogMu.Lock()
	Global.Sources = layerSources()
	catalogMu.Unlock()
	ResetCatalog()
}

func sameStamps(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for path, st := range a {
		if b[path] != st {
			return false
		}
	}
	return true
}
