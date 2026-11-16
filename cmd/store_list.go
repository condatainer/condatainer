package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	artifactcache "github.com/condatainer/condatainer/internal/artifact/cache"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newStoreListCmd() *cobra.Command {
	var equivalence string
	var jsonOutput bool
	var all bool
	var detail bool
	cmd := &cobra.Command{
		Use:     "list [flags] [name]",
		Aliases: []string{"ls"},
		Short:   "List store entries, or the builds that can stand in for one",
		Long: `List store entries by address, grouped by images directory.

- Give a name to list only its builds.
- Each address can be pasted into the other store commands.
- An identity in grey belongs to the build installed under the plain name.`,
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
			var found store.Report
			if equivalence != "" {
				if name == "" {
					return errors.New("store list --equiv requires a name")
				}
				query, err := store.ParseIdentityQuery(equivalence)
				if err != nil {
					return err
				}
				found, err = store.Equivalent(name, query, nil)
				if err != nil {
					return err
				}
			} else {
				// Flat is opt-in: `store list` lists the store. But an overlay under
				// the plain name is the one that currently answers to it, so any
				// question about which build a name means needs --all to be
				// answerable at all.
				found = store.Scan(store.ScanOptions{Name: name, Stored: true, Flat: all})
			}
			store.SortReport(&found)
			if jsonOutput {
				return report.Write(os.Stdout, storeListDoc(found))
			}
			return printStoreReport(found, detail)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Also list the overlay installed under the plain name")
	cmd.Flags().BoolVar(&detail, "detail", false, "Show layout, size and path for each entry")
	cmd.Flags().StringVar(&equivalence, "equiv", "", "List the installed builds that can stand in for an equivalence key or prefix")
	jsonFlag(cmd.Flags(), &jsonOutput)
	return cmd
}

// printStoreReport lists entries grouped by images directory, as `list` does.
//   - By default the address is the whole row, in columns: other store commands accept it. --detail adds layout, size and the filename relative to its directory heading.
func printStoreReport(scanned store.Report, detail bool) error {
	byRoot := map[string][]store.Candidate{}
	var roots []string
	for _, candidate := range scanned.Candidates {
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
	for _, issue := range scanned.Issues {
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

// storeListDoc is the overlays document of a scan, one record per entry. An
// entry that could not be trusted is a problem.
func storeListDoc(scanned store.Report) *report.Overlays[report.Stored] {
	doc := &report.Overlays[report.Stored]{Overlays: report.List[report.Stored]{}}
	records := artifactcache.Default().NewBatch()
	defer records.Flush()
	read := func(path string) (meta.Runtime, error) { return meta.ReadRuntimeWithCache(path, records) }
	for _, candidate := range scanned.Candidates {
		doc.Overlays = append(doc.Overlays, storedRecordWith(candidate, read))
	}
	for _, issue := range scanned.Issues {
		doc.Problems = append(doc.Problems, report.Problem{Name: issue.Path, Message: issue.Error})
	}
	return doc
}
