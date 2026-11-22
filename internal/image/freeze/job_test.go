package freeze

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A path with a space or a comma cannot be written into a --fusemount specification, so it is reached through a link in the job's directory.
func TestBuildJobLinksAPathItCannotPass(t *testing.T) {
	dir, src := t.TempDir(), filepath.Join(t.TempDir(), "a dir", "x,y.sqf")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mnt := t.TempDir()

	spec, err := buildJob(dir, []Mount{{Bin: "/opt/squashfuse_ll", Args: []string{"-o", "ro", src}, Path: mnt}}, nil, "true\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Mounts) != 1 || strings.ContainsAny(strings.TrimPrefix(spec.Mounts[0], "container:"), ",") ||
		strings.Count(spec.Mounts[0], " ") != 4 {
		t.Fatalf("mount = %q, want a specification with no space or comma inside a word", spec.Mounts)
	}
	link := strings.Fields(spec.Mounts[0])[3]
	if got, err := os.Readlink(link); err != nil || got != src {
		t.Errorf("link %s points at %q (%v), want %s", link, got, err, src)
	}
	if !slices.Contains(spec.Binds, filepath.Dir(src)) {
		t.Errorf("binds = %v, want the directory of %s", spec.Binds, src)
	}

	if _, err := buildJob(t.TempDir(), []Mount{{Bin: "/opt/f", Args: []string{"-o", "a,b"}, Path: mnt}}, nil, ""); err == nil {
		t.Error("an option with a comma that is not a path was accepted")
	}
}

// A path that does not exist yet stands for its parent, and a path under one already bound is not bound again.
func TestJobBindsStandInForTheParentAndDropWhatIsCovered(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	binds, err := jobBinds([]string{filepath.Join(dir, "out", "new.sqf"), sub, dir})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(binds, dir) {
		t.Fatalf("binds = %v, want %s", binds, dir)
	}
	for _, b := range binds {
		if b == sub || strings.HasPrefix(b, dir+"/") {
			t.Errorf("binds = %v: %s is under %s, which is bound", binds, b, dir)
		}
	}
}

// A directory with a comma in its name cannot be bound, and the refusal says so.
func TestJobBindsRefuseAPathABindCannotCarry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a,b")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := jobBinds([]string{dir}); err == nil || !strings.Contains(err.Error(), "comma") {
		t.Errorf("err = %v, want a refusal that names the comma", err)
	}
}

// The job binds /etc/passwd and /etc/group as files, never /etc: the host's resolv.conf can be a link into /run, which the job's root lacks.
func TestJobBindsLeaveTheHostEtcOut(t *testing.T) {
	binds, err := jobBinds(nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(binds, "/etc") {
		t.Errorf("binds = %v, want /etc left out", binds)
	}
	for _, f := range systemFiles {
		if _, err := os.Stat(f); err == nil && !slices.Contains(binds, f) {
			t.Errorf("binds = %v, want %s bound as a file", binds, f)
		}
	}
}
