package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the condatainer dashboard server",
	Long: `Manage the condatainer dashboard server.

The server runs on the login node and provides:
  - A web dashboard at http://localhost:{port}/
  - A reverse proxy to helper apps on compute nodes

- The port is remembered after the first start.
- start, stop and restart cannot run inside a container or a scheduler job.
- The token is private and stays the same across restarts.`,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(serverStartCmd, serverStopCmd, serverStatusCmd, serverRestartCmd)
}

// requireLoginNode refuses the commands that start or stop the server inside a
// container or a scheduler job: the server belongs to the login node.
func requireLoginNode(cmd *cobra.Command) error {
	var where string
	switch {
	case config.IsInsideContainer():
		where = "a container"
	case scheduler.IsInsideJob():
		where = "a scheduler job"
	default:
		return nil
	}
	cmd.SilenceUsage = true
	return fmt.Errorf("server %s cannot run inside %s; run it on the login node", cmd.Name(), where)
}

// rotateServerToken replaces the dashboard token for --rotate-token.
// A running server follows the file within seconds, so it needs no restart.
func rotateServerToken() error {
	if !server.TokenRequired() {
		utils.PrintWarning("server.require_token is off, so there is no token to replace.")
		return nil
	}
	if _, err := server.RotateToken(); err != nil {
		return fmt.Errorf("replacing the token: %w", err)
	}
	utils.PrintMessage("Dashboard token replaced. Every browser signs in again.")
	return nil
}

// startServerDaemon forks the server using the pipe-readiness protocol.
//   - The child always runs in a new session (Setsid) so sshd exits cleanly on logout.
//   - In non-daemon mode the child watches the parent shell PID and exits when it dies. stdout/stderr are redirected to the server log file.
func startServerDaemon(port int, daemon bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	// Open log file before forking so errors are visible to the user.
	logPath := config.GetServerLogPath()
	var logFile *os.File
	if logPath != "" {
		if utils.EnsureWritableDir(config.GetUserStateDir()) {
			logFile, _ = os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, utils.PermFile)
		}
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("creating pipe: %w", err)
	}

	args := []string{"_server_daemon", "--report-fd", "3", "--port", strconv.Itoa(port)}
	if utils.DebugMode {
		args = append(args, "--debug")
	}
	// Non-daemon: watch the SSH session leader so the server exits when the user
	// logs out, regardless of how many process layers deep this call originates
	// (e.g. auto-started from "condatainer helper run" via EnsureServer).
	if !daemon {
		if sid := sshSessionLeaderPID(); sid > 1 {
			args = append(args, "--watch-pid", strconv.Itoa(sid))
		}
	}
	daemonCmd := exec.Command(exe, args...)
	// Always create a new session so sshd exits cleanly on logout.
	daemonCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	daemonCmd.ExtraFiles = []*os.File{pw}
	daemonCmd.Stdout = logFile
	daemonCmd.Stderr = logFile

	if err := daemonCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("starting server: %w", err)
	}
	pw.Close()
	if logFile != nil {
		logFile.Close()
	}
	daemonCmd.Process.Release() //nolint:errcheck

	msg, _ := io.ReadAll(pr)
	pr.Close()
	if len(msg) > 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(msg)))
	}

	utils.PrintSuccess("Server dashboard started:")
	utils.PrintLink(server.TokenLink(fmt.Sprintf("http://localhost:%d/", port)))
	if logPath != "" {
		utils.PrintMessage("Log: %s", logPath)
	}
	return nil
}

// sshSessionLeaderPID returns the PID of the current session leader by reading /proc/self/stat.
//   - This is the login shell regardless of process nesting depth, so the server correctly exits when the user logs out even when auto-started from a helper command.
//   - Falls back to os.Getppid() if /proc is unavailable.
func sshSessionLeaderPID() int {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return os.Getppid()
	}
	// Format: pid (comm) state ppid pgrp session ...
	// Skip past comm which may contain spaces by finding the last ')'.
	idx := bytes.LastIndexByte(data, ')')
	if idx < 0 {
		return os.Getppid()
	}
	fields := bytes.Fields(data[idx+1:])
	// After ')': state(0) ppid(1) pgrp(2) session(3)
	if len(fields) < 4 {
		return os.Getppid()
	}
	sid, err := strconv.Atoi(string(fields[3]))
	if err != nil || sid <= 1 {
		return os.Getppid()
	}
	return sid
}
