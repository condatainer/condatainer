package freeze

import (
	"context"
	"fmt"
	"os"

	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
)

// executablePath resolves condatainer's own binary path, for MountedRun to
// re-exec into the job's command. Overridden in tests: os.Executable() inside `go
// test` returns the test binary, which has no _mount_job command.
var executablePath = os.Executable

// Mount is a FUSE server that MountedRun starts, mounted at Path before the work runs.
type Mount struct {
	Bin  string   // the FUSE server
	Args []string // its options and its source, one element each, ahead of Path
	Path string   // the mountpoint, an existing directory
}

// MountedRun mounts every mount and runs work with them in place, then ends the mounts.
//   - work is a bash fragment that sees each Path as a directory. It reads the mounts and starts no FUSE server itself.
//   - binds are the host paths the mounts and work name, besides the mount paths' own directories. A path that does not exist yet stands for its parent.
//   - The mounts are made in order, so a later one can name an earlier Path.
//   - Exported so internal/build can read a .sif the same way.
func MountedRun(ctx context.Context, mounts []Mount, binds []string, work string, io execpkg.IO) error {
	if len(mounts) == 0 {
		return fmt.Errorf("nothing to mount")
	}
	return runJob(ctx, mounts, binds, work, io)
}
