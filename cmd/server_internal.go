package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/image/producer"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/spf13/cobra"
)

// helperJobCmd groups the commands a helper job calls. It stays out of the top-level help.
var helperJobCmd = &cobra.Command{
	Use:    "_helper",
	Hidden: true,
	Short:  "Commands a helper job calls to report to CondaTainer",
}

func init() {
	// A helper job script written by one build can run against a newer binary, so these
	// commands ignore flags they do not know.
	for _, c := range []*cobra.Command{serverReadyCmd, serverMessageCmd, serverDoneCmd, serverStartedCmd, pickPortCmd, helperHoldCmd, helperStateDirCmd} {
		c.FParseErrWhitelist.UnknownFlags = true
		helperJobCmd.AddCommand(c)
	}
	rootCmd.AddCommand(helperJobCmd)
	rootCmd.AddCommand(serverDaemonCmd)
}

// ── ready ──────────────────────────────────────────────────────────

var (
	serverReadyID          string
	serverReadyPort        int
	serverReadyNode        string
	serverReadyURLPath     string
	serverReadyExternalURL string
	serverReadyJobID       string
	serverReadyWait        int
	serverReadyPID         int
)

var serverReadyCmd = &cobra.Command{
	Use:   "ready",
	Short: "Signal that a helper service is ready (called from compute node)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverReadyID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}

		node := serverReadyNode
		if node == "" {
			node, _ = os.Hostname()
		}

		walltimeSec, _ := strconv.ParseInt(os.Getenv("CNT_HELPER_WALLTIME_SECS"), 10, 64)
		jobID := serverReadyJobID
		if jobID == "" {
			jobID = os.Getenv("CNT_HELPER_JOB_ID")
		}

		if serverReadyPort > 0 && serverReadyExternalURL == "" && serverReadyWait > 0 {
			timeout := time.Duration(serverReadyWait) * time.Second
			err := waitForPort(cmd.Context(), serverReadyPort, timeout, serverReadyPID)
			switch {
			case errors.Is(err, errServerExited):
				text := fmt.Sprintf("service exited before listening on port %d", serverReadyPort)
				_ = helper.AppendEvent(id, helper.StateEvent{Type: "msg", Level: "error", Text: text})
				return err
			case err != nil:
				fmt.Fprintf(os.Stderr, "warning: port %d is not accepting connections after %s; reporting ready anyway\n",
					serverReadyPort, timeout)
			}
		}

		ev := helper.StateEvent{
			Type:        "ready",
			Port:        serverReadyPort,
			Node:        node,
			WalltimeSec: walltimeSec,
			JobID:       jobID,
			URLPath:     serverReadyURLPath,
			ExternalURL: serverReadyExternalURL,
		}
		if err := helper.AppendEvent(id, ev); err != nil {
			return fmt.Errorf("writing ready event: %w", err)
		}
		return nil
	},
}

func init() {
	serverReadyCmd.Flags().StringVar(&serverReadyID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverReadyCmd.Flags().IntVar(&serverReadyPort, "port", 0, "Service port (0 = no tunnel)")
	serverReadyCmd.Flags().StringVar(&serverReadyNode, "node", "", "Compute node hostname (defaults to $HOSTNAME)")
	serverReadyCmd.Flags().StringVar(&serverReadyURLPath, "url-path", "", "URL path/query appended to proxy URL")
	serverReadyCmd.Flags().StringVar(&serverReadyExternalURL, "external-url", "", "External URL shown as-is")
	serverReadyCmd.Flags().StringVar(&serverReadyJobID, "job-id", "", "Scheduler job ID")
	serverReadyCmd.Flags().IntVar(&serverReadyWait, "wait", 60, "Seconds to wait for the port to accept connections before reporting ready (0 = don't wait)")
	serverReadyCmd.Flags().IntVar(&serverReadyPID, "pid", 0, "Service process ID; fail instead of reporting ready if it exits before the port opens")
}

var (
	errPortTimeout  = errors.New("port did not accept connections in time")
	errServerExited = errors.New("service exited before accepting connections")
)

// waitForPort polls 127.0.0.1:port until it accepts a connection.
//   - It returns errServerExited as soon as pid (when > 0) no longer exists.
//   - It returns errPortTimeout when timeout passes, or the context error when ctx ends.
func waitForPort(ctx context.Context, port int, timeout time.Duration, pid int) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			conn.Close() //nolint:errcheck
			return nil
		}
		if pid > 0 && !processExists(pid) {
			return errServerExited
		}
		if time.Now().After(deadline) {
			return errPortTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// processExists reports whether pid is a live process on this host.
func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ── message ────────────────────────────────────────────────────────

var (
	serverMessageID    string
	serverMessageLevel string
)

var serverMessageCmd = &cobra.Command{
	Use:   "message <text>",
	Short: "Append a message to the helper messages file (called from compute node)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverMessageID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}
		level := serverMessageLevel
		if level == "" {
			level = "info"
		}
		return helper.AppendEvent(id, helper.StateEvent{Type: "msg", Level: level, Text: args[0]})
	},
}

func init() {
	serverMessageCmd.Flags().StringVar(&serverMessageID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverMessageCmd.Flags().StringVar(&serverMessageLevel, "level", "info", "Message level: info|warn|error")
}

// ── started ────────────────────────────────────────────────────────

var (
	serverStartedID   string
	serverStartedNode string
)

var serverStartedCmd = &cobra.Command{
	Use:    "started",
	Hidden: true,
	Short:  "Record the node a helper job started on (called from the wrapper script)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverStartedID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}
		node := serverStartedNode
		if node == "" {
			node, _ = os.Hostname()
		}
		return helper.AppendEvent(id, helper.StateEvent{
			Type:  "start",
			Node:  node,
			JobID: os.Getenv("CNT_HELPER_JOB_ID"),
		})
	},
}

func init() {
	serverStartedCmd.Flags().StringVar(&serverStartedID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverStartedCmd.Flags().StringVar(&serverStartedNode, "node", "", "Node name (defaults to the host name)")
}

// ── done ───────────────────────────────────────────────────────────

var (
	serverDoneID       string
	serverDoneExitCode int
)

var serverDoneCmd = &cobra.Command{
	Use:    "done",
	Hidden: true,
	Short:  "Signal that a helper job has finished (called from the wrapper script)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverDoneID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}
		return helper.AppendEvent(id, helper.StateEvent{
			Type:     "done",
			ExitCode: &serverDoneExitCode,
		})
	},
}

func init() {
	serverDoneCmd.Flags().StringVar(&serverDoneID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverDoneCmd.Flags().IntVar(&serverDoneExitCode, "exit-code", 0, "Process exit code")
}

// ── pick-port ─────────────────────────────────────────────────────────────

var pickPortCmd = &cobra.Command{
	Use:   "pick-port",
	Short: "Print a free TCP port on this host",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		port, err := pickFreePort()
		if err != nil {
			return err
		}
		fmt.Println(port)
		return nil
	},
}

func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("finding free port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port, nil
}

// ── hold ───────────────────────────────────────────────────────────

var helperHoldCmd = &cobra.Command{
	Use:    "hold <lock-file> <wrapper-pid>",
	Hidden: true,
	Short:  "Hold a headless helper's lock file until its wrapper exits (called from the wrapper)",
	Args:   cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		pid, err := strconv.Atoi(args[1])
		if err != nil || pid <= 0 {
			return fmt.Errorf("invalid wrapper pid %q", args[1])
		}
		// The wrapper's group TERM must not release the lock before it has
		// recorded done; the holder ends only when the wrapper does.
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		lock, err := helper.HoldLock(args[0], producer.Info{
			Runner:    "local",
			Node:      producer.Hostname(),
			PID:       pid,
			CreatedAt: time.Now().Format(time.RFC3339),
		})
		if err != nil {
			return err
		}
		defer lock.Close()
		for os.Getppid() == pid {
			time.Sleep(time.Second)
		}
		return nil
	},
}

// ── state-dir ──────────────────────────────────────────────────────

var helperStateDirCmd = &cobra.Command{
	Use:    "state-dir <id>",
	Hidden: true,
	Short:  "Print the NFS state directory for a helper run ID",
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		dir := helper.StateDir(args[0])
		if dir == "" {
			return fmt.Errorf("cannot determine state directory")
		}
		fmt.Println(dir)
		return nil
	},
}

// ── _server_daemon ─────────────────────────────────────────────────────────

var (
	serverDaemonPort     int
	serverDaemonReportFd int
	serverDaemonWatchPID int
)

var serverDaemonCmd = &cobra.Command{
	Use:    "_server_daemon",
	Hidden: true,
	Short:  "Run the condatainer server daemon (invoked by 'server start')",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		port := serverDaemonPort
		if port == 0 {
			var err error
			port, err = pickFreePort()
			if err != nil {
				return err
			}
		}

		var reportPipe *os.File
		if serverDaemonReportFd > 0 {
			reportPipe = os.NewFile(uintptr(serverDaemonReportFd), "report")
		}

		return server.RunDaemon(port, reportPipe, serverDaemonWatchPID)
	},
}

func init() {
	serverDaemonCmd.Flags().IntVar(&serverDaemonPort, "port", 0, "Port to listen on (0 = use saved or free port)")
	serverDaemonCmd.Flags().IntVar(&serverDaemonReportFd, "report-fd", 0, "File descriptor to signal readiness to parent")
	serverDaemonCmd.Flags().IntVar(&serverDaemonWatchPID, "watch-pid", 0, "Exit when this PID dies (SSH session tracking)")
}
