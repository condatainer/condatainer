// Command mountharness is a test-only stand-in for the condatainer binary,
// used by internal/image/freeze's mount tests. MountedRun re-execs
// os.Executable() into the hidden _mount_job command; the `go test`
// binary can't play that role since it has no cmd/cobra dispatch at all, so
// tests point MountedRun at this binary instead.
//
// Usage:
//
//	mountharness _mount_job <spec>                               see freeze.RunJob
//	mountharness _payload_key <dir> <base> <workers>             prints the payload key of dir, as the hidden command does
//	mountharness drive <fuseBin> <sqf> <mnt> <work>              calls freeze.MountedRun directly and blocks until it returns
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/image/freeze"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: mountharness _mount_job <spec> | _payload_key <dir> <base> <workers> | drive <fuseBin> <sqf> <mnt> <work>")
	}

	switch os.Args[1] {
	case "_mount_job":
		if len(os.Args) != 3 {
			fail("usage: mountharness _mount_job <spec>")
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		if err := freeze.RunJob(ctx, os.Args[2]); err != nil {
			if code, ok := freeze.JobExitCode(err); ok {
				os.Exit(code)
			}
			fail("%v", err)
		}
	case "_payload_key":
		if len(os.Args) != 5 {
			fail("usage: mountharness _payload_key <dir> <base> <workers>")
		}
		workers, err := strconv.Atoi(os.Args[4])
		if err != nil {
			fail("workers: %v", err)
		}
		records, err := key.TreeOf(context.Background(), os.Args[2], os.Args[3], workers)
		if err != nil {
			fail("%v", err)
		}
		ref, err := key.PayloadKey(records)
		if err != nil {
			fail("%v", err)
		}
		fmt.Println(ref.SHA256)
	case "drive":
		if len(os.Args) != 6 {
			fail("usage: mountharness drive <fuseBin> <sqf> <mnt> <work>")
		}
		io := execpkg.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
		mounts := []freeze.Mount{{Bin: os.Args[2], Args: []string{os.Args[3]}, Path: os.Args[4]}}
		if err := freeze.MountedRun(context.Background(), mounts, []string{os.Args[3]}, os.Args[5], io); err != nil {
			fail("%v", err)
		}
	default:
		fail("unknown mode: %s", os.Args[1])
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
