package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestParseGPU(t *testing.T) {
	for in, want := range map[string]GpuSpec{
		"2":       {Count: 2},
		"a100":    {Type: "a100", Count: 1},
		"A100:2":  {Type: "a100", Count: 2},
		" h100 ":  {Type: "h100", Count: 1},
		"a100: 4": {Type: "a100", Count: 4},
	} {
		got, err := ParseGPU(in)
		if err != nil || *got != want {
			t.Errorf("ParseGPU(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-1", "a100:0", "a100:x", "a100:", ":2"} {
		if _, err := ParseGPU(bad); err == nil {
			t.Errorf("ParseGPU(%q) was accepted", bad)
		}
	}
}

func TestParseResourceHeaders(t *testing.T) {
	text := "#!/bin/bash\n#NCPU: 8 ## threads\n#MEM: 16\n#TIME: 2\n#GPU: a100:2\n#DESC: ignored\n"
	h, err := ParseResourceHeaders(utils.ScanAnnotations([]byte(text)))
	if err != nil {
		t.Fatal(err)
	}
	if h.CpusPerTask != 8 || h.MemPerNodeMB != 16*1024 || h.Time != 2*time.Hour || h.Gpu == nil || h.Gpu.Count != 2 {
		t.Errorf("headers = %+v", h)
	}
	if h.Lines["#NCPU"] != 2 || h.Lines["#GPU"] != 5 || len(h.Lines) != 4 {
		t.Errorf("lines = %v", h.Lines)
	}

	text = "#NCPU: many\n#MEM: 4G\n#TIME: soon\n#GPU: a100:0\n#NCPU: 2\n#MEM:\n"
	h, err = ParseResourceHeaders(utils.ScanAnnotations([]byte(text)))
	if err == nil {
		t.Fatal("bad values were accepted")
	}
	for _, want := range []string{"#NCPU on line 1", "#TIME on line 3", "#GPU on line 4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if h.MemPerNodeMB != 4096 || h.CpusPerTask != 2 {
		t.Errorf("the headers that parsed were dropped: %+v", h)
	}

	_, err = ParseResourceHeaders(utils.ScanAnnotations([]byte("#MEM: 4G\n#MEM: 8G\n")))
	if err == nil || !strings.Contains(err.Error(), "#MEM on line 2: already set on line 1") {
		t.Errorf("a repeated header: %v", err)
	}
}

// withHost makes DetectType find the named scheduler binaries, and no others.
func withHost(t *testing.T, binaries ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range binaries {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func readHeaderScript(t *testing.T, text string) (*ScriptSpecs, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.sh")
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	return ReadScriptSpecsFromPath(path)
}

// A script is read from one source: the current scheduler's directives, else the
// #KEY: headers, else another scheduler's directives. Sources are not merged.
func TestScriptSourceOrder(t *testing.T) {
	const both = "#!/bin/bash\n#SBATCH --cpus-per-task=2\n#NCPU: 8\n#PARTITION: gpu\n"
	for name, tc := range map[string]struct {
		host  []string
		text  string
		cpus  int
		part  string
		isJob bool
	}{
		"headers alone, no scheduler":  {nil, "#NCPU: 8\n#MEM: 4G\n#PARTITION: gpu\n", 8, "gpu", true},
		"current directives beat them": {[]string{"sbatch"}, both, 2, "", true},
		"headers beat foreign ones":    {[]string{"qsub"}, both, 8, "gpu", true},
		"a name alone is no job":       {nil, "#NAME: job\n", 1, "", false},
		"directives alone still work":  {[]string{"sbatch"}, "#SBATCH --cpus-per-task=2\n", 2, "", true},
	} {
		t.Run(name, func(t *testing.T) {
			withHost(t, tc.host...)
			specs, err := readHeaderScript(t, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if specs.HasDirectives != tc.isJob || specs.Spec.CpusPerTask != tc.cpus || specs.Control.Partition != tc.part {
				t.Errorf("job=%v cpus=%d partition=%q; want job=%v cpus=%d partition=%q",
					specs.HasDirectives, specs.Spec.CpusPerTask, specs.Control.Partition, tc.isJob, tc.cpus, tc.part)
			}
		})
	}
}

func TestHeaderScriptSpecs(t *testing.T) {
	withHost(t)
	specs, err := readHeaderScript(t, "#NCPU: 4\n#MEM: 8G\n#TIME: 3h\n#GPU: a100:2\n#NAME: align\n#OUTPUT: out.log\n#ERROR: err.log\n#ACCOUNT: lab\n")
	if err != nil {
		t.Fatal(err)
	}
	s, c := specs.Spec, specs.Control
	if s.CpusPerTask != 4 || s.MemPerNodeMB != 8192 || s.Time != 3*time.Hour || s.Gpu == nil || s.Gpu.Count != 2 || s.Nodes != 1 {
		t.Errorf("spec = %+v", s)
	}
	if c.JobName != "align" || c.Stdout != "out.log" || c.Stderr != "err.log" || c.Account != "lab" {
		t.Errorf("control = %+v", c)
	}

	_, err = readHeaderScript(t, "#NCPU: many\n")
	if !errors.Is(err, ErrInvalidHeader) || !strings.Contains(err.Error(), "#NCPU on line 1") {
		t.Errorf("a bad value: %v", err)
	}
}
