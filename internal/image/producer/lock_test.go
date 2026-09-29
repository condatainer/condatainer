package producer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// asJob makes the process look like the given scheduler job for one test.
func asJob(t *testing.T, id string) {
	t.Helper()
	previous := currentJobID
	currentJobID = func() string { return id }
	t.Cleanup(func() { currentJobID = previous })
}

func TestAcquireLocalSerializesTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo.sqf")
	first, err := AcquireLocal(target)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release() //nolint:errcheck

	_, err = AcquireLocal(target)
	var producing *ProducingError
	if !errors.As(err, &producing) {
		t.Fatalf("second AcquireLocal error = %v, want *ProducingError", err)
	}
	if producing.Info.PID != os.Getpid() {
		t.Fatalf("ProducingError names PID %d, want %d", producing.Info.PID, os.Getpid())
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLocal(target)
	if err != nil {
		t.Fatalf("AcquireLocal after release: %v", err)
	}
	second.Release() //nolint:errcheck
}

func TestAcquireLocalClearsStaleLockAndPartial(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo.sqf")
	stale := Info{}
	if err := Acquire(Path(target), stale); err != nil {
		t.Fatal(err)
	}
	partial := PreparedPath(target, stale)
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	guard, err := AcquireLocal(target)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release() //nolint:errcheck
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("stale partial remains: %v", err)
	}
}

func TestTagDistinguishesLocalAndSchedulerOwners(t *testing.T) {
	local := Tag(Info{Runner: "local", Node: "node-a", PID: 42})
	job := Tag(Info{Runner: "slurm", JobID: "42"})
	if local == job || local != "local-node-a-42" || job != "slurm-42" {
		t.Fatalf("tags = %q and %q", local, job)
	}
}

// A submitted job finds a lock its own submitter created and must adopt it, not
// report itself as another producer. A job ID names one job, so a lock carrying
// ours is ours.
func TestAcquireLocalAdoptsItsOwnJobLock(t *testing.T) {
	asJob(t, "12345")
	target := filepath.Join(t.TempDir(), "demo.sqf")
	submitted := Info{Runner: "slurm", JobID: "12345", Node: "login01", CreatedAt: "2026-01-01T00:00:00Z"}
	if err := Acquire(Path(target), submitted); err != nil {
		t.Fatal(err)
	}

	guard, err := AcquireLocal(target)
	if err != nil {
		t.Fatalf("a job did not adopt its own lock: %v", err)
	}
	if guard.Info().JobID != "12345" {
		t.Errorf("adopted info = %#v, want the submitter's record", guard.Info())
	}
	// Releasing hands the lock back, which is what lets the next restore see the
	// artifact as no longer in flight.
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(target)); !os.IsNotExist(err) {
		t.Errorf("the adopted lock survived release: %v", err)
	}
}

// Another job's live lock is still a conflict: only a matching job ID is ours.
func TestAcquireLocalRefusesAnotherJobsLock(t *testing.T) {
	asJob(t, "12345")
	target := filepath.Join(t.TempDir(), "demo.sqf")
	other := Info{Runner: "slurm", JobID: "99999", Node: "login01", CreatedAt: "2026-01-01T00:00:00Z"}
	if err := Acquire(Path(target), other); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLocal(target); err == nil {
		t.Fatal("another job's lock was adopted")
	}
}

// A lock is alive exactly while its holder holds it: the file left behind by a
// holder that died, on any node, is stale.
func TestLockIsAliveOnlyWhileHeld(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo.sqf")
	guard, err := AcquireLocal(target)
	if err != nil {
		t.Fatal(err)
	}
	if stale, _, err := IsStale(Path(target), guard.Info()); stale || err != nil {
		t.Fatalf("a held lock: stale=%v err=%v, want alive", stale, err)
	}
	guard.Release() //nolint:errcheck

	leftover := Info{Runner: "local", Node: "another-node", PID: 1}
	if err := Acquire(Path(target), leftover); err != nil {
		t.Fatal(err)
	}
	if stale, _, err := IsStale(Path(target), leftover); !stale || err != nil {
		t.Fatalf("a lock nobody holds: stale=%v err=%v, want stale", stale, err)
	}
	next, err := AcquireLocal(target)
	if err != nil {
		t.Fatalf("a lock nobody holds was not cleared: %v", err)
	}
	next.Release() //nolint:errcheck
}

// A handoff lets go of the hold but leaves the file for the job that takes it over.
func TestHandoffKeepsTheFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo.sqf")
	guard, err := AcquireLocal(target)
	if err != nil {
		t.Fatal(err)
	}
	guard.Handoff()
	guard.Release() //nolint:errcheck
	if _, err := os.Stat(Path(target)); err != nil {
		t.Fatalf("a handed-off lock was removed: %v", err)
	}
	if held, err := isHeld(Path(target)); held || err != nil {
		t.Errorf("a handed-off lock is still held: %v, %v", held, err)
	}
}

// A submission still waiting for its job ID is alive while the submitter holds it.
func TestPendingSubmissionIsAliveWhileHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.sqf.lock")
	pending := Info{Runner: "slurm", Node: Hostname(), PID: os.Getpid()}
	hold, err := Claim(path, pending)
	if err != nil {
		t.Fatal(err)
	}
	if stale, _, _ := IsStale(path, pending); stale {
		t.Error("a submission in progress was judged stale")
	}
	if removed, err := RemoveStale(path); removed || err != nil {
		t.Errorf("RemoveStale took a held lock: %v, %v", removed, err)
	}
	hold.Close() //nolint:errcheck
	if stale, _, _ := IsStale(path, pending); !stale {
		t.Error("a submission whose submitter let go without a job ID was judged alive")
	}
}
