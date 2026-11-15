package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
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

var (
	errPortTimeout  = errors.New("port did not accept connections in time")
	errServerExited = errors.New("service exited before accepting connections")
)

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
