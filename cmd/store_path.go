package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/store"
	"github.com/spf13/cobra"
)

func newStorePathCmd() *cobra.Command {
	var identity string
	cmd := &cobra.Command{
		Use:   "path [flags] <name>@<identity>",
		Short: "Print the file path of one exact build",
		Long: `Print the file path of one exact build, for use in a script.

- It fails unless the name and identity select exactly one overlay.`,
		Example: `  condatainer store path star/2.7.11b@9f2c1ab
  condatainer store path star/2.7.11b --identity 9f2c1ab`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name, query, err := splitStoreAddress(args[0], identity)
			if err != nil {
				return err
			}
			candidate, _, err := store.ResolveIdentity(name, query, nil)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, candidate.Path)
			return nil
		},
	}
	cmd.Flags().StringVar(&identity, "identity", "", "Complete identity or unambiguous digest prefix")
	return cmd
}
