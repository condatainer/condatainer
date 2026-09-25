package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Inspect overlays kept under an exact build identity",
	Long: `Inspect and manage overlays kept under an exact build identity.

- The store holds builds that could not take a plain name because another build of it was installed.
- An entry is addressed as <name>@<identity>, so several builds of a name can coexist.
- Overlays under a plain name are handled by 'condatainer list' and 'condatainer remove'.`,
}

func init() {
	rootCmd.AddCommand(storeCmd)
	storeCmd.AddCommand(newStoreAddCmd(), newStoreListCmd(), newStorePathCmd(), newStoreValidateCmd(),
		newStoreUseCmd(), newStoreRemoveCmd(), newStoreGCCmd())
}

func newStoreListCmd() *cobra.Command {
	var equivalence string
	var jsonOutput bool
	var all bool
	var detail bool
	cmd := &cobra.Command{
		Use:   "list [flags] [name]",
		Short: "List store entries, or the builds that can stand in for one",
		Long: `List store entries by address, grouped by images directory.

- Give a name to list only its builds.
- Each address can be pasted into the other store commands.
- An identity in grey belongs to the build installed under the plain name.
- --detail adds the layout, size and relative file path.
- --json adds the complete keys, absolute paths and exact sizes.
- --all includes the overlay installed under the plain name.
- --equiv lists the installed builds that can stand in for an equivalence key.`,
		Example: `  condatainer store list
  condatainer store list star/2.7.11b --all --detail
  condatainer store list star/2.7.11b --equiv 9f2c1ab`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			var report store.Report
			if equivalence != "" {
				if name == "" {
					return errors.New("store list --equiv requires a name")
				}
				query, err := store.ParseIdentityQuery(equivalence)
				if err != nil {
					return err
				}
				report, err = store.Equivalent(name, query, nil)
				if err != nil {
					return err
				}
			} else {
				// Flat is opt-in: `store list` lists the store. But an overlay under
				// the plain name is the one that currently answers to it, so any
				// question about which build a name means needs --all to be
				// answerable at all.
				report = store.Scan(store.ScanOptions{Name: name, Stored: true, Flat: all})
			}
			store.SortReport(&report)
			return printStoreReport(report, jsonOutput, detail)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Also list the overlay installed under the plain name")
	cmd.Flags().BoolVar(&detail, "detail", false, "Show layout, size and path for each entry")
	cmd.Flags().StringVar(&equivalence, "equiv", "", "List the builds matching an equivalence key or prefix")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newStorePathCmd() *cobra.Command {
	var identity string
	cmd := &cobra.Command{
		Use:   "path [flags] <name>@<identity>",
		Short: "Print the file path of one exact build",
		Long: `Print the file path of one exact build, for use in a script.

- It fails unless the name and identity select exactly one artifact.`,
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

func newStoreValidateCmd() *cobra.Command {
	var payload bool
	cmd := &cobra.Command{
		Use:   "validate [flags] [name]",
		Short: "Check that every entry still matches the identity it is filed under",
		Long: `Check that every entry still matches the identity it is filed under.

- Give a name to check only its builds.
- It prints one line per entry, and fails if any entry does not match.
- --payload also checks each entry's file contents. This reads the whole store.
- --payload needs squashfuse and unshare.
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

// printStoreReport lists entries grouped by images directory, as `list` does.
//   - By default the address is the whole row, in columns: other store commands accept it. --detail adds layout, size and the filename relative to its directory heading.
//   - The full key, absolute paths and exact byte counts are in --json.
func printStoreReport(report store.Report, jsonOutput, detail bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	byRoot := map[string][]store.Candidate{}
	var roots []string
	for _, candidate := range report.Candidates {
		if _, seen := byRoot[candidate.Root]; !seen {
			roots = append(roots, candidate.Root)
		}
		byRoot[candidate.Root] = append(byRoot[candidate.Root], candidate)
	}

	for i, root := range roots {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(dirHeader(root))
		if !detail {
			plain := make([]string, len(byRoot[root]))
			styled := make([]string, len(byRoot[root]))
			for j, candidate := range byRoot[root] {
				plain[j] = storeAddress(candidate)
				styled[j] = styleStoreAddress(candidate)
			}
			printColumns(plain, styled, 0, terminalWidth())
			continue
		}
		for _, candidate := range byRoot[root] {
			fmt.Fprintf(os.Stdout, "%-34s %-6s %9s  %s\n", storeAddress(candidate),
				candidate.Layout, utils.FormatSize(candidate.Size),
				relativeTo(root, candidate.Path))
		}
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(os.Stderr, "%-34s %s: %s\n", "invalid", issue.Path, issue.Error)
	}
	return nil
}

// styleStoreAddress greys the identity of the build installed under the plain
// name: that one answers to its name, so its identity is informational.
func styleStoreAddress(candidate store.Candidate) string {
	suffix := "@" + shortDigest(candidate.Identity)
	if candidate.Layout == store.LayoutFlat {
		return candidate.Name + utils.StyleDim(suffix)
	}
	return candidate.Name + suffix
}

// relativeTo renders a path against the directory heading it, falling back to
// the absolute path when it lies elsewhere.
func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// splitStoreAddress reads "<name>" or "<name>@<identity>", with --identity as the alternative spelling of the second half.
//   - It splits on the first @: a name never contains one and an identity may (scheme@sha256:...).
//   - The combined form lets a line of `store list` output be pasted into `store use`.
func splitStoreAddress(arg, flag string) (string, store.IdentityQuery, error) {
	name, inline, hasInline := strings.Cut(arg, "@")
	switch {
	case hasInline && flag != "" && inline != flag:
		return "", store.IdentityQuery{}, fmt.Errorf(
			"two identities given: %q in the address and %q in --identity", inline, flag)
	case !hasInline:
		inline = flag
	}
	if strings.TrimSpace(inline) == "" {
		return "", store.IdentityQuery{}, errors.New(
			"an identity is required: give it as <name>@<identity>, or with --identity")
	}
	query, err := store.ParseIdentityQuery(inline)
	if err != nil {
		return "", store.IdentityQuery{}, err
	}
	return name, query, nil
}

// storeAddress renders the address that selects one artifact, which is the form
// every store command accepts back.
func storeAddress(candidate store.Candidate) string {
	return candidate.Name + "@" + shortDigest(candidate.Identity)
}

// shortDigest renders the identity prefix that names the file on disk.
func shortDigest(ref meta.KeyRef) string {
	if len(ref.SHA256) < store.DefaultPrefixChars {
		return ref.SHA256
	}
	return ref.SHA256[:store.DefaultPrefixChars]
}
