package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// The resources come from the current scheduler's directives when one sets a resource, else the
// #KEY: headers, else another scheduler's directives. Job control is read from both.
func TestScriptSourceOrder(t *testing.T) {
	const both = "#!/bin/bash\n#SBATCH --cpus-per-task=2\n#NCPU: 8\n#PARTITION: gpu\n"
	for name, tc := range map[string]struct {
		host  []string
		text  string
		cpus  int
		part  string
		isJob bool
	}{
		"headers alone, no scheduler":                           {nil, "#NCPU: 8\n#MEM: 4G\n#PARTITION: gpu\n", 8, "gpu", true},
		"directives give resources, headers name the partition": {[]string{"sbatch"}, both, 2, "gpu", true},
		"headers beat foreign ones":                             {[]string{"qsub"}, both, 8, "gpu", true},
		"a name alone is a job":                                 {nil, "#NAME: job\n", 1, "", true},
		"directives alone still work":                           {[]string{"sbatch"}, "#SBATCH --cpus-per-task=2\n", 2, "", true},
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

// A directive that sets no resource passes through and does not hide the headers.
func TestHeadersGiveResourcesBesideANonResourceDirective(t *testing.T) {
	for name, tc := range map[string]struct {
		host, directive, remaining string
	}{
		"slurm": {"sbatch", "#SBATCH --nodelist=node1", "--nodelist=node1"},
		"pbs":   {"qsub", "#PBS -V", "-V"},
		"lsf":   {"bsub", "#BSUB -G labgroup", "-G labgroup"},
	} {
		t.Run(name, func(t *testing.T) {
			withHost(t, tc.host)
			specs, err := readHeaderScript(t, "#!/bin/bash\n"+tc.directive+"\n#NCPU: 8\n#MEM: 4G\n")
			if err != nil {
				t.Fatal(err)
			}
			if !specs.HasDirectives || specs.Spec == nil || specs.Spec.CpusPerTask != 8 || specs.Spec.MemPerNodeMB != 4096 {
				t.Errorf("directives=%v spec=%+v, want the headers' 8 CPUs and 4G", specs.HasDirectives, specs.Spec)
			}
			if !slices.Contains(specs.RemainingFlags, tc.remaining) {
				t.Errorf("remaining = %v, want %q to pass through", specs.RemainingFlags, tc.remaining)
			}
		})
	}
}

// With no headers, a lone directive that sets no resource leaves the scheduler defaults.
func TestNonResourceDirectiveAloneKeepsDefaults(t *testing.T) {
	withHost(t, "sbatch")
	specs, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --nodelist=node1\n")
	if err != nil {
		t.Fatal(err)
	}
	if !specs.HasDirectives || specs.Spec == nil || specs.Spec.CpusPerTask != GetSpecDefaults().CpusPerTask {
		t.Errorf("directives=%v spec=%+v, want the defaults", specs.HasDirectives, specs.Spec)
	}
}

// A job control directive is not a resource, so the headers still give the resources.
func TestJobControlDirectiveDoesNotHideHeaders(t *testing.T) {
	withHost(t, "sbatch")
	specs, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --job-name=align\n#SBATCH --mail-user=a@b.org\n#NCPU: 4\n")
	if err != nil {
		t.Fatal(err)
	}
	if specs.Spec.CpusPerTask != 4 || specs.Control.JobName != "align" || specs.Control.MailUser != "a@b.org" {
		t.Errorf("cpus=%d control=%+v, want 4 CPUs and the directives' name and mail", specs.Spec.CpusPerTask, specs.Control)
	}
}

// A directive that sets a resource wins, and a resource header beside it is ignored.
func TestResourceDirectiveBeatsResourceHeaders(t *testing.T) {
	withHost(t, "sbatch")
	specs, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --cpus-per-task=2\n#NCPU: 8\n#MEM: lots\n")
	if err != nil {
		t.Fatal(err)
	}
	if specs.Spec.CpusPerTask != 2 {
		t.Errorf("cpus = %d, want the directive's 2", specs.Spec.CpusPerTask)
	}
}

// Job control from the headers joins the directives', field by field.
func TestJobControlComesFromBothSources(t *testing.T) {
	withHost(t, "sbatch")
	specs, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --cpus-per-task=2\n#SBATCH --partition=short\n#NAME: align\n#ACCOUNT: lab\n")
	if err != nil {
		t.Fatal(err)
	}
	if c := specs.Control; c.Partition != "short" || c.JobName != "align" || c.Account != "lab" {
		t.Errorf("control = %+v, want the directive's partition and the headers' name and account", c)
	}
}

// A field named in both a directive and a header is refused, naming both.
func TestJobControlNamedTwiceIsRefused(t *testing.T) {
	withHost(t, "sbatch")
	_, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --partition=short\n#NCPU: 4\n#PARTITION: long\n")
	if !errors.Is(err, ErrInvalidHeader) || !strings.Contains(err.Error(), "#PARTITION on line 4") ||
		!strings.Contains(err.Error(), "#SBATCH --partition=short") {
		t.Fatalf("err = %v, want both lines named", err)
	}
}

// A malformed job control header is an error even beside directives that set resources.
func TestBadJobControlHeaderIsRefusedBesideDirectives(t *testing.T) {
	withHost(t, "sbatch")
	_, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --cpus-per-task=2\n#NAME: not a name!\n")
	if !errors.Is(err, ErrInvalidHeader) || !strings.Contains(err.Error(), "#NAME on line 3") {
		t.Fatalf("err = %v, want #NAME on line 3 refused", err)
	}
}

// Directives whose resources do not parse stay unparsed, with or without headers.
func TestUnparsedDirectivesStayUnparsedWithHeaders(t *testing.T) {
	withHost(t, "sbatch")
	specs, err := readHeaderScript(t, "#!/bin/bash\n#SBATCH --nodelist=n01,n02\n#NCPU: 8\n")
	if err != nil {
		t.Fatal(err)
	}
	if !IsUnparsed(specs) {
		t.Errorf("spec = %+v, want the directives left unparsed", specs.Spec)
	}
}
