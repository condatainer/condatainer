package scheduler

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// captureEnvNotes records what the user would be told was overridden.
func captureEnvNotes(t *testing.T) *[]string {
	t.Helper()
	var notes []string
	prev := noteEnvOverride
	noteEnvOverride = func(theirs, ours string) { notes = append(notes, theirs+" -> "+ours) }
	t.Cleanup(func() { noteEnvOverride = prev })
	return &notes
}

// A SLURM job is submitted with the whole environment. A script's own limit is
// replaced with a note, keeping the variables it added; one that already asks
// for everything is kept without a note.
func TestSlurmFullEnv(t *testing.T) {
	t.Setenv("SBATCH_EXPORT", "")
	tests := []struct {
		name       string
		flags      []string
		wantExport string
		wantNote   bool
	}{
		{"no directive", []string{"--qos=high"}, "--export=ALL", false},
		{"limited", []string{"--qos=high", "--export=NONE"}, "--export=ALL", true},
		{"limited with assignments", []string{"--export=NONE,FOO=bar,PATH"}, "--export=ALL,FOO=bar", true},
		{"already everything", []string{"--export=ALL,FOO=bar"}, "--export=ALL,FOO=bar", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notes := captureEnvNotes(t)
			rest, export := slurmFullEnv(tt.flags)
			if export != tt.wantExport {
				t.Errorf("export = %q, want %q", export, tt.wantExport)
			}
			if slices.ContainsFunc(rest, func(f string) bool { return strings.HasPrefix(f, "--export") }) {
				t.Errorf("the script's own --export is still passed through: %v", rest)
			}
			if got := len(*notes) > 0; got != tt.wantNote {
				t.Errorf("notes = %v, want one: %v", *notes, tt.wantNote)
			}
		})
	}
}

// SBATCH_EXPORT limits the environment from outside the script, so it is
// reported too.
func TestSlurmFullEnvNotesSbatchExport(t *testing.T) {
	t.Setenv("SBATCH_EXPORT", "NONE")
	notes := captureEnvNotes(t)
	slurmFullEnv(nil)
	if len(*notes) != 1 || !strings.HasPrefix((*notes)[0], "This shell sets SBATCH_EXPORT=NONE") {
		t.Errorf("notes = %v, want one naming SBATCH_EXPORT", *notes)
	}
}

// The generated script carries the --export directive, and sbatch is given the
// same value on its command line, where it wins over SBATCH_EXPORT.
func TestSlurmSubmitRepeatsExportOnTheCommandLine(t *testing.T) {
	t.Setenv("SBATCH_EXPORT", "")
	captureEnvNotes(t)
	jobSpec := &JobSpec{
		Name:    "test/job",
		Command: "echo hello",
		Specs: &ScriptSpecs{
			RemainingFlags: []string{"--export=NONE,FOO=bar"},
			Spec:           &ResourceSpec{CpusPerTask: 1},
		},
	}
	scriptPath, err := newTestSlurmScheduler().CreateScriptWithSpec(jobSpec, t.TempDir())
	if err != nil {
		t.Fatalf("CreateScriptWithSpec: %v", err)
	}
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "#SBATCH --export=ALL,FOO=bar\n") || strings.Contains(string(content), "NONE") {
		t.Errorf("script does not carry the full-environment directive alone:\n%s", content)
	}
	if args := buildSlurmSubmitArgs(nil, scriptPath); args[0] != "--export=ALL,FOO=bar" {
		t.Errorf("sbatch arguments = %v, want --export first", args)
	}
}

// A PBS script asks for the whole environment once, whether or not the user's
// script already did.
func TestPbsScriptCarriesTheEnvironment(t *testing.T) {
	for name, flags := range map[string][]string{
		"added":           nil,
		"already present": {"-V"},
	} {
		t.Run(name, func(t *testing.T) {
			jobSpec := &JobSpec{
				Name:    "test/job",
				Command: "echo hello",
				Specs:   &ScriptSpecs{RemainingFlags: flags, Spec: &ResourceSpec{CpusPerTask: 1}},
			}
			scriptPath, err := newTestPbsScheduler().CreateScriptWithSpec(jobSpec, t.TempDir())
			if err != nil {
				t.Fatalf("CreateScriptWithSpec: %v", err)
			}
			content, err := os.ReadFile(scriptPath)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(content), "#PBS -V\n"); n != 1 {
				t.Errorf("script has %d `#PBS -V` lines, want 1:\n%s", n, content)
			}
		})
	}
}

// LSF copies the environment unless a script says otherwise, so only a limiting
// -env is rewritten.
func TestLsfFullEnv(t *testing.T) {
	tests := []struct {
		name     string
		flags    []string
		want     []string
		wantNote bool
	}{
		{"no directive", []string{"-q long"}, []string{"-q long"}, false},
		{"limited", []string{"-q long", `-env "none, FOO=bar"`}, []string{"-q long", `-env "all, FOO=bar"`}, true},
		{"already everything", []string{`-env "all, FOO=bar"`}, []string{`-env "all, FOO=bar"`}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notes := captureEnvNotes(t)
			if got := lsfFullEnv(tt.flags); !slices.Equal(got, tt.want) {
				t.Errorf("flags = %q, want %q", got, tt.want)
			}
			if got := len(*notes) > 0; got != tt.wantNote {
				t.Errorf("notes = %v, want one: %v", *notes, tt.wantNote)
			}
		})
	}
}

// An HTCondor submit file always sets getenv to True, once.
func TestHTCondorFullEnv(t *testing.T) {
	tests := []struct {
		name     string
		flags    []string
		wantNote bool
	}{
		{"no directive", []string{"request_disk = 1GB"}, false},
		{"limited", []string{"getenv = False", "request_disk = 1GB"}, true},
		{"already everything", []string{"getenv = true"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notes := captureEnvNotes(t)
			got := htcondorFullEnv(tt.flags)
			var getenv []string
			for _, flag := range got {
				if strings.HasPrefix(strings.ToLower(flag), "getenv") {
					getenv = append(getenv, flag)
				}
			}
			if !slices.Equal(getenv, []string{"getenv = True"}) {
				t.Errorf("getenv lines = %q, want one set to True (all flags: %q)", getenv, got)
			}
			if got := len(*notes) > 0; got != tt.wantNote {
				t.Errorf("notes = %v, want one: %v", *notes, tt.wantNote)
			}
		})
	}
}

// A script that is not one of ours carries no --export line, and gets no extra
// sbatch argument.
func TestSlurmExportArgWithoutDirective(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.sbatch")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := slurmExportArg(path); got != "" {
		t.Errorf("slurmExportArg = %q, want none", got)
	}
}
