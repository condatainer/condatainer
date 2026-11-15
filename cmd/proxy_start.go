package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/runtime/proxy"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var proxyStartCmd = &cobra.Command{
	Use:          "start",
	Args:         cobra.NoArgs,
	Short:        "Start the proxy",
	Long:         `Start a SOCKS5 proxy so compute-node jobs reach the internet via the login node.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.IsInsideContainer() {
			return fmt.Errorf("proxy must be started outside a container")
		}
		if scheduler.ActiveScheduler() == nil {
			return fmt.Errorf("no scheduler detected — proxy is only needed on HPC clusters")
		}

		currentHost, _ := os.Hostname()
		isInsideJob := scheduler.ActiveScheduler().IsInsideJob()

		if !isInsideJob {
			// ── Login node ──────────────────────────────────────────────────
			targetHost := proxyStartHost
			if targetHost == "" {
				targetHost = currentHost
			}

			// Delegate to another login node if --host points elsewhere
			if targetHost != currentHost {
				utils.PrintMessage("Delegating proxy start to %s…", targetHost)
				return proxy.StartOnNode(targetHost, proxyStartVia, proxyStartPort)
			}

			// Start shared proxy on this node
			// Check if already running
			if ps, err := proxy.ReadPidFile(); err == nil {
				if proxy.ProxyAlive(ps.Host, ps.Port) {
					utils.PrintMessage("Proxy already running on %s:%d (PID %d)", ps.Host, ps.Port, ps.PID)
					return nil
				}
				proxy.RemovePidFile()
			}

			sshDest := proxyStartVia
			if sshDest == "" {
				sshDest = currentHost
			}

			port := proxyStartPort
			if port == 0 {
				var err error
				port, err = proxy.FreePort()
				if err != nil {
					return fmt.Errorf("failed to find free port: %w", err)
				}
			}

			return startProxyDaemon(sshDest, port, false)

		} else {
			// ── Compute node (inside job) ────────────────────────────────────
			// Check if already running (local or shared)
			if url, ok := proxy.FindActiveProxy(); ok {
				utils.PrintMessage("Proxy already active: %s", url)
				return nil
			}

			viaHost, err := proxy.ResolveViaHost(proxyStartVia)
			if err != nil {
				return err
			}

			port := proxyStartPort
			if port == 0 {
				port, err = proxy.FreePort()
				if err != nil {
					return fmt.Errorf("failed to find free port: %w", err)
				}
			}

			return startProxyDaemon(viaHost, port, true)
		}
	},
}

// proxy start flags
var (
	proxyStartHost string
	proxyStartVia  string
	proxyStartPort int
)

func init() {
	proxyStartCmd.Flags().StringVar(&proxyStartHost, "host", "", "Run the proxy on this login node (over SSH if it is not the current one)")
	proxyStartCmd.Flags().StringVar(&proxyStartVia, "via", "", "SSH server to tunnel through (default: this node; in a job, its login node)")
	proxyStartCmd.Flags().IntVarP(&proxyStartPort, "port", "p", 0, "Port to listen on (default: a free port)")
}

// startProxyDaemon forks the proxy daemon and waits for it to signal readiness.
// The daemon reports back via a pipe: EOF = ready, non-empty message = error.
// localOnly=false: shared mode (0.0.0.0, NFS PID file).
// localOnly=true:  per-job mode (127.0.0.1, node-local PID file).
func startProxyDaemon(sshDest string, port int, localOnly bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}

	// Report pipe: daemon closes write end (EOF) on success or writes error message.
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("report pipe: %w", err)
	}

	args := []string{"_proxy_daemon", "--report-fd", "3"}
	if localOnly {
		args = append(args, "--local")
	}
	if utils.DebugMode {
		args = append(args, "--debug")
	}
	args = append(args, sshDest, strconv.Itoa(port))

	daemonCmd := exec.Command(exe, args...)
	daemonCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	daemonCmd.ExtraFiles = []*os.File{pw} // ExtraFiles[0] → fd 3 in daemon
	if utils.DebugMode {
		daemonCmd.Stderr = os.Stderr
	}

	if err := daemonCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return fmt.Errorf("failed to start proxy daemon: %w", err)
	}
	pw.Close()                  // parent closes its copy of the write end
	daemonCmd.Process.Release() //nolint:errcheck

	// Block until daemon signals ready (EOF) or reports an error.
	msg, _ := io.ReadAll(pr)
	pr.Close()
	if len(msg) > 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(msg)))
	}

	if localOnly {
		utils.PrintSuccess("Per-job proxy started on 127.0.0.1:%d (via %s)", port, sshDest)
		utils.PrintMessage("This job will use http://127.0.0.1:%d", port)
		return nil
	}

	currentHost, _ := os.Hostname()
	utils.PrintSuccess("Proxy started on %s:%d", currentHost, port)
	utils.PrintMessage("Compute node jobs will use http://%s:%d", currentHost, port)

	// Try to enable linger so the proxy survives logout.
	if survives, _, _ := proxy.WillSurviveLogout(); !survives {
		user := os.Getenv("USER")
		lingerCmd := exec.Command("loginctl", "enable-linger", user)
		lingerCmd.Stderr = nil
		if err := lingerCmd.Run(); err == nil {
			utils.PrintMessage("Enabled linger for %s — proxy will survive logout.", user)
		} else {
			utils.PrintWarning("Proxy may be killed on full logout (KillUserProcesses=yes, Linger=no, enable-linger failed).")
			utils.PrintMessage("Proxy will survive SSH disconnects but not complete logout.")
			utils.PrintMessage("Ask your sysadmin to run: loginctl enable-linger %s", user)
		}
	}
	return nil
}
