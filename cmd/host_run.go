package cmd

import (
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
- --on picks the node: localhost, login (the node the job was submitted from) or a node name.
- --stdin sends standard input, up to 1 MiB. Without it the command reads nothing.

It works in a container started from a launch listed in host_run.launch.
The command is run directly, not through a shell. For a pipe, use host-run bash -c '...'.`,
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
