package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/image/producer"
	"github.com/spf13/cobra"
)

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
