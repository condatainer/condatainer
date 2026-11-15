package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/capsule"
	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newProject builds a project root with cnt-lock/ and chdirs into it, so the
// commands exercise their own upward root discovery.
func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, lock.DirName), 0o775); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	projectDir = ""
	t.Cleanup(func() { projectDir = "" })
	return root
}

func writeScript(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"+body), 0o664); err != nil {
		t.Fatal(err)
	}
}

// vendorArtifact puts a verifiable artifact into cnt-lock/provenance/ and returns
// its relative path, standing in for a selection that already happened.
func vendorArtifact(t *testing.T, root, name, recipe string) string {
	t.Helper()
	manifest := meta.Manifest{
		SchemaVersion: meta.SchemaVersion,
		Name:          name,
		Type:          artifact.TypeApp,
		BuildType:     "script",
		// This host, in uname form: a fixture claiming another architecture
		// would be refused by restore's platform check before anything else.
		Platform: meta.NativePlatform(),
		Source:   meta.Source{Files: []string{meta.RecipeFileName}},
	}
	derived, err := key.Generate(manifest, key.Sources{meta.RecipeFileName: []byte(recipe)})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Keys = derived.Keys()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	relative, err := lock.StageEntry(root, capsule.EntryName(name, manifest.Keys.Identity.Digest()),
		map[string][]byte{meta.FileName: data, meta.RecipeFileName: []byte(recipe)})
	if err != nil {
		t.Fatal(err)
	}
	return relative
}

// resetFlags returns every project flag to its default. Cobra builds the
// command tree once, so a flag set by one test would otherwise persist into the
// next and make it pass for the wrong reason.
func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, child := range cmd.Commands() {
		resetFlags(child)
	}
}

// run executes the project command tree with args and captures stdout.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags(projectCmd)
	stdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write

	rootCmd.SetArgs(args)
	runErr := rootCmd.Execute()

	write.Close()
	os.Stdout = stdout
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := read.Read(buf)
		out.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	read.Close()
	return out.String(), runErr
}

func TestProjectFlagSelectsTheNamedRoot(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	base := vendorArtifact(t, root, "ubuntu24/base", "echo base\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	l.Pins[lock.BaseKey] = pinOf(base)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	elsewhere := t.TempDir()
	t.Chdir(elsewhere)
	if _, err := run(t, "project", "validate", "--lock-only", "--project", root); err != nil {
		t.Fatalf("--project did not select the named root: %v", err)
	}
}

// runConsole captures what utils.Print* writes, which is stderr — run captures
// stdout, where only --json output lands.
func runConsole(t *testing.T, args ...string) (string, error) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = write

	_, runErr := run(t, args...)

	write.Close()
	os.Stderr = stderr
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := read.Read(buf)
		out.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	read.Close()
	return out.String(), runErr
}

// --validate reads a .sqf; anything else is refused before it reads a byte.
func TestInfoValidateRefusesAnythingButASquashFS(t *testing.T) {
	img := filepath.Join(t.TempDir(), "env.img")
	if err := os.WriteFile(img, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runConsole(t, "info", img, "--validate")
	if err == nil {
		t.Fatalf("--validate accepted an .img:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--validate reads a .sqf") {
		t.Errorf("error = %v", err)
	}
}

// A submitted job re-runs create from the name alone, so everything that changes
// what it builds or where it lands travels as a flag; submission and mode flags
// do not.
func TestCreateJobFlagsCarryWhatChangesTheBuild(t *testing.T) {
	savedSources, savedLayer := createSources, createLayer
	settingstest.ClearFlags(t)
	t.Cleanup(func() {
		createSources, createLayer = savedSources, savedLayer
		createFlagSet.VisitAll(func(f *pflag.Flag) { f.Changed = false })
	})
	createSources, createLayer = []string{"lab"}, "u"
	args := []string{"-c", "conda-forge", "-c", "bioconda", "--block-size", "256k", "--data-block-size", "1m", "--always-submit-data", "--no-submit"}
	if err := createFlagSet.Parse(args); err != nil {
		t.Fatal(err)
	}

	got := strings.Join(createJobFlags(), " ")
	want := "--source lab --layer u --block-size 256k --channel conda-forge --channel bioconda --data-block-size 1m"
	if got != want {
		t.Errorf("flags = %q, want %q", got, want)
	}
}

func TestPrintSymlinksWarnsForScriptsAndNotesFolders(t *testing.T) {
	out, _ := stderrOf(t, func() error {
		printSymlinks([]string{"a/step.sh", "b.sh"}, []string{"data", "results"})
		return nil
	})
	for _, want := range []string{"Symlink not scanned: a/step.sh, b.sh", "Symlinked folders skipped: data, results"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if out, _ := stderrOf(t, func() error { printSymlinks(nil, nil); return nil }); out != "" {
		t.Errorf("nothing skipped printed %q", out)
	}
}
