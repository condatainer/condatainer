package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newStoreRemoveCmd() *cobra.Command {
	var identity string
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:     "remove [flags] <name>@<identity>",
		Aliases: []string{"rm"},
		Short:   "Remove one store entry",
		Long: `Delete one build from the store, chosen by name and identity.

- Only store entries. Use 'condatainer remove' for an overlay under a plain name.
- It fails while a container is using the entry.
- A write-protected overlay is never removed.`,
		Example: `  condatainer store remove star/2.7.11b@9f2c1ab
  condatainer store remove star/2.7.11b --identity 9f2c1ab`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, query, err := splitStoreAddress(args[0], identity)
			if err != nil {
				return err
			}
			return runStoreRemove(cmd, name, query, dryRun, asJSON)
		},
	}
	cmd.Flags().StringVar(&identity, "identity", "", "Complete identity or unambiguous digest prefix")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show which entry would be removed, without removing")
	jsonFlag(cmd.Flags(), &asJSON)
	return cmd
}

// runStoreRemove removes the entry an address names, or with dryRun only checks
// that it could be: its lock is free and it is not write-protected. An entry
// that is found and cannot be removed is kept, with the reason, and ends the
// command with its error after the document is printed.
func runStoreRemove(cmd *cobra.Command, name string, query store.IdentityQuery, dryRun, asJSON bool) error {
	var candidate store.Candidate
	var err error
	if dryRun {
		candidate, err = store.Removable(name, query, nil)
	} else {
		candidate, err = store.Remove(name, query, nil)
	}
	if candidate.Path == "" {
		return err
	}
	rec := storedRecord(candidate)
	var removed []report.Removed
	var kept []report.Kept
	if err != nil {
		reason := report.KeptFailed
		switch {
		case errors.Is(err, image.ErrInUse):
			reason = report.KeptInUse
		case errors.Is(err, image.ErrProtected):
			reason = report.KeptProtected
		}
		kept = append(kept, report.Kept{Overlay: rec.Overlay, Location: rec.Location, Reason: reason, Error: err.Error()})
	} else {
		removed = append(removed, report.Removed{Overlay: rec.Overlay, Location: rec.Location})
	}

	switch {
	case asJSON:
		if werr := writeRemovalDoc(cmd.OutOrStdout(), dryRun, removed, kept); werr != nil {
			return werr
		}
	case err != nil:
	case dryRun:
		utils.PrintMessage("Would remove %s (%s)%s", candidate.Path,
			store.FormatKeyRef(candidate.Identity), config.LayerNote(candidate.Path))
		utils.PrintMessage("Dry run: nothing was removed.")
	default:
		utils.PrintSuccess("Removed %s (%s)%s", candidate.Path,
			store.FormatKeyRef(candidate.Identity), config.LayerNote(candidate.Path))
	}
	return err
}

func newStoreGCCmd() *cobra.Command {
	var (
		graceDay int
		layer    string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Report reclaimable store entries, or delete them",
		Long: `Report which store entries can be deleted and how much space that frees.

- It lists what can be deleted, then asks before deleting. -y answers yes.
- An entry can be deleted when it is older than the grace period, nothing is using it, and it still matches its identity.
- Age is the newest of the access, modification and change times. Each row names the one that decided it.
- If every row says "ctime", the filesystem does not record access times.
- -l/--layer narrows the run, as in 'list'. Deleting requires it.`,
		Example: `  condatainer store gc
  condatainer store gc --layer user --grace 60
  condatainer store gc --layer user -y`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs, err := storeGCDirs(layer)
			if err != nil {
				return err
			}
			opts := store.GCOptions{Dirs: dirs, Grace: time.Duration(graceDay) * 24 * time.Hour}
			if jsonOut {
				// An unanswered question is a no: without -y it only reports.
				opts.Apply = !previewOnly(true)
				collected, err := store.GC(opts)
				if err != nil {
					return err
				}
				return writeCollection(cmd.OutOrStdout(), collected)
			}
			found, err := store.GC(opts)
			if err != nil {
				return err
			}
			printGCReport(found)
			if len(found.Collectable) == 0 {
				return nil
			}
			if layer == "" {
				utils.PrintHint("add --layer to choose where to delete from")
				return nil
			}
			if !utils.ShouldAnswerYes() && !utils.Confirm(cmd.Context(), os.Stderr,
				fmt.Sprintf("Remove %d entrie(s)? Cannot be undone. [y/N]: ", len(found.Collectable))) {
				return nil
			}
			opts.Apply = true
			removed, err := store.GC(opts)
			if err != nil {
				return err
			}
			utils.PrintSuccess("Removed %d entrie(s), %s freed%s.",
				len(removed.Collectable), utils.FormatSize(removed.Reclaimable), config.LayerNote(collectablePaths(removed)...))
			return nil
		},
	}
	cmd.Flags().IntVar(&graceDay, "grace", 0, "Days below which nothing is collected (0 uses store_gc_grace)")
	cmd.Flags().StringVarP(&layer, "layer", "l", "", "Limit to a layer: "+layerChoices)
	jsonFlag(cmd.Flags(), &jsonOut)
	cmd.RegisterFlagCompletionFunc("layer", //nolint:errcheck
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return []string{"user", "cnt-root", "extra-root"}, cobra.ShellCompDirectiveNoFileComp
		})
	return cmd
}

// storeGCDirs narrows the writable image roots to the layer --layer names.
//   - Empty means every writable root, which only a report may have: store.GC refuses an applied run with no scope.
//   - A layer that is not configured is an error rather than an empty report, since "nothing to collect" and "you named a layer that has no root" are answers someone would act on differently.
func storeGCDirs(layer string) ([]string, error) {
	if layer == "" {
		return nil, nil
	}
	return imageDirsOfLayer(layer)
}

// printGCReport shows what was found, largest first.
//
// Retained entries are printed too, and not only as a count: "0 collectable"
// with no explanation is indistinguishable from a broken scan, and the reasons
// are what tell a locked artifact from a pinned one from a young one.
func printGCReport(found *store.GCReport) {
	for _, entry := range found.Collectable {
		label := entry.Name
		if entry.Staging {
			label = "abandoned staging file"
		}
		fmt.Fprintf(os.Stdout, "  %-10s %-28s %8s  %s (%s)\n", "collectable", label,
			utils.FormatSize(entry.Size), entry.Path,
			fmt.Sprintf("%s %s", entry.Basis, utils.FormatDuration(entry.Age)))
	}
	for _, entry := range found.Retained {
		utils.PrintDebug("kept %s: %s", entry.Path, entry.Reason)
	}
	if len(found.Collectable) == 0 {
		utils.PrintSuccess("Nothing to collect (%d entries kept, grace %s).",
			len(found.Retained), utils.FormatDuration(found.Grace))
		return
	}
	utils.PrintSuccess("%d entrie(s) collectable, %s reclaimable%s.",
		len(found.Collectable), utils.FormatSize(found.Reclaimable), config.LayerNote(collectablePaths(found)...))
}

// collectablePaths lists the paths of the entries a gc run found.
func collectablePaths(found *store.GCReport) []string {
	paths := make([]string, len(found.Collectable))
	for i, entry := range found.Collectable {
		paths[i] = entry.Path
	}
	return paths
}

// writeCollection prints the document of a store collection: the entries it
// took, or in a report would take, and the ones it left with the reason.
func writeCollection(w io.Writer, collected *store.GCReport) error {
	entries := report.List[report.Collected]{}
	for _, entry := range collected.Collectable {
		loc := plannedLocation(entry.Path, false)
		loc.Layout, loc.Size = store.LayoutStored, entry.Size
		entries = append(entries, report.Collected{
			Overlay: report.Overlay{Name: entry.Name}, Location: loc, Identity: entry.Identity,
			Age: int64(entry.Age.Seconds()), Basis: string(entry.Basis), Staging: entry.Staging,
		})
	}
	kept := report.List[report.CollectKept]{}
	for _, entry := range collected.Retained {
		kept = append(kept, report.CollectKept{Path: entry.Path, Reason: entry.Reason})
	}
	grace := int64(collected.Grace.Seconds())
	if collected.Applied {
		return report.Write(w, &report.Collection{Grace: grace, Reclaimed: collected.Reclaimable, Removed: entries, Kept: kept})
	}
	return report.Write(w, &report.CollectionPlan{Grace: grace, Reclaimable: collected.Reclaimable, WouldRemove: entries, Kept: kept})
}
