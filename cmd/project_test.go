package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/capsule"
	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/publish"
	"github.com/condatainer/condatainer/internal/project/restore"
	"github.com/condatainer/condatainer/internal/report"
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

// A declared but needPin request leaves a valid partial lock and a nonzero
// exit: the lock is published, but never reported as complete.
func TestProjectLockPublishesPartiallyAndFails(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")

	out, err := run(t, "project", "lock", "--json")
	if err == nil {
		t.Fatal("project lock succeeded with an needPin request")
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Requests      []struct {
			Request  string `json:"request"`
			Pinnable bool   `json:"pinnable"`
			Artifact string `json:"artifact"`
		} `json:"requests"`
		Problems []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Requests) != 1 || doc.Requests[0].Request != "star/2.7.11b" ||
		!doc.Requests[0].Pinnable || doc.Requests[0].Artifact != "" || len(doc.Problems) == 0 {
		t.Errorf("document = %+v, want the unpinned request and why", doc)
	}
	if _, err := os.Stat(lock.FilePath(root)); err != nil {
		t.Fatalf("no lock was published: %v", err)
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatalf("the published lock does not load: %v", err)
	}
	if len(loaded.Pins) != 0 {
		t.Fatalf("selections = %#v, want none invented", loaded.Pins)
	}
}

func TestProjectValidateSucceedsOnACompleteProject(t *testing.T) {
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

	if _, err := run(t, "project", "validate", "--lock-only"); err != nil {
		t.Fatalf("validate failed on a complete project: %v", err)
	}
}

// Validate also asks whether the pins are on this machine; --lock-only does not.
// A complete lock passes the first and fails the second when nothing is
// installed.
func TestProjectValidateFailsWhenAPinIsNotInstalled(t *testing.T) {
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

	if _, err := run(t, "project", "validate", "--lock-only"); err != nil {
		t.Fatalf("--lock-only failed: %v", err)
	}
	out, err := run(t, "project", "validate", "--json")
	if err == nil {
		t.Fatalf("validate passed with nothing installed:\n%s", out)
	}
	if !strings.Contains(out, "is not installed here") {
		t.Errorf("validate does not say which pin is missing:\n%s", out)
	}
}

// A declaration the lock cannot reproduce fails validate, even when every other
// declaration is pinned. Nothing in a script silences it: freezing the overlay
// is what closes it, and the finding says so.
func TestProjectValidateFailsOnAnUnpinnableDeclaration(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\n#DEP: env.img\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := runConsole(t, "project", "validate")
	if err == nil {
		t.Fatal("validate passed a project whose environment is a writable overlay")
	}
	if !strings.Contains(out, "overlay freeze") {
		t.Errorf("validate does not point at freeze:\n%s", out)
	}
}

// Push holds a project to what validate asks, so a lock that cannot reproduce
// the project is never uploaded. The refusal is reached before any registry is
// contacted.
func TestProjectPushRefusesWhatValidateRefuses(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\n#DEP: env.img\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	_, err := runConsole(t, "project", "registry", "push")
	if err == nil {
		t.Fatal("push published a project validate rejects")
	}
	if !strings.Contains(err.Error(), "env.img") {
		t.Errorf("the refusal does not name the declaration: %v", err)
	}
}

// A #DEP: below the header is inert, and validate says so rather than passing.
func TestProjectValidateFailsOnAnInertDeclaration(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n#DEP: cutadapt/5.0\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "project", "validate"); err == nil {
		t.Fatal("validate passed a project with an inert declaration")
	}
}

func TestProjectValidateJSONReportsWhatIsWrong(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")

	out, err := run(t, "project", "validate", "--json")
	if err == nil {
		t.Fatal("validate passed with nothing selected")
	}
	var report struct {
		SchemaVersion int  `json:"schema_version"`
		Valid         bool `json:"valid"`
		Problems      []any
		Unselected    []struct {
			Request string   `json:"request"`
			Scripts []string `json:"scripts"`
		} `json:"unpinned"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Valid || report.SchemaVersion != 1 || report.Problems == nil {
		t.Errorf("report = %+v", report)
	}
	if len(report.Unselected) != 1 || report.Unselected[0].Request != "star/2.7.11b" {
		t.Fatalf("unpinned = %#v", report.Unselected)
	}
	if len(report.Unselected[0].Scripts) != 1 || report.Unselected[0].Scripts[0] != "run.sh" {
		t.Errorf("scripts = %v, want the declaring script", report.Unselected[0].Scripts)
	}
}

// Reconcile drops a selection nothing declares any more. The lock is
// published right after Reconcile, before the base pin is even attempted, so
// this holds independent of whether a root can be derived.
func TestProjectLockDropsAStaleSelection(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	writeScript(t, root, "run.sh", "echo no declarations\n")

	// No base image is installed, so the run still fails deriving the base
	// pin — but that failure is reported alongside whatever Reconcile already
	// published, not instead of it.
	if _, err := run(t, "project", "lock"); err == nil {
		t.Fatal("project lock succeeded with no base image installed")
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pins) != 0 {
		t.Fatalf("a selection nothing declares survived: %#v", loaded.Pins)
	}
	if _, err := os.Stat(filepath.Join(lock.Dir(root), artifact)); !os.IsNotExist(err) {
		t.Errorf("its artifact was not pruned: %v", err)
	}
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

// --dry-run reports the plan and acquires nothing. The project below has no
// images at all, so every step is work and none of it may happen.
func TestProjectRestoreDryRunAcquiresNothing(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	relative := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(relative)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	before := lockBytes(t, root)
	if out, err := run(t, "project", "restore", "--dry-run", "--no-prebuilt"); err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(entry.Name(), ".sqf") {
			t.Errorf("a dry run produced %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after := lockBytes(t, root); after != before {
		t.Error("a dry run rewrote the lock")
	}
}

func lockBytes(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(lock.FilePath(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// set-match records the mode in the lock, and every later restore plans under it.
func TestProjectSelectMatchIsWhatRestoreUses(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	relative := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(relative)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, "project", "set-match", "exact"); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("an unknown mode was accepted: %v\n%s", err, out)
	}
	if out, err := run(t, "project", "set-match", "identity"); err != nil {
		t.Fatalf("set-match failed: %v\n%s", err, out)
	}
	out, err := run(t, "project", "restore", "--dry-run", "--no-prebuilt", "--json")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	var report struct {
		Match string `json:"match"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Match != "identity" {
		t.Errorf("match = %q, want the lock's", report.Match)
	}
}

func TestProjectRestoreDryRunJSONListsTheSteps(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	relative := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(relative)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "restore", "--dry-run", "--no-prebuilt", "--json")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	var report struct {
		SchemaVersion int    `json:"schema_version"`
		Root          string `json:"root"`
		Problems      []any  `json:"problems"`
		Steps         []struct {
			Name     string `json:"name"`
			Action   string `json:"action"`
			Artifact string `json:"artifact"`
			Direct   bool   `json:"direct"`
			Identity struct {
				Scheme string `json:"scheme"`
				SHA256 string `json:"sha256"`
			} `json:"identity"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.SchemaVersion != 1 || report.Root != root || report.Problems == nil {
		t.Errorf("document = %+v", report)
	}
	if len(report.Steps) != 1 || report.Steps[0].Action != "build" {
		t.Fatalf("steps = %#v", report.Steps)
	}
	if step := report.Steps[0]; step.Artifact != relative || !step.Direct || step.Identity.Scheme == "" || step.Identity.SHA256 == "" {
		t.Errorf("step = %+v, want its vendored directory and full locked identity", step)
	}
}

// An unsound lock stops restore before it acquires anything, and says why.
func TestProjectRestoreFailsOnAnInvalidLock(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf("provenance/star--2.7.11b@000000000000")
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, "project", "restore", "--no-prebuilt"); err == nil {
		t.Fatalf("restore accepted an invalid lock: %s", out)
	}
}

// Scanning a directory for declarations is what makes it a project, so lock has
// nothing to refuse. Every other subcommand acts on selections that must already
// exist, and must not seed a project from a mistyped path.
func TestOnlyLockCreatesAProject(t *testing.T) {
	prev := projectDir
	t.Cleanup(func() { projectDir = prev })
	projectDir = ""

	chdir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(wd) })
		// macOS hands out a symlinked temp path; Getwd resolves it.
		resolved, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}

	dir := chdir(t)
	root, err := projectRootOrInit(false)
	if err != nil || root != dir {
		t.Fatalf("projectRootOrInit = (%q, %v), want %q", root, err, dir)
	}

	if _, err := projectRoot(false); !errors.Is(err, lock.ErrNoProject) {
		t.Fatalf("projectRoot = %v, want ErrNoProject", err)
	}
	if _, err := os.Stat(filepath.Join(dir, lock.DirName)); !os.IsNotExist(err) {
		t.Error("resolving a root created cnt-lock/; only publishing should")
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

// A manual pin is what `project lock` cannot sweep, so `project unpin` is the
// only way it goes — and what it alone vendored goes with it.
func TestProjectUnpinRemovesAManualPinAndItsProvenance(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "ubuntu24/build-essential", "echo apt\n")
	l := lock.New()
	l.Pins["ubuntu24/build-essential"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := runConsole(t, "project", "unpin", "ubuntu24/build-essential")
	if err != nil {
		t.Fatalf("unpin failed: %v", err)
	}
	if !strings.Contains(out, artifact) {
		t.Errorf("the pruned artifact was not reported:\n%s", out)
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pins) != 0 {
		t.Fatalf("the pin survived: %#v", loaded.Pins)
	}
	if _, err := os.Stat(filepath.Join(lock.Dir(root), artifact)); !os.IsNotExist(err) {
		t.Errorf("its provenance was not pruned: %v", err)
	}
}

// A declared pin is removed by removing the declaration: unpinning one here
// would be undone by the next lock, possibly at a different identity.
func TestProjectUnpinRefusesADeclaredPin(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "project", "unpin", "star/2.7.11b")
	if err == nil {
		t.Fatal("a declared pin was unpinned")
	}
	if !strings.Contains(err.Error(), "project lock") {
		t.Errorf("the refusal does not name the way to remove it: %v", err)
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Pins["star/2.7.11b"]; !ok {
		t.Error("the refusal still removed the pin")
	}
}

func TestProjectUnpinRefusesWhatIsNotPinned(t *testing.T) {
	newProject(t)
	if _, err := run(t, "project", "unpin", "star/2.7.11b"); err == nil {
		t.Fatal("unpinning nothing succeeded")
	}
}

// The listing reads the lock and the records beside it, and is the only view
// that shows a manual pin — no scan produces one.
func TestProjectListShowsEveryPinAndMarksTheManualOne(t *testing.T) {
	root := newProject(t)
	declared := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	manual := vendorArtifact(t, root, "ubuntu24/build-essential", "echo apt\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(declared)
	l.Pins["ubuntu24/build-essential"] = manualPinOf(manual)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	rows, err := run(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for _, want := range []string{"star/2.7.11b", "ubuntu24/build-essential", "manual"} {
		if !strings.Contains(rows, want) {
			t.Errorf("stdout does not carry %q:\n%s", want, rows)
		}
	}
	// A named pin's key is its name, so the listing must not print it twice.
	if strings.Contains(rows, "star/2.7.11b (star/2.7.11b)") {
		t.Errorf("the name was repeated after the key it equals:\n%s", rows)
	}
	console, err := runConsole(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(console, "2 pin(s), 1 manual") {
		t.Errorf("the summary is not on the console:\n%s", console)
	}
}

// JSON carries the flag as data, never as the styled word the text view prints.
func TestProjectListJSONReportsTheManualFlag(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "ubuntu24/build-essential", "echo apt\n")
	l := lock.New()
	l.Pins["ubuntu24/build-essential"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "list", "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v", err)
	}
	var report struct {
		Pins []struct {
			Request  string `json:"request"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			Identity struct {
				SHA256 string `json:"sha256"`
			} `json:"identity"`
			Manual bool `json:"manual"`
		} `json:"pins"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("cannot decode: %v\n%s", err, out)
	}
	if len(report.Pins) != 1 {
		t.Fatalf("pins = %#v", report.Pins)
	}
	pin := report.Pins[0]
	if !pin.Manual || pin.Name != "ubuntu24/build-essential" || pin.Identity.SHA256 == "" || pin.Type == "" {
		t.Errorf("pin = %#v", pin)
	}
}

// A pin whose artifact is gone is still listed: the key is what addresses it,
// and hiding it would hide the thing to unpin.
func TestProjectListReportsAnUnreadableArtifact(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(lock.Dir(root), artifact)); err != nil {
		t.Fatal(err)
	}

	rows, err := run(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(rows, "star/2.7.11b") || !strings.Contains(rows, "unreadable") {
		t.Errorf("a broken pin was not listed as one:\n%s", rows)
	}
}

// In JSON the broken pin is listed too, and the reason is a problem named by the pin.
func TestProjectListJSONReportsAnUnreadableArtifactAsAProblem(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(lock.Dir(root), artifact)); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "list", "--json")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Pins          []struct{ Request string }
		Problems      []struct{ Name, Message string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Pins) != 1 || len(doc.Problems) != 1 || doc.Problems[0].Name != "star/2.7.11b" {
		t.Errorf("document = %+v", doc)
	}
}

// A path key addresses a file and says nothing about what is in it, so the name
// is printed after it — the one case where the second column carries something.
func TestProjectListNamesWhatAPathPinHolds(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "combined/1.0", "echo combined\n")
	l := lock.New()
	l.Pins["path:overlays/combined.sqf"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	rows, err := run(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(rows, "path:overlays/combined.sqf (combined/1.0)") {
		t.Errorf("a path pin does not name its artifact:\n%s", rows)
	}
}

// An equivalent artifact stands in, and the note says which inputs differ. The
// field names hold colons themselves, so only the colon-and-space ends one.
func TestEquivalentNoteNamesTheInputsThatDiffer(t *testing.T) {
	result := restore.Result{
		Identity: meta.KeyRef{Scheme: "identity-v1", SHA256: "41ab1c2d3e4f5a6b7c8d"},
		Found:    meta.KeyRef{Scheme: "identity-v1", SHA256: "9f2b7e04a1c3d5e6f708"},
		Diffs: []string{
			"src:gtf: 9f2b7e04a1c3 -> 3c81d5e2b7a4",
			"dep:samtools/1.23.1: 11aa22bb33cc -> 44dd55ee66ff",
		},
	}
	got := equivalentNote(result)
	for _, want := range []string{"equivalent, not 41ab1c2d3e4f;", "differs: src:gtf, dep:samtools/1.23.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("note = %q, want it to contain %q", got, want)
		}
	}
	// Digests are for --json, not a restore of many artifacts.
	if strings.Contains(got, "3c81d5e2b7a4") {
		t.Errorf("note = %q carries a digest", got)
	}
}

// With no diffs to name, the note is what it was before diffs existed.
func TestEquivalentNoteWithoutDiffsNamesNothing(t *testing.T) {
	got := equivalentNote(restore.Result{Identity: meta.KeyRef{Scheme: "identity-v1", SHA256: "41ab1c2d3e4f5a6b7c8d"}})
	if want := " (equivalent, not 41ab1c2d3e4f)"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
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

func TestProjectRegistrySetRecordsTheCodeURL(t *testing.T) {
	root := newProject(t)
	cmd := newProjectRegistrySetCmd()
	if cmd.Flags().Lookup("source") != nil {
		t.Error("project registry set still has --source")
	}
	if err := cmd.Flags().Set("code-url", "https://github.com/my-lab/rnaseq"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"ghcr.io/my-lab/rnaseq/cnt"}); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if l.Source != "https://github.com/my-lab/rnaseq" || l.OCI.Push != "ghcr.io/my-lab/rnaseq/cnt" {
		t.Errorf("lock source %q, push %q", l.Source, l.OCI.Push)
	}
}

// captureStderr executes the project command tree with args and returns what it printed to stderr.
func captureStderr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	stderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
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

func TestProjectRegistrySetSaysWhenTheSourceCameFromOrigin(t *testing.T) {
	root := newProject(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:my-lab/rnaseq.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	out, err := captureStderr(t, "project", "registry", "set", "ghcr.io/my-lab/rnaseq/cnt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "https://github.com/my-lab/rnaseq") || !strings.Contains(out, "taken from the origin remote") {
		t.Errorf("output does not say the source came from origin:\n%s", out)
	}

	out, err = captureStderr(t, "project", "registry", "set", "ghcr.io/my-lab/rnaseq/cnt", "--code-url", "https://example.org/x/y")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "origin remote") || !strings.Contains(out, "https://example.org/x/y") {
		t.Errorf("an explicit --code-url was reported as from origin:\n%s", out)
	}
}

func TestProjectRegistrySetNotesAnOriginWithAPort(t *testing.T) {
	root := newProject(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://git.lab.example:8443/team/p.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	out, err := captureStderr(t, "project", "registry", "set", "ghcr.io/my-lab/rnaseq/cnt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "names a port") || !strings.Contains(out, "--code-url") || strings.Contains(out, "  source ") {
		t.Errorf("output does not note the port:\n%s", out)
	}
}

func ignoreList(t *testing.T, root string) string {
	t.Helper()
	text, err := os.ReadFile(lock.IgnorePath(root))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(text)
}

// A line is anchored at the project root, wherever the command is typed.
func TestProjectIgnoreAnchorsPathsAtTheRoot(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "src/tmp/a.py", "")
	writeScript(t, root, "data/b.sh", "")
	t.Chdir(filepath.Join(root, "src"))

	if _, err := run(t, "project", "ignore", "tmp/*.py", "../data", "/notebooks/"); err != nil {
		t.Fatal(err)
	}
	if got, want := ignoreList(t, root), "/data\n/notebooks/\n/src/tmp/*.py\n"; got != want {
		t.Errorf("list = %q, want %q", got, want)
	}
	if out, err := run(t, "project", "ignore", "--list"); err != nil || out != "/data\n/notebooks/\n/src/tmp/*.py\n" {
		t.Errorf("--list = %q, %v", out, err)
	}
}

func TestProjectIgnoreRemovesALine(t *testing.T) {
	root := newProject(t)
	if _, err := run(t, "project", "ignore", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "project", "ignore", "--remove", "a"); err != nil {
		t.Fatal(err)
	}
	if got := ignoreList(t, root); got != "/b\n" {
		t.Errorf("list = %q", got)
	}
	if _, err := run(t, "project", "ignore", "--remove", "a"); err == nil {
		t.Error("removing a line that is not there succeeded")
	}
	if _, err := run(t, "project", "ignore", "--remove", "b"); err != nil {
		t.Fatal(err)
	}
	if got := ignoreList(t, root); got != "" {
		t.Errorf("an empty list leaves a file: %q", got)
	}
}

func TestProjectIgnoreSkipsWhatGitignoreCovers(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, ".gitignore", "venv/\n")
	if _, err := run(t, "project", "ignore", "venv/lib", "other"); err != nil {
		t.Fatal(err)
	}
	if got := ignoreList(t, root); got != "/other\n" {
		t.Errorf("list = %q", got)
	}
}

func TestProjectIgnoreRefusesAPathOutsideTheProject(t *testing.T) {
	newProject(t)
	_, err := run(t, "project", "ignore", "../elsewhere")
	if err == nil || !strings.Contains(err.Error(), "outside the project") {
		t.Errorf("err = %v", err)
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

// A flag nobody classified is one a submitted rebuild might silently drop.
func TestEveryRestoreFlagHasAJobRule(t *testing.T) {
	cmd := newProjectRestoreCmd()
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if _, ok := restoreJobRules[f.Name]; !ok && f.Name != "help" {
			t.Errorf("--%s has no entry in restoreJobRules: say whether a submitted rebuild repeats it", f.Name)
		}
	})
	for name := range restoreJobRules {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("restoreJobRules lists --%s, which restore does not have", name)
		}
	}
}

// Each outcome of a run lands on its own step, and a path pin is told apart by its path.
func TestRestoreDocMergesTheRunIntoThePlan(t *testing.T) {
	key := func(hex string) meta.KeyRef { return meta.KeyRef{Scheme: "identity-v1", SHA256: hex} }
	plan := &restore.Plan{Root: "/p", Match: restore.MatchEquivalence, Steps: []restore.Step{
		{Artifact: "a", Name: "a", Identity: key("aa"), Action: restore.ActionBuild},
		{Artifact: "b", Name: "b", Identity: key("bb"), Action: restore.ActionBuild, DependsOn: []string{"a"}},
		{Artifact: "c", Name: "c", Identity: key("cc"), Action: restore.ActionFetch},
		{Artifact: "d", Name: "d", Identity: key("dd"), Action: restore.ActionBuild},
		{Artifact: "e", Name: "e", Identity: key("ee"), Action: restore.ActionAdopt, Destination: "x.sqf"},
		{Artifact: "e", Name: "e", Identity: key("ee"), Action: restore.ActionAdopt, Destination: "y.sqf"},
	}}
	run := &restore.Report{Plan: plan,
		Results: []restore.Result{
			{Artifact: "c", Name: "c", Identity: key("cc"), Found: key("c2"), Outcome: restore.OutcomeBuilt, Path: "/img/c.sqf"},
			{Artifact: "e", Name: "e", Identity: key("ee"), Outcome: restore.OutcomeAdopted, Destination: "y.sqf", Path: "/p/y.sqf"},
		},
		Failures:  []restore.Failure{{Artifact: "a", Name: "a", Reason: "boom"}},
		Blocked:   []restore.Blocked{{Artifact: "b", Name: "b", Cause: "a"}},
		Submitted: []restore.Submitted{{Artifact: "d", Name: "d", JobID: "42", DependsOn: []string{"7"}}},
	}

	doc := restoreDoc("/p", lock.New(), plan, run)
	if doc.Root != "/p" || doc.Match != "equivalence" {
		t.Errorf("root, match = %q, %q", doc.Root, doc.Match)
	}
	want := []struct {
		outcome report.Outcome
		action  report.Action
	}{
		{report.OutcomeFailed, report.ActionBuild},
		{report.OutcomeSkipped, report.ActionBuild},
		{report.OutcomeDone, report.ActionBuild},
		{report.OutcomeSubmitted, report.ActionBuild},
		{report.OutcomeSkipped, report.ActionAdopt},
		{report.OutcomeDone, report.ActionAdopt},
	}
	if len(doc.Steps) != len(want) {
		t.Fatalf("steps = %d, want %d", len(doc.Steps), len(want))
	}
	for i, w := range want {
		step := doc.Steps[i]
		if step.Outcome != w.outcome || step.Action != w.action {
			t.Errorf("step %d = %s/%s, want %s/%s", i, step.Action, step.Outcome, w.action, w.outcome)
		}
	}
	if got := doc.Steps[4]; got.Reason != "not reached" {
		t.Errorf("unreached step reason = %q", got.Reason)
	}
	// The fetch fell back to a build, and a stand-in is reported with its full key.
	if got := doc.Steps[2]; got.Found != key("c2") || got.Path != "/img/c.sqf" {
		t.Errorf("fetched-then-built step = %+v", got)
	}
	if got := doc.Steps[0]; got.Error != "boom" {
		t.Errorf("failed step error = %q", got.Error)
	}
	if got := doc.Steps[1]; got.Cause != "a" || got.Reason == "" {
		t.Errorf("blocked step = %+v", got)
	}
	if got := doc.Steps[3]; got.JobID != "42" || got.Runs != report.RunsJob || len(got.WaitsFor) != 1 {
		t.Errorf("submitted step = %+v", got)
	}
}

// A run that planning stopped has no outcomes, and a dry run has none either.
func TestRestoreDocHasNoOutcomeBeforeAnythingRan(t *testing.T) {
	plan := &restore.Plan{Root: "/p", Steps: []restore.Step{{Artifact: "a", Name: "a", Action: restore.ActionBuild}},
		Problems: []string{"x cannot be built"}}
	for name, run := range map[string]*restore.Report{"dry run": nil, "stopped": {Plan: plan, Problems: plan.Problems}} {
		doc := restoreDoc("/p", lock.New(), plan, run)
		if doc.Steps[0].Outcome != "" || len(doc.Problems) != 1 {
			t.Errorf("%s: step = %+v, problems = %v", name, doc.Steps[0], doc.Problems)
		}
	}
}

// Each disposition lands on its own record, and a run adds the outcome.
func TestPushDocReportsEachStep(t *testing.T) {
	remote := &lock.Remote{Repository: "ghcr.io/lab/cnt", ManifestDigest: "sha256:aa"}
	plan := &publish.Plan{Repository: "ghcr.io/lab/cnt", Audience: "public", Steps: []publish.Step{
		{Artifact: "a", Name: "a", Disposition: publish.Upload, Tags: []string{"a__1"}, Channels: []string{"conda-forge"}},
		{Artifact: "b", Name: "b", Disposition: publish.Present, Remote: remote},
		{Artifact: "c", Name: "c", Disposition: publish.Served, Remote: &lock.Remote{Repository: "ghcr.io/src/cnt", ManifestDigest: "sha256:cc"}},
		{Artifact: "d", Name: "d", Disposition: publish.Upload, Tags: []string{"d__1"}},
		{Artifact: "e", Name: "e", Disposition: publish.Refused, Reason: "not allowed"},
	}}

	preview := pushDoc("/p", plan, &lock.Verified{}, nil)
	if preview.Repository != "ghcr.io/lab/cnt" || len(preview.Pushes) != 5 {
		t.Fatalf("preview = %+v", preview)
	}
	for i, want := range []report.Outcome{"", "", "", "", report.OutcomeRefused} {
		if got := preview.Pushes[i].Outcome; got != want {
			t.Errorf("preview %d outcome = %q, want %q", i, got, want)
		}
	}
	if !preview.Pushes[1].Existing || preview.Pushes[1].Digest != "sha256:aa" || len(preview.Pushes[0].Channels) != 1 {
		t.Errorf("present or channels wrong: %+v %+v", preview.Pushes[1], preview.Pushes[0])
	}
	if got := preview.Pushes[2]; got.Served == nil || got.Served.Repository != "ghcr.io/src/cnt" || got.Digest != "" {
		t.Errorf("served = %+v", got)
	}

	uploaded := plan.Steps[0]
	uploaded.Path, uploaded.Remote = "/img/a.sqf", &lock.Remote{Repository: "ghcr.io/lab/cnt", ManifestDigest: "sha256:new"}
	run := &publish.Report{
		Published: []publish.Step{uploaded, plan.Steps[1], plan.Steps[2]},
		Failures:  []publish.Failure{{Step: plan.Steps[3], Reason: "network down"}},
	}
	done := pushDoc("/p", plan, &lock.Verified{}, run)
	for i, want := range []report.Outcome{report.OutcomeDone, report.OutcomeDone, report.OutcomeSkipped, report.OutcomeFailed} {
		if got := done.Pushes[i].Outcome; got != want {
			t.Errorf("run %d outcome = %q, want %q", i, got, want)
		}
	}
	if got := done.Pushes[0]; got.Digest != "sha256:new" || got.Path != "/img/a.sqf" {
		t.Errorf("uploaded = %+v", got)
	}
	if got := done.Pushes[3]; got.Error != "network down" {
		t.Errorf("failed = %+v", got)
	}
}

// A project validate rejects is refused with the reasons in the document, and the exit is non-zero.
func TestProjectPushJSONPrintsWhyItWasRefused(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\n#DEP: env.img\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "registry", "push", "--json")
	if err == nil {
		t.Fatal("push published a project validate rejects")
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Pushes        []any
		Problems      []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Pushes) != 0 || len(doc.Problems) == 0 || !strings.Contains(doc.Problems[0].Message+doc.Problems[len(doc.Problems)-1].Message, "env.img") {
		t.Errorf("document = %+v", doc)
	}
}
