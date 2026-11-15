package cmd

import (
	"fmt"
	"net"

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
