package publish

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

const gcRepository = "ghcr.io/lab/proj/cnt"

func gcDigest(c string) string { return "sha256:" + strings.Repeat(c, 64) }

func gcLock(t *testing.T, pins map[string]string) *lock.Lock {
	t.Helper()
	l := lock.New()
	for artifact, digest := range pins {
		if err := l.AddRemote("provenance/"+artifact, lock.Remote{Repository: gcRepository, ManifestDigest: digest}); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestPinnedCountsOnlyThisRepository(t *testing.T) {
	l := gcLock(t, map[string]string{"a--1@aaaaaaaaaaaa": gcDigest("a")})
	if err := l.AddRemote("provenance/b--1@bbbbbbbbbbbb", lock.Remote{Repository: "ghcr.io/lab/other", ManifestDigest: gcDigest("b")}); err != nil {
		t.Fatal(err)
	}
	got := Pinned(l, "oci://"+gcRepository)
	if len(got) != 1 || !got[gcDigest("a")] {
		t.Errorf("pinned = %v, want only the digest in this repository", got)
	}
}

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitLock(t *testing.T, repo, root string, l *lock.Lock, message string) {
	t.Helper()
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lock.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock.FilePath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", message)
}

// Every revision of the lock reachable from HEAD adds what it pinned, even from a project below the repository root.
func TestHistoricPinnedReadsEveryRevisionOfTheLock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	root := filepath.Join(repo, "analysis")
	runGit(t, repo, "init", "-q")
	commitLock(t, repo, root, gcLock(t, map[string]string{"a--1@aaaaaaaaaaaa": gcDigest("a")}), "first")
	commitLock(t, repo, root, gcLock(t, map[string]string{"a--1@aaaaaaaaaaaa": gcDigest("a"), "b--1@bbbbbbbbbbbb": gcDigest("b")}), "second")
	commitLock(t, repo, root, gcLock(t, map[string]string{"b--1@bbbbbbbbbbbb": gcDigest("b")}), "third")
	if err := os.Remove(lock.FilePath(root)); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "drop the lock")

	pinned := Pinned(gcLock(t, map[string]string{"c--1@cccccccccccc": gcDigest("c")}), gcRepository)
	if err := HistoricPinned(t.Context(), root, gcRepository, pinned); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"a", "b", "c"} {
		if !pinned[gcDigest(c)] {
			t.Errorf("digest %s is not kept; pinned = %v", c, pinned)
		}
	}
}

func TestHistoricPinnedRefusesAShallowCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	origin := t.TempDir()
	runGit(t, origin, "init", "-q")
	commitLock(t, origin, origin, gcLock(t, map[string]string{"a--1@aaaaaaaaaaaa": gcDigest("a")}), "first")
	commitLock(t, origin, origin, gcLock(t, map[string]string{"b--1@bbbbbbbbbbbb": gcDigest("b")}), "second")
	clone := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	err := HistoricPinned(t.Context(), clone, gcRepository, map[string]bool{})
	if !errors.Is(err, ErrShallowHistory) {
		t.Errorf("error = %v, want ErrShallowHistory", err)
	}
}
