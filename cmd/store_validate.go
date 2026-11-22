package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/spf13/cobra"
)

func newStoreValidateCmd() *cobra.Command {
	var payload bool
	cmd := &cobra.Command{
		Use:   "validate [flags] [name]",
		Short: "Check that every entry matches its identity",
		Long: `Check that every entry still matches the identity it is filed under.

- Give a name to check only its builds.
- It prints one line per entry, and fails if any entry does not match.
- --payload needs squashfuse and apptainer.
- Conda and definition builds have no payload key, so --payload skips them.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			report := store.Scan(store.ScanOptions{Name: name, Stored: true, Uncached: true})
			store.SortReport(&report)
			for _, candidate := range report.Candidates {
				if payload {
					if err := freeze.VerifyPayload(cmd.Context(), candidate.Path); err != nil && !errors.Is(err, freeze.ErrNoPayloadKey) {
						report.Issues = append(report.Issues, store.Issue{Path: candidate.Path, Error: err.Error()})
						continue
					}
				}
				fmt.Fprintf(os.Stdout, "ok\t%s\t%s\n", store.FormatKeyRef(candidate.Identity), candidate.Path)
			}
			for _, issue := range report.Issues {
				fmt.Fprintf(os.Stderr, "invalid\t%s\t%s\n", issue.Path, issue.Error)
			}
			if len(report.Issues) != 0 {
				return fmt.Errorf("store validation failed for %d entries", len(report.Issues))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&payload, "payload", false, "Also check each entry's file contents (reads the whole store)")
	return cmd
}
