package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func layerFromYAML(t *testing.T, typ, text string) *Layer {
	t.Helper()
	_, root, err := parseRoot([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return &Layer{Type: typ, root: root}
}

func TestLayerReadsEveryShape(t *testing.T) {
	l := layerFromYAML(t, "user", `
# hand-written
build:
  ncpus: 8
  mem: 16g
"scheduler.partition": gpu   # flat dotted key
Autoload_GPU: "1"
nested_run:
helper:
  notification: ""
channels: conda-forge bioconda
bind: [/a, /b]
`)
	if got := l.GetInt("build.ncpus"); got != 8 {
		t.Errorf("build.ncpus = %d", got)
	}
	if got := l.GetString("build.mem"); got != "16g" {
		t.Errorf("build.mem = %q", got)
	}
	if got := l.GetString("scheduler.partition"); got != "gpu" {
		t.Errorf("flat key = %q", got)
	}
	if !l.GetBool("autoload_gpu") {
		t.Error(`"1" reads as true, case-insensitively`)
	}
	if l.InConfig("nested_run") {
		t.Error("a null value is unset")
	}
	if !l.InConfig("helper.notification") || l.GetString("helper.notification") != "" {
		t.Error("an empty string is set")
	}
	if got := l.GetStringSlice("channels"); !slices.Equal(got, []string{"conda-forge", "bioconda"}) {
		t.Errorf("channels = %v", got)
	}
	if got := l.GetStringSlice("bind"); !slices.Equal(got, []string{"/a", "/b"}) {
		t.Errorf("bind = %v", got)
	}
	want := []string{"build.ncpus", "build.mem", "scheduler.partition", "autoload_gpu", "nested_run", "helper.notification", "channels", "bind"}
	if got := l.Keys(); !slices.Equal(got, want) {
		t.Errorf("Keys = %v", got)
	}
}

func TestLayerWriteKeepsCommentsAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const start = "# mine\nzeta: 1  # keep\nbuild:\n  ncpus: 2  # cores\n  mem: 8g\nalpha: x\n"
	if err := os.WriteFile(path, []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateConfigKey(path, "build.ncpus", 4); err != nil {
		t.Fatal(err)
	}
	if err := UpdateConfigKey(path, "scheduler.slurm.emit_mem", false); err != nil {
		t.Fatal(err)
	}
	if err := DeleteConfigKey(path, "build.mem"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "# mine\nzeta: 1 # keep\nbuild:\n  ncpus: 4 # cores\nalpha: x\nscheduler:\n  slurm:\n    emit_mem: false\n"
	if strings.ReplaceAll(string(data), "  # ", " # ") != want {
		t.Errorf("file =\n%s\nwant\n%s", data, want)
	}
}

func TestDeleteConfigKeyPrunesEmptyParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := UpdateConfigKey(path, "scheduler.slurm.emit_mem", true); err != nil {
		t.Fatal(err)
	}
	if err := DeleteConfigKey(path, "scheduler.slurm.emit_mem"); err != nil {
		t.Fatal(err)
	}
	if keys := ReadConfigAllKeys(path); len(keys) != 0 {
		t.Errorf("keys left: %v", keys)
	}
}

func TestConcurrentWritesDoNotLoseKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var wg sync.WaitGroup
	keys := []string{"build.ncpus", "build.time", "autoload_gpu", "channels", "nested_run", "scheduler.account"}
	for _, k := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := UpdateConfigKey(path, k, "v"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := ReadConfigAllKeys(path); len(got) != len(keys) {
		t.Errorf("keys = %v, want all of %v", got, keys)
	}
}
