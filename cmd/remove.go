package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	artifactcache "github.com/condatainer/condatainer/internal/artifact/cache"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	removeJSON    bool
	removePreview bool // --json without -y: report what would be removed
)

var removeCmd = &cobra.Command{
	Use:     "remove [flags] [terms...]",
	Aliases: []string{"rm", "uninstall"},
	Short:   "Remove installed overlays matching search terms",
	Long: `Remove installed overlays by name, alias, or search terms.

- A single plain term must match a name exactly, unlike 'list' and 'avail'.
- Several terms must all match (AND).
- Wildcards (*, ?) and regex also work.
- It asks for confirmation before removing and exit an error when cannot remove.`,
	Example: `  condatainer rm cellranger/9.0.1                   # One version
  condatainer rm cellranger                         # Every version of cellranger
  condatainer rm cellranger/9.0.1 cellranger/8.0.1  # Several overlays
  condatainer rm cellranger 9                       # AND search (multiple terms)
  condatainer rm 'cell*'                            # Wildcard`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runRemove,
}

func init() {
	rootCmd.AddCommand(removeCmd)
	jsonFlag(removeCmd.Flags(), &removeJSON)
	removeCmd.Flags().StringP("layer", "l", "", "Limit to a layer: "+layerChoices)
	removeCmd.RegisterFlagCompletionFunc("layer", //nolint:errcheck
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return []string{"user", "cnt-root", "extra-root"}, cobra.ShellCompDirectiveNoFileComp
		})
}

// imageDirsOfLayer returns the images directories of the layer a --layer value names.
// A layer whose root is not configured has none, which is an error rather than an empty list.
func imageDirsOfLayer(name string) ([]string, error) {
	layer, err := config.ParseDataLayer(name)
	if err != nil {
		return nil, err
	}
	dirs := config.FilterDirsByLayer(config.GetImageSearchPaths(), layer)
	if len(dirs) == 0 {
		return nil, fmt.Errorf("the %s layer is not configured", layer)
	}
	return dirs, nil
}

// scopedImageDirs returns the image search paths, narrowed to --layer when it is set.
// It exits on an error, so an empty result never reads as "no overlays installed".
func scopedImageDirs(cmd *cobra.Command) []string {
	loc, _ := cmd.Flags().GetString("layer")
	if loc == "" {
		return config.GetImageSearchPaths()
	}
	dirs, err := imageDirsOfLayer(loc)
	if err != nil {
		ExitWithError("%v", err)
	}
	return dirs
}

// isScoped reports whether the user restricted the operation to a layer.
func isScoped(cmd *cobra.Command) bool {
	loc, _ := cmd.Flags().GetString("layer")
	return loc != ""
}

func runRemove(cmd *cobra.Command, args []string) error {
	removePreview = previewOnly(removeJSON)
	// Split args: file paths (have overlay extension) vs search terms
	var externalPaths []string
	var searchArgs []string
	for _, arg := range args {
		if utils.IsOverlay(arg) || utils.IsSif(arg) {
			externalPaths = append(externalPaths, arg)
		} else {
			searchArgs = append(searchArgs, arg)
		}
	}
	if len(externalPaths) > 0 && len(searchArgs) > 0 {
		return fmt.Errorf("cannot mix file paths and search terms; use separate commands")
	}
	if len(externalPaths) > 0 {
		return removeExternalOverlays(cmd, externalPaths)
	}

	terms := normalizeFilters(searchArgs)

	// Resolve names within the scoped directories, so a copy shadowed by a
	// higher-priority layer is reachable via --layer.
	installedOverlays, err := getInstalledOverlaysMapIn(scopedImageDirs(cmd))
	if err != nil {
		return err
	}

	if len(installedOverlays) == 0 {
		utils.PrintWarning("No installed overlays found.")
		return deleteNames(cmd, nil, false, nil)
	}

	// Build search query (exact match for single term; exact-first for multiple)
	distroLower := strings.ToLower(projectDefaultDistro())
	distroPrefix := distroLower + "/"
	installedLower := make(map[string]bool, len(installedOverlays))
	for name := range installedOverlays {
		lower := strings.ToLower(name)
		installedLower[lower] = true
		if distroLower != "" {
			if alias, ok := strings.CutPrefix(lower, distroPrefix); ok {
				installedLower[alias] = true
			}
		}
	}
	query := NewSearchQuery(terms, func(term string) bool { return installedLower[term] })

	var filtered []string
	for name := range installedOverlays {
		var alias string
		if distroLower != "" {
			if a, ok := strings.CutPrefix(strings.ToLower(name), distroPrefix); ok {
				alias = a
			}
		}
		if query.MatchesOrAlias(name, alias) {
			filtered = append(filtered, name)
		}
	}

	// Warn about terms that matched nothing in exact mode
	var notFound []string
	if query.Mode == SearchModeExact {
		for _, term := range query.Raw {
			found := false
			for _, name := range filtered {
				lower := strings.ToLower(name)
				if lower == term {
					found = true
					break
				}
				if distroLower != "" {
					if alias, ok := strings.CutPrefix(lower, distroPrefix); ok && alias == term {
						found = true
						break
					}
				}
			}
			if !found {
				utils.PrintWarning("Overlay %s not found among installed overlays.", term)
				notFound = append(notFound, term)
			}
		}
	}

	if len(filtered) == 0 {
		utils.PrintWarning("No matching installed overlays found.")
		return deleteNames(cmd, nil, false, notFound)
	}

	return deleteNames(cmd, filtered, true, notFound)
}

// performDelete removes the named overlays of list -r, after confirmation. names should be in "name/version" format.
//   - Pass showListing=false to skip reprinting the overlay list (e.g. when list -d already showed it).
func performDelete(cmd *cobra.Command, names []string, showListing ...bool) error {
	return deleteNames(cmd, names, len(showListing) == 0 || showListing[0], nil)
}

// removalItem is something to remove: an installed name with its file, or a
// file given by path.
type removalItem struct {
	name string
	path string
}

func (it removalItem) label() string {
	if it.name != "" {
		return it.name
	}
	return filepath.Base(it.path)
}

// deleteNames handles duplicate detection, confirmation, and deletion of named overlays, reading --layer from cmd to restrict to a layer.
//   - notFound are the exact names that matched nothing; they are reported as kept.
//   - A name installed in several layers is refused unless a layer is chosen.
func deleteNames(cmd *cobra.Command, names []string, printListing bool, notFound []string) error {
	scoped := isScoped(cmd)

	allCopies, err := getAllOverlayCopies()
	if err != nil {
		return err
	}

	// Resolve within the scoped dirs rather than filtering the full map: the map
	// keeps only the highest-priority path per name, so filtering it would drop
	// shadowed copies instead of selecting them.
	installedOverlays, err := getInstalledOverlaysMapIn(scopedImageDirs(cmd))
	if err != nil {
		return err
	}

	// Normalize and deduplicate; keep only names present in the (filtered) installed set
	seen := make(map[string]bool)
	var valid []string
	for _, name := range names {
		norm := utils.NormalizeModule(name)
		if seen[norm] {
			continue
		}
		seen[norm] = true
		if _, ok := installedOverlays[norm]; ok {
			valid = append(valid, norm)
		}
	}
	sort.Strings(valid)

	var kept []report.Kept
	for _, term := range notFound {
		kept = append(kept, report.Kept{Overlay: report.Overlay{Name: term}, Reason: report.KeptNotFound})
	}
	if len(valid) == 0 {
		return finishOverlayRemoval(cmd, nil, kept, false)
	}

	// When not scoped, refuse to remove overlays present in multiple dirs
	if !scoped {
		var ambiguous []string
		for _, name := range valid {
			if len(allCopies[name]) > 1 {
				ambiguous = append(ambiguous, name)
			}
		}
		if len(ambiguous) > 0 {
			if !removeJSON {
				utils.PrintWarning("The following overlays exist in multiple layers. Use --layer to pick one:")
			}
			for _, name := range ambiguous {
				if !removeJSON {
					fmt.Printf("  %s\n", name)
					for _, p := range allCopies[name] {
						dir := filepath.Dir(p)
						fmt.Printf("    %s %s\n", dir, utils.StyleDim("("+string(config.ClassifyDataDir(dir))+")"))
					}
				}
				kept = append(kept, report.Kept{Overlay: report.Overlay{Name: name}, Reason: report.KeptAmbiguous,
					Error: "installed in several layers; choose one with --layer"})
			}
			return finishOverlayRemoval(cmd, nil, kept, true)
		}
	}

	// Display grouped by directory
	byDir := make(map[string][]string)
	for _, name := range valid {
		dir := filepath.Dir(installedOverlays[name])
		byDir[dir] = append(byDir[dir], name)
	}
	if printListing && !removeJSON {
		firstDir := true
		for _, dir := range config.GetImageSearchPaths() {
			dirNames := byDir[dir]
			if len(dirNames) == 0 {
				continue
			}
			if !firstDir {
				fmt.Println()
			}
			firstDir = false
			fmt.Println(dirHeader(dir))
			for _, name := range dirNames {
				fmt.Printf("  %s\n", name)
			}
		}
	}

	items := make([]removalItem, len(valid))
	for i, name := range valid {
		items[i] = removalItem{name: name, path: installedOverlays[name]}
	}
	if !confirmOverlayRemoval(cmd, len(items), "overlay(s)") {
		return nil
	}
	return removeItems(cmd, items, kept)
}

// confirmOverlayRemoval asks before a removal, on stderr so the prompt never reaches
// a document on stdout. A preview and -y do not ask. It reports whether to go on.
func confirmOverlayRemoval(cmd *cobra.Command, n int, what string) bool {
	if removePreview || utils.ShouldAnswerYes() {
		return true
	}
	fmt.Fprint(os.Stderr, "\n")
	return utils.Confirm(cmd.Context(), os.Stderr, fmt.Sprintf("Remove %d %s? Cannot be undone. [y/N]: ", n, what))
}

// removeExternalOverlays removes overlay files specified by direct path.
func removeExternalOverlays(cmd *cobra.Command, paths []string) error {
	// Resolve and validate each path
	var items []removalItem
	var kept []report.Kept
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			utils.PrintWarning("Cannot resolve path %s: %v", p, err)
			continue
		}
		if !utils.FileExists(abs) {
			utils.PrintWarning("File not found: %s", abs)
			kept = append(kept, report.Kept{Location: report.Location{Path: abs}, Reason: report.KeptNotFound})
			continue
		}
		items = append(items, removalItem{path: abs})
	}
	if len(items) == 0 {
		return finishOverlayRemoval(cmd, nil, kept, false)
	}

	if !removeJSON {
		fmt.Println("Overlay files to be removed:")
		for _, it := range items {
			fmt.Printf("  %s\n", it.path)
		}
	}
	if !confirmOverlayRemoval(cmd, len(items), "overlay file(s)") {
		return nil
	}
	return removeItems(cmd, items, kept)
}

// removeItems removes each item, or in a preview only checks that it could be.
// What cannot be removed is kept, with the reason.
func removeItems(cmd *cobra.Command, items []removalItem, kept []report.Kept) error {
	var removed []report.Removed
	for _, it := range items {
		rec := removalRecord(it)
		reason, err := removeOne(it, removePreview)
		switch reason {
		case "":
			removed = append(removed, report.Removed{Overlay: rec.Overlay, Location: rec.Location})
			if !removePreview && !removeJSON {
				utils.PrintSuccess("Overlay %s removed%s.", it.label(), config.LayerNote(it.path))
			}
			continue
		case report.KeptProtected:
			if !removeJSON {
				utils.PrintWarning("Overlay %s is in a read-only directory and cannot be removed.", it.label())
			}
		case report.KeptInUse:
			if !removeJSON {
				utils.PrintError("Cannot remove %s: %v", it.label(), err)
			}
		default:
			if !removeJSON {
				utils.PrintError("Failed to remove overlay %s: %v", it.label(), err)
			}
		}
		rec.Reason = reason
		if err != nil {
			rec.Error = err.Error()
		}
		kept = append(kept, rec)
	}
	return finishOverlayRemoval(cmd, removed, kept, false)
}

// removeOne removes one overlay file and its .env sidecar, or with preview only
// checks that it could be: the directory is writable and no process holds the
// file. It returns why it was kept, or "" when it was removed.
func removeOne(it removalItem, preview bool) (string, error) {
	if !utils.CanWriteToDir(filepath.Dir(it.path)) {
		return report.KeptProtected, nil
	}
	lock, err := image.AcquireLock(it.path, true)
	if err != nil {
		return report.KeptInUse, err
	}
	lock.Close()
	if preview {
		return "", nil
	}
	if err := os.Remove(it.path); err != nil {
		return report.KeptFailed, err
	}
	artifactcache.Forget(it.path)
	envPath := it.path + ".env"
	if utils.FileExists(envPath) {
		if err := os.Remove(envPath); err != nil {
			utils.PrintDebug("Failed to remove env file %s: %v", envPath, err)
		}
	}
	return "", nil
}

// removalRecord describes an item before it is touched: its name, type and
// architecture from the image, and where it is.
func removalRecord(it removalItem) report.Kept {
	rec := report.Kept{Overlay: report.Overlay{Name: it.name}, Location: plannedLocation(it.path, true)}
	if rt, err := meta.ReadRuntime(it.path); err == nil {
		rec.Type, rec.Arch = rt.Type, rt.Platform.Arch
		if rec.Name == "" {
			rec.Name = rt.Name
		}
	}
	return rec
}

// finishOverlayRemoval prints the document under --json, and returns the error a removal that left something behind ends with.
// refused marks a removal that did not start.
func finishOverlayRemoval(cmd *cobra.Command, removed []report.Removed, kept []report.Kept, refused bool) error {
	if removeJSON {
		if err := writeRemovalDoc(cmd.OutOrStdout(), removePreview, removed, kept); err != nil {
			return err
		}
	}
	stuck := 0
	for _, k := range kept {
		if k.Reason != report.KeptNotFound {
			stuck++
		}
	}
	if stuck > 0 || refused {
		return fmt.Errorf("%d overlay(s) could not be removed", stuck)
	}
	return nil
}

// writeRemovalDoc prints what a removal took, or in a preview would take, and
// what it left.
func writeRemovalDoc(w io.Writer, preview bool, removed []report.Removed, kept []report.Kept) error {
	if preview {
		return report.Write(w, &report.RemovalPlan{WouldRemove: removed, Kept: kept})
	}
	return report.Write(w, &report.Removal{Removed: removed, Kept: kept})
}

// getAllOverlayCopies returns all paths for each overlay name across all image dirs.
// Unlike getInstalledOverlaysMap, this stores every copy, not just the highest-priority one.
func getAllOverlayCopies() (map[string][]string, error) {
	copies, err := image.ScanOverlays(image.ScanOptions{})
	if err != nil {
		utils.PrintWarning("%v", err)
	}
	return copies, nil
}

// getInstalledOverlaysMap returns a map of overlay name to path from all search paths.
// Only the highest-priority (first found) copy per name is kept.
func getInstalledOverlaysMap() (map[string]string, error) {
	return getInstalledOverlaysMapIn(nil)
}

// getInstalledOverlaysMapIn is getInstalledOverlaysMap scoped to specific
// directories; nil means every search path. Names resolve within dirs only, so a
// copy shadowed by a higher-priority layer stays reachable.
func getInstalledOverlaysMapIn(dirs []string) (map[string]string, error) {
	scan, err := image.ScanOverlays(image.ScanOptions{Dirs: dirs})
	if err != nil {
		// An unreadable directory costs the overlays in it, not the command.
		utils.PrintWarning("%v", err)
	}
	return image.FirstPaths(scan), nil
}
