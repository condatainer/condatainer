package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestServerControlRefusedInsideContainer(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "1")
	for _, c := range []*cobra.Command{serverStartCmd, serverStopCmd, serverRestartCmd} {
		err := requireLoginNode(c)
		if err == nil || !strings.Contains(err.Error(), "cannot run inside a container") {
			t.Errorf("server %s inside a container: err = %v", c.Name(), err)
		}
	}
}
