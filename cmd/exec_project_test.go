package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

// Outside a project, `-o` means exactly what it always meant.
func TestProjectOverlaysPassesThroughOutsideAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	got, err := projectOverlays(context.Background(), []string{"star/2.7.11b", "env.img"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "star/2.7.11b" || got[1] != "env.img" {
		t.Fatalf("overlays = %v, want them untouched", got)
	}
}

// Standing in a project, a name that is not locked is refused rather than
// resolved to whatever currently answers to it.
func TestProjectOverlaysRefusesAnUnselectedName(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}

	_, err := projectOverlays(context.Background(), []string{"star/2.7.11b"})
	if err == nil {
		t.Fatal("an unlocked name was accepted inside a project")
	}
	if !strings.Contains(err.Error(), "star/2.7.11b") {
		t.Errorf("error does not name the overlay: %v", err)
	}
	if !strings.Contains(err.Error(), "project pin") {
		t.Errorf("error does not name the remedy: %v", err)
	}
}

// A writable .img has no identity to pin, so it is mounted as written — and
// anchored on the root, which is where a project's relative paths resolve.
func TestProjectOverlaysMountsAWritableImageLiterally(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}

	got, err := projectOverlays(context.Background(), []string{"env.img"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "env.img")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("overlays = %v, want %q", got, want)
	}
}

// The suffix is a mount mode, not part of what is being addressed, and it has
// to reach the runtime.
func TestProjectOverlaysKeepsTheMountMode(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}

	got, err := projectOverlays(context.Background(), []string{"env.img:rw"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "env.img") + ":rw"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("overlays = %v, want %q", got, want)
	}
}

// A range is a build-recipe feature. Inside a project the lock says which
// version, so "any version in this range" is not a request.
func TestProjectOverlaysRefusesAVersionConstraint(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}

	_, err := projectOverlays(context.Background(), []string{"star/2.7.11b>=2.7.0"})
	if err == nil {
		t.Fatal("a constrained overlay was accepted inside a project")
	}
	if !strings.Contains(err.Error(), "constraint") {
		t.Errorf("error does not explain the refusal: %v", err)
	}
}

// A project-relative .sqf is a restore output, so it answers to the lock like
// any other selection rather than being mounted on sight.
func TestProjectOverlaysRefusesAnUnselectedProjectPath(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	writeScript(t, root, "overlays/tool.sqf", "not really an overlay\n")

	if _, err := projectOverlays(context.Background(), []string{"overlays/tool.sqf"}); err == nil {
		t.Fatal("an unlocked project path was mounted on sight")
	}
}

// Nothing to resolve is not a reason to look for a project.
func TestProjectOverlaysIgnoresAnEmptyList(t *testing.T) {
	newProject(t)

	got, err := projectOverlays(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("overlays = %v, want none", got)
	}
}

// Outside a project, there is no root to override with.
func TestProjectBaseImagePassesThroughOutsideAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	path, err := projectBaseImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("path = %q, want none outside a project", path)
	}
}

// A lock with no base pin — written before this feature, or hand-edited —
// has nothing to override with either; ensureRootBaseImage's ordinary
// fallback takes over.
func TestProjectBaseImageEmptyWithNoBasePin(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}

	path, err := projectBaseImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("path = %q, want none with no base pin", path)
	}
}

// A pinned root that is not installed here refuses rather than falling back
// to this machine's configured default — the same rule projectOverlays
// already applies to an ordinary declaration.
func TestProjectBaseImageRefusesAnUnresolvedBase(t *testing.T) {
	root := newProject(t)
	base := vendorArtifact(t, root, "ubuntu24/base", "echo base\n")
	l := lock.New()
	l.Pins[lock.BaseKey] = pinOf(base)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	_, err := projectBaseImage(context.Background())
	if err == nil {
		t.Fatal("an uninstalled root was accepted")
	}
	if !strings.Contains(err.Error(), "project restore") {
		t.Errorf("error does not name the remedy: %v", err)
	}
}

// Outside a project, there is no base pin to read a distro out of.
func TestProjectSelectedDistroEmptyOutsideAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	if got := projectSelectedDistro(); got != "" {
		t.Fatalf("distro = %q, want none outside a project", got)
	}
}

// A lock with no base pin has nothing to read either.
func TestProjectSelectedDistroEmptyWithNoBasePin(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}

	if got := projectSelectedDistro(); got != "" {
		t.Fatalf("distro = %q, want none with no base pin", got)
	}
}

// set-distro and DeriveBase only ever compose "<distro>/base", so the
// distro is the base pin's manifest name up to the slash — read from the
// vendored manifest, not from a local install.
func TestProjectSelectedDistroReadsTheBasePinsName(t *testing.T) {
	root := newProject(t)
	base := vendorArtifact(t, root, "rocky9/base", "echo base\n")
	l := lock.New()
	l.Pins[lock.BaseKey] = pinOf(base)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if got := projectSelectedDistro(); got != "rocky9" {
		t.Fatalf("distro = %q, want rocky9", got)
	}
}

// Outside a project, or with no base pin, projectDefaultDistro falls back to
// the configured default_distro rather than the project-scoped answer.
func TestProjectDefaultDistroFallsBackToConfig(t *testing.T) {
	settingstest.Override(t, "default_distro", "ubuntu24")

	t.Chdir(t.TempDir())
	if got := projectDefaultDistro(); got != "ubuntu24" {
		t.Fatalf("distro = %q, want the configured default", got)
	}
}

// --no-project is a caller outside any project, so a project's selected distro
// does not decide what a bare name expands under.
func TestNoProjectUsesTheConfiguredDistro(t *testing.T) {
	settingstest.Override(t, "default_distro", "ubuntu24")
	root := newProject(t)
	base := vendorArtifact(t, root, "rocky9/base", "echo base\n")
	l := lock.New()
	l.Pins[lock.BaseKey] = pinOf(base)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	if got := projectDefaultDistro(); got != "rocky9" {
		t.Fatalf("distro = %q, want the project's rocky9", got)
	}

	noProjectRequested = true
	t.Cleanup(func() { noProjectRequested = false })
	if got := projectDefaultDistro(); got != "ubuntu24" {
		t.Errorf("--no-project distro = %q, want the configured ubuntu24", got)
	}
	if standing, err := standingProjectHere(); err != nil || standing != nil {
		t.Errorf("--no-project still stands in a project: %v, %v", standing, err)
	}
}

func TestSplitOverlayModeSeparatesTheMountMode(t *testing.T) {
	for _, tc := range []struct{ in, value, suffix string }{
		{"env.img:rw", "env.img", ":rw"},
		{"tool.sqf:ro", "tool.sqf", ":ro"},
		{"star/2.7.11b", "star/2.7.11b", ""},
	} {
		value, suffix := splitOverlayMode(tc.in)
		if value != tc.value || suffix != tc.suffix {
			t.Errorf("splitOverlayMode(%q) = %q, %q; want %q, %q", tc.in, value, suffix, tc.value, tc.suffix)
		}
	}
}

// A command's own flags never reach Apptainer as pass-through flags, whichever
// way they are written, and the ones left over keep their order.
func TestOwnFlagsAreNotPassedToApptainer(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = []string{"condatainer", "exec", "--no-project", "--project", "/p", "--project=/q", "--nv", "bash", "-c", "true"}

	command, apptainerFlags := ParseCommandArgs(&execCmd.Command)
	if want := []string{"bash", "-c", "true"}; !slices.Equal(command, want) {
		t.Errorf("command = %v, want %v", command, want)
	}
	if want := []string{"--nv"}; !slices.Equal(apptainerFlags, want) {
		t.Errorf("apptainer flags = %v, want only %v", apptainerFlags, want)
	}
}

func TestEOwnFlagsAreNotPassedToApptainer(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = []string{"condatainer", "e", "--no-project", "--project", "/p", "-r", "--env", "A=1", "ol.sqf", "--nv", "--", "bash", "-c", "true"}

	overlays, command, apptainerFlags, err := parseEArgs(eCmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ol.sqf"}; !slices.Equal(overlays, want) {
		t.Errorf("overlays = %v, want %v", overlays, want)
	}
	if want := []string{"bash", "-c", "true"}; !slices.Equal(command, want) {
		t.Errorf("command = %v, want %v", command, want)
	}
	if want := []string{"--nv"}; !slices.Equal(apptainerFlags, want) {
		t.Errorf("apptainer flags = %v, want only %v", apptainerFlags, want)
	}
}

// A project's pinned names are what completion offers from its lock: the
// plain name/version keys, not the root, a path or a constraint.
func TestCompletionProjectReadsThePinnedNames(t *testing.T) {
	settingstest.Override(t, "default_distro", "ubuntu24")
	root := newProject(t)
	l := lock.New()
	l.Pins["fastp/1.1.0"] = pinOf(vendorArtifact(t, root, "fastp/1.1.0", "echo fastp\n"))
	l.Pins["ubuntu24/r/4.6.1"] = pinOf(vendorArtifact(t, root, "ubuntu24/r/4.6.1", "echo r\n"))
	l.Pins["samtools/1.24>=1.20"] = pinOf(vendorArtifact(t, root, "samtools/1.24", "echo samtools\n"))
	l.Pins["star"] = pinOf(vendorArtifact(t, root, "star/2.7.11b", "echo star\n"))
	l.Pins[lock.BaseKey] = pinOf(vendorArtifact(t, root, "rocky9/base", "echo base\n"))
	l.Pins[lock.PathPrefix+"overlays/tool.sqf"] = pinOf(vendorArtifact(t, root, "tool/1", "echo tool\n"))
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	distro, pinned := completionProject()
	slices.Sort(pinned)
	if want := []string{"fastp/1.1.0", "ubuntu24/r/4.6.1"}; !slices.Equal(pinned, want) {
		t.Errorf("pinned = %v, want %v", pinned, want)
	}
	if distro != "rocky9" {
		t.Errorf("distro = %q, want the project's rocky9", distro)
	}

	noProjectRequested = true
	t.Cleanup(func() { noProjectRequested = false })
	distro, pinned = completionProject()
	if len(pinned) != 0 {
		t.Errorf("--no-project still offered %v", pinned)
	}
	if distro != "ubuntu24" {
		t.Errorf("--no-project distro = %q, want the configured ubuntu24", distro)
	}
}

// Outside a project nothing is pinned, and the distro is the configured one.
func TestCompletionProjectOutsideAProject(t *testing.T) {
	settingstest.Override(t, "default_distro", "ubuntu24")
	t.Chdir(t.TempDir())

	distro, pinned := completionProject()
	if distro != "ubuntu24" || len(pinned) != 0 {
		t.Errorf("completionProject = %q, %v; want ubuntu24 and no pins", distro, pinned)
	}
}
