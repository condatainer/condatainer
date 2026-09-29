// Package producer coordinates commands that create or replace one image.
//
// Its lock is separate from image.AcquireLock: a producer lock prevents two
// builds or pulls from targeting the same pathname, while the inode lock
// prevents replacing an image that a running container is using.
package producer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// PreparedSuffix marks a producer's in-progress output.
const PreparedSuffix = ".part"

// currentJobID reads the running scheduler job. Injected for tests, which have
// no scheduler on PATH.
var currentJobID = scheduler.CurrentJobID

// ErrProducing reports that a live producer already holds a target's lock.
var ErrProducing = errors.New("another producer holds the target")

// ProducingError names the live producer. It carries Info because a caller that
// waits on a scheduler job has to report which job, not merely that one exists.
type ProducingError struct {
	Target string
	Path   string
	Info   Info
}

func (e *ProducingError) Error() string {
	who := e.Info.Runner
	if e.Info.JobID != "" {
		who += " job " + e.Info.JobID
	} else if e.Info.Node != "" {
		who += " on " + ShortName(e.Info.Node)
	}
	return fmt.Sprintf("%s is being produced by %s (lock: %s)", e.Target, who, e.Path)
}

func (e *ProducingError) Unwrap() error { return ErrProducing }

// Info is the JSON metadata stored in a producer lock file.
type Info struct {
	Runner    string `json:"runner"`
	JobID     string `json:"job_id"`
	Node      string `json:"node"`
	PID       int    `json:"pid"`
	CreatedAt string `json:"created_at"`
}

// Guard is a local producer lock held for one target. Release removes only the
// lock this guard created.
type Guard struct {
	path string
	info Info
	hold *utils.FileLock // held while this process produces; nil for an adopted job lock
}

// AcquireLocal claims target for the current process. A definitely stale lock
// and its owner-derived partial output are removed before one bounded retry.
func AcquireLocal(target string) (*Guard, error) {
	info := Info{
		Runner:    "local",
		Node:      Hostname(),
		PID:       os.Getpid(),
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	path := Path(target)
	for attempt := 0; attempt < 2; attempt++ {
		if hold, err := Claim(path, info); err == nil {
			return &Guard{path: path, info: info, hold: hold}, nil
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("cannot create producer lock %s: %w", path, err)
		}

		existing, err := Read(path)
		if os.IsNotExist(err) {
			continue // released in between
		}
		if err != nil {
			return nil, fmt.Errorf("cannot read producer lock %s: %w", path, err)
		}
		// Our own scheduler job's lock, created by whatever submitted us: adopt it
		// rather than reporting ourselves as another producer. A job ID names one
		// job, so a lock carrying ours is ours.
		if job := currentJobID(); job != "" && existing.JobID == job {
			return &Guard{path: path, info: existing}, nil
		}
		stale, _, checkErr := IsStale(path, existing)
		if !stale {
			if checkErr != nil {
				return nil, fmt.Errorf("producer lock found at %s: %w", path, checkErr)
			}
			return nil, &ProducingError{Target: target, Path: path, Info: existing}
		}
		removed, err := RemoveStale(path)
		if err != nil {
			return nil, fmt.Errorf("cannot remove stale producer lock %s: %w", path, err)
		}
		if !removed {
			return nil, &ProducingError{Target: target, Path: path, Info: existing}
		}
		os.Remove(PreparedPath(target, existing)) //nolint:errcheck
	}
	return nil, fmt.Errorf("another build or pull claimed %s", target)
}

// Info returns the metadata recorded by this guard.
func (g *Guard) Info() Info { return g.info }

// Release gives up the producer lock. The file goes before the hold, so no
// checker sees a free lock on a file this guard still owns.
func (g *Guard) Release() error {
	if g == nil || g.path == "" {
		return nil
	}
	path := g.path
	g.path = ""
	err := os.Remove(path)
	g.letGo()
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Handoff leaves the lock in place for a producer that outlives this process,
// such as a scheduler job, and lets go of the hold. A later Release does nothing.
func (g *Guard) Handoff() {
	if g == nil {
		return
	}
	g.letGo()
	g.path = ""
}

func (g *Guard) letGo() {
	if g.hold != nil {
		g.hold.Close() //nolint:errcheck
		g.hold = nil
	}
}

// Claim creates the lock at path, holds an exclusive fcntl lock on it and records info.
//   - The hold lasts until the returned lock is closed or the process dies, however it dies. That is what tells a live holder from a dead one.
//   - An existing lock returns an error matching os.IsExist.
//   - The file is locked before its content is written, and checked to still be the file at path. A checker cannot remove it in between.
func Claim(path string, info Info) (*utils.FileLock, error) {
	data, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal producer lock: %w", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, utils.PermFile)
		if err != nil {
			return nil, err
		}
		hold, err := utils.LockOpenFile(f, true)
		if err != nil {
			f.Close()
			if errors.Is(err, utils.ErrLockConflict) {
				continue // a checker holds it and is removing it
			}
			return nil, err
		}
		if !namesFile(path, f) {
			hold.Close() //nolint:errcheck
			continue
		}
		if _, err := f.Write(data); err != nil {
			os.Remove(path) //nolint:errcheck
			hold.Close()    //nolint:errcheck
			return nil, err
		}
		utils.ShareWithParentGroup(path)
		return hold, nil
	}
	return nil, fmt.Errorf("could not claim %s", path)
}

// RemoveStale removes the lock at path if nothing holds it, and reports whether it is gone.
//   - It takes an exclusive lock first and removes the file while holding it, so a new holder cannot claim it in between.
//   - A lock someone holds is left in place and reported as not removed.
func RemoveStale(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	hold, err := utils.LockOpenFile(f, true)
	if err != nil {
		f.Close()
		if errors.Is(err, utils.ErrLockConflict) {
			return false, nil
		}
		return false, err
	}
	defer hold.Close() //nolint:errcheck
	if !namesFile(path, f) {
		return false, nil // replaced by a new claim
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

// namesFile reports whether path still names the open file f.
func namesFile(path string, f *os.File) bool {
	at, err1 := os.Stat(path)
	open, err2 := f.Stat()
	return err1 == nil && err2 == nil && os.SameFile(at, open)
}

// isHeld reports whether a live process holds the lock at path.
func isHeld(path string) (bool, error) {
	probe, err := utils.AcquireFileLock(path, false)
	if errors.Is(err, utils.ErrLockConflict) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	probe.Close() //nolint:errcheck
	return false, nil
}

// Path returns the producer lock path for target.
func Path(target string) string { return target + ".lock" }

// PreparedPath returns a producer-private output path beside target. Because it
// is derived from Info, stale-lock cleanup can find the abandoned output.
func PreparedPath(target string, info Info) string {
	return target + "." + Tag(info) + PreparedSuffix
}

// Tag renders the filesystem-safe producer identity shared by prepared outputs
// and private build workspaces.
func Tag(info Info) string {
	owner := info.Runner
	if owner == "" {
		owner = "local"
	}
	tag := owner
	if info.JobID != "" {
		tag += "-" + info.JobID
	} else {
		tag += "-" + info.Node + "-" + strconv.Itoa(info.PID)
	}
	return sanitizeTag(tag)
}

func sanitizeTag(tag string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, tag)
}

// LocalInfo identifies the current process for provisional workspace paths.
// Build lock acquisition produces the same tag unless a scheduler lock is
// adopted, in which case the workspace is re-sited to the scheduler job tag.
func LocalInfo() Info {
	return Info{Runner: "local", Node: Hostname(), PID: os.Getpid()}
}

// Hostname returns this host's full name, which is what a lock records.
func Hostname() string {
	h, _ := os.Hostname()
	return h
}

// ShortName cuts host at its first dot, for display.
func ShortName(host string) string {
	if idx := strings.Index(host, "."); idx > 0 {
		return host[:idx]
	}
	return host
}

// Acquire atomically creates path and records info. Callers check os.IsExist to
// distinguish an active producer from other filesystem errors.
func Acquire(path string, info Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal producer lock: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, utils.PermFile)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	utils.ShareWithParentGroup(path)
	return nil
}

// Overwrite updates a lock already held by the caller.
func Overwrite(path string, info Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal producer lock: %w", err)
	}
	return os.WriteFile(path, data, utils.PermFile)
}

// Read parses a producer lock. An empty lock, left when Acquire was interrupted
// after creating the file, returns zero Info and is consequently stale.
func Read(path string) (Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	if len(data) == 0 {
		return Info{}, nil
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, fmt.Errorf("corrupt lock file: %w", err)
	}
	return info, nil
}

// IsStale reports whether the producer recorded in the lock at path is definitely gone.
//   - A scheduler job is asked of the scheduler. One that cannot be verified is treated as alive, with an explanatory error.
//   - Anything else, a local holder or a submission still waiting for its job ID, is alive exactly while its fcntl lock is held.
func IsStale(path string, info Info) (stale bool, status scheduler.JobStatus, err error) {
	if info.Runner != "" && info.Runner != "local" && info.JobID != "" {
		sched := scheduler.ActiveScheduler()
		if sched == nil {
			return false, scheduler.JobStatusUnknown, fmt.Errorf("scheduler unavailable, cannot verify job %s", info.JobID)
		}
		st, err := sched.GetJobStatus(context.Background(), info.JobID)
		if err != nil {
			return false, scheduler.JobStatusUnknown, fmt.Errorf("cannot check job %s: %w", info.JobID, err)
		}
		if st == scheduler.JobStatusUnknown {
			return false, st, fmt.Errorf("cannot determine status of job %s", info.JobID)
		}
		return !st.IsAlive(), st, nil
	}

	held, err := isHeld(path)
	if os.IsNotExist(err) {
		return true, scheduler.JobStatusUnknown, nil
	}
	if err != nil {
		return false, scheduler.JobStatusUnknown, fmt.Errorf("cannot check producer lock %s: %w", path, err)
	}
	if held {
		return false, scheduler.JobStatusRunning, nil
	}
	return true, scheduler.JobStatusUnknown, nil
}
