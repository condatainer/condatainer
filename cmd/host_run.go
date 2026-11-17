package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/spf13/cobra"
)

var (
	hostRunOn    string
	hostRunStdin bool
)

var hostRunCmd = &cobra.Command{
	Use:   "host-run [flags] <command> [args...]",
	Short: "Run a command on the host from inside a container",
	Long: `Run a command on the host from inside a container.

- The command runs on the host with your full user and group identity.
- It starts in the current directory, which must exist on the host.
- Its output and exit code come back.
- --on login is the node the job was submitted from.
- Without --stdin the command reads nothing.

It works in a container started from a launch listed in host_run.launch.
The command runs directly, not through a shell.
For a pipe on the host, use ` + "`host-run bash -c 'squeue | head'`" + `.`,
	Example: `  condatainer host-run squeue --me
  condatainer host-run --on login sbatch job.sh
  condatainer host-run --stdin sbatch < job.sh`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runHostRun,
}

func init() {
	rootCmd.AddCommand(hostRunCmd)
	hostRunCmd.Flags().StringVar(&hostRunOn, "on", "", "Node to run on: localhost, login or a node name (default: host_run.on)")
	hostRunCmd.Flags().BoolVar(&hostRunStdin, "stdin", false, "Send standard input, up to 1 MiB")
	hostRunCmd.Flags().SetInterspersed(false) // Stop flag parsing at the command; its own flags are its own
}

func runHostRun(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	call := hostrun.Call{
		Sock:   os.Getenv(hostrun.EnvSock),
		Argv:   args,
		Cwd:    cwd,
		On:     hostRunOn,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if hostRunStdin {
		call.Stdin = os.Stdin
	}
	code, err := call.Run(cmd.Context())
	if err != nil {
		return fmt.Errorf("host-run: %w", err)
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// hostRunCall sends a call to the host through host-run. Tests replace it.
var hostRunCall = func(ctx context.Context, call hostrun.Call) (int, error) { return call.Run(ctx) }

// runOnHost runs argv on the host through host-run, from the current directory, and exits with its exit code.
//   - Output comes back on the streams it was written to.
//   - flag names the option that asked for it, in the error.
func runOnHost(ctx context.Context, argv []string, on, flag string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	code, err := hostRunCall(ctx, hostrun.Call{
		Sock:   os.Getenv(hostrun.EnvSock),
		Argv:   argv,
		Cwd:    cwd,
		On:     on,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("%s from inside a container goes through host-run: %w", flag, err)
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}
