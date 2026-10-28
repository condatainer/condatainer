package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/condatainer/condatainer/internal/artifact"
	artifactcache "github.com/condatainer/condatainer/internal/artifact/cache"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
)

var listRemove bool
var listExact bool
var listOne bool
var listDescription bool
var listJSON bool

var listCmd = &cobra.Command{
	Use:     "list [flags] [terms...]",
	Aliases: []string{"ls"},
	Short:   "List installed overlays",
	Long: `List installed overlays grouped by directory.

` + searchSyntaxHint,
	Example: `  condatainer list                      # List all
  condatainer list cellranger           # Substring match
  condatainer list cellranger 9         # AND search (multiple terms)
  condatainer list 'cell*'              # Wildcard
  condatainer list cellranger/9.0.1 -e  # Exact match
  condatainer list cellranger 9 -r      # Remove the matches after confirmation`,
	SilenceUsage: true,
	RunE:         runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVarP(&listRemove, "remove", "r", false, "Remove listed overlays after confirmation (used with search terms)")
	listCmd.Flags().BoolVarP(&listExact, "exact", "e", false, "Force exact full-name match even for a single term")
	listCmd.Flags().StringP("layer", "l", "", "Limit to a layer: "+layerChoices)
	listCmd.RegisterFlagCompletionFunc("layer", //nolint:errcheck
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return []string{"user", "cnt-root", "extra-root"}, cobra.ShellCompDirectiveNoFileComp
		})
	listCmd.Flags().BoolVarP(&listOne, "one", "1", false, "One entry per line (disable multi-column layout)")
	listCmd.Flags().BoolVar(&listDescription, "desc", false, "Show description for each overlay (default: off)")
	jsonFlag(listCmd.Flags(), &listJSON)
}

// DirOverlays holds the scan results for a single image directory.
type DirOverlays struct {
	Dir       string
	AppGroups map[string][]string // name → sorted []version
	DataList  []string
	Paths     map[string]string // normalized name/version → full overlay path
	// Misnamed holds images whose recorded name is not the one their filename
	// addresses. They are listed under the recorded name, which is the one that
	// describes the content — and the one -o cannot resolve them by.
	Misnamed []NameMismatch
	// Files is every listed image, one entry each, in directory order.
	Files []ListedFile
}

// ListedFile is one image the scan matched, with the runtime document it read.
type ListedFile struct {
	Path       string
	Runtime    meta.Runtime
	RuntimeErr error
}

// NameMismatch is one image listed under a name its filename does not address.
type NameMismatch struct {
	File     string // basename as installed
	Recorded string // the name inside the image
}

func runList(cmd *cobra.Command, args []string) error {
	filters := normalizeFilters(args)
	filterActive := len(filters) > 0

	// Build exact lookup from installed overlay names (including distro-prefix aliases)
	installedMap, err := getInstalledOverlaysMap()
	if err != nil {
		return err
	}
	distroLower := strings.ToLower(projectDefaultDistro())
	installedLower := make(map[string]bool, len(installedMap))
	for name := range installedMap {
		lower := strings.ToLower(name)
		installedLower[lower] = true
		if alias := catalog.ShortForm(distroLower, lower); alias != lower {
			installedLower[alias] = true
		}
	}
	var listExactLookup func(string) bool
	if listExact {
		listExactLookup = func(string) bool { return true }
	} else if len(filters) > 1 {
		listExactLookup = func(t string) bool { return installedLower[t] }
	}
	query := NewSearchQuery(filters, listExactLookup)

	results := scanOverlaysByDir(scopedImageDirs(cmd), query)
	if listJSON {
		if listRemove {
			return errors.New("--remove asks for confirmation and cannot be combined with --json; use `condatainer remove`")
		}
		return report.Write(os.Stdout, listDoc(results, projectDefaultDistro(), manifestKeys))
	}

	hasAnyMatch := false
	firstSection := true
	for _, d := range results {
		total := len(d.AppGroups) + len(d.DataList)
		// When filtering, skip dirs with no matches
		if filterActive && total == 0 {
			continue
		}
		hasAnyMatch = hasAnyMatch || total > 0

		if !firstSection {
			fmt.Println()
		}
		firstSection = false

		fmt.Println(dirHeader(d.Dir))

		if total == 0 {
			fmt.Println("  (no overlays)")
			continue
		}

		// Separate OS overlays from app overlays
		osOverlays := map[string][]string{}
		moduleOverlays := map[string][]string{}
		for name, versions := range d.AppGroups {
			var osVers, modVers []string
			for _, v := range versions {
				if v == "(system app)" {
					osVers = append(osVers, v)
				} else {
					modVers = append(modVers, v)
				}
			}
			if len(osVers) > 0 {
				osOverlays[name] = osVers
			}
			if len(modVers) > 0 {
				moduleOverlays[name] = modVers
			}
		}

		if len(osOverlays) > 0 {
			fmt.Println(utils.StyleTitle("Available OS overlays:"))
			names := sortedKeys(osOverlays)
			plain := make([]string, len(names))
			styled := make([]string, len(names))
			for i, name := range names {
				plain[i] = name
				styled[i] = name
				if distro := projectDefaultDistro(); distro != "" {
					if alias := catalog.ShortForm(distro, name); alias != name {
						plain[i] += "  [" + alias + "]"
						styled[i] += "  " + ("[" + alias + "]")
					}
				}
			}
			if listDescription {
				for i, name := range names {
					printListDescription(utils.StyleName(name)+strings.TrimPrefix(styled[i], name), readOverlayDescription(d.Paths[name+"/(system app)"]))
				}
			} else {
				printColumns(plain, styled, 0, listTermWidth())
			}
		}

		if len(moduleOverlays) > 0 {
			fmt.Println(utils.StyleTitle("Available app overlays:"))
			var plain, styled []string
			var keys []string // parallel slice for path lookup
			for _, name := range sortedKeys(moduleOverlays) {
				for _, v := range moduleOverlays[name] {
					if v == "(env)" {
						plain = append(plain, name+" (env)")
						styled = append(styled, name+" (env)")
						keys = append(keys, name+"/(env)")
					} else {
						plain = append(plain, name+"/"+v)
						styled = append(styled, name+"/"+v)
						keys = append(keys, name+"/"+v)
					}
				}
			}
			if listDescription {
				for i := range plain {
					printListDescription(utils.StyleName(plain[i]), readOverlayDescription(d.Paths[keys[i]]))
				}
			} else {
				printColumns(plain, styled, 0, listTermWidth())
			}
		}

		if len(d.DataList) > 0 {
			fmt.Println(utils.StyleTitle("Available data overlays:"))
			if listDescription {
				for _, data := range d.DataList {
					printListDescription(utils.StyleName(data), readOverlayDescription(d.Paths[data]))
				}
			} else {
				printColumns(d.DataList, d.DataList, 0, listTermWidth())
			}
		}

		for _, m := range d.Misnamed {
			utils.PrintWarning("%s records the name %s; it is listed under that name but only %s resolves it.",
				m.File, m.Recorded, image.DecodeArtifactName(strings.TrimSuffix(m.File, filepath.Ext(m.File))))
		}
	}

	if !hasAnyMatch && filterActive {
		utils.PrintWarning("No installed overlays match the provided search terms.")
		os.Exit(ExitCodeError)
	}

	if listRemove && filterActive && hasAnyMatch {
		var allMatching []string
		for _, d := range results {
			for name, versions := range d.AppGroups {
				for _, version := range versions {
					if version == "(system app)" || version == "(env)" {
						allMatching = append(allMatching, name)
					} else {
						allMatching = append(allMatching, name+"/"+version)
					}
				}
			}
			allMatching = append(allMatching, d.DataList...)
		}
		return performDelete(cmd, allMatching, false)
	}

	return nil
}

// scanOverlaysByDir scans each image directory independently and returns per-dir results.
// Missing directories are skipped; empty directories are included with empty groups.
func scanOverlaysByDir(dirs []string, query *SearchQuery) []DirOverlays {
	distroLower := strings.ToLower(projectDefaultDistro())
	var result []DirOverlays
	cacheBatch := artifactcache.Default().NewBatch()
	defer cacheBatch.Flush()

	for _, imageDir := range dirs {
		if !utils.DirExists(imageDir) {
			continue // skip missing
		}
		d := DirOverlays{Dir: imageDir, AppGroups: map[string][]string{}, Paths: map[string]string{}}

		entries, err := os.ReadDir(imageDir)
		if err != nil {
			result = append(result, d)
			continue
		}

		appGrouped := map[string]map[string]struct{}{}
		for _, entry := range entries {
			if entry.IsDir() || !utils.IsOverlay(entry.Name()) {
				continue
			}
			nameVersion := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			normalized := strings.ToLower(utils.NormalizeModule(nameVersion))
			overlayPath := filepath.Join(imageDir, entry.Name())
			runtime, runtimeErr := meta.ReadRuntimeWithCache(overlayPath, cacheBatch)
			presentation := presentListOverlay(nameVersion, runtime, runtimeErr == nil)
			// Recorded only where it was read; the mismatch is noted for the
			// images that are actually listed, not for every file in the dir.
			misnamed := runtimeErr == nil && runtime.Name != "" &&
				runtime.Name != image.DecodeArtifactName(nameVersion)
			mismatch := NameMismatch{File: entry.Name(), Recorded: runtime.Name}

			file := ListedFile{Path: overlayPath, Runtime: runtime, RuntimeErr: runtimeErr}

			if presentation.data {
				// Data overlay
				if query.Matches(normalized) {
					d.DataList = append(d.DataList, presentation.name)
					d.Paths[presentation.name] = overlayPath
					d.Files = append(d.Files, file)
					if misnamed {
						d.Misnamed = append(d.Misnamed, mismatch)
					}
				}
				continue
			}

			// App overlay
			if !query.Matches(normalized) {
				alias := catalog.ShortForm(distroLower, normalized)
				if alias == normalized || !query.Matches(alias) {
					continue
				}
			}

			name, version := presentation.name, presentation.version
			if name == "" {
				continue
			}
			if appGrouped[name] == nil {
				appGrouped[name] = map[string]struct{}{}
			}
			appGrouped[name][version] = struct{}{}
			d.Paths[name+"/"+version] = overlayPath
			d.Files = append(d.Files, file)
			if misnamed {
				d.Misnamed = append(d.Misnamed, mismatch)
			}
		}

		for name, versions := range appGrouped {
			vers := make([]string, 0, len(versions))
			for v := range versions {
				vers = append(vers, v)
			}
			sort.Strings(vers)
			d.AppGroups[name] = vers
		}
		sort.Strings(d.DataList)
		result = append(result, d)
	}
	return result
}

type listPresentation struct {
	name    string
	version string
	data    bool
}

// presentListOverlay trusts recorded runtime type and name. An image without
// readable metadata is classified by the depth of its filename instead.
func presentListOverlay(encodedName string, runtime meta.Runtime, recorded bool) listPresentation {
	decoded := strings.ReplaceAll(encodedName, "--", "/")
	if recorded {
		name := runtime.Name
		if name == "" {
			name = decoded
		}
		switch runtime.Type {
		case artifact.TypeData:
			return listPresentation{name: name, data: true}
		case artifact.TypeOS:
			return listPresentation{name: name, version: "(system app)"}
		default:
			if base, version, ok := strings.Cut(name, "/"); ok {
				if i := strings.LastIndex(name, "/"); i >= 0 {
					base, version = name[:i], name[i+1:]
				}
				return listPresentation{name: base, version: version}
			}
			return listPresentation{name: name, version: "(env)"}
		}
	}
	if strings.Count(encodedName, "--") > 1 {
		return listPresentation{name: decoded, data: true}
	}
	if name, version, ok := strings.Cut(encodedName, "--"); ok {
		return listPresentation{name: name, version: version}
	}
	return listPresentation{name: encodedName, version: "(env)"}
}

// dirHeader returns a full-width separator line with the directory path and its
// data layer centered. Padding is measured on the unstyled text, since the styled
// form carries ANSI escapes that occupy no display width.
func dirHeader(path string) string {
	w := terminalWidth()
	layer := "(" + string(config.ClassifyDataDir(path)) + ")"
	plain := " " + path + " " + layer + " "
	inner := " " + path + " " + utils.StyleDim(layer) + " "
	pad := w - len(plain)
	if pad < 2 {
		return inner
	}
	left := pad / 2
	right := pad - left
	return strings.Repeat("═", left) + inner + strings.Repeat("═", right)
}

// terminalWidth returns the current terminal width, defaulting to 80.
func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// listTermWidth returns 0 (single column) when -1 or --desc is set, otherwise the terminal width.
func listTermWidth() int {
	if listOne || listDescription {
		return 0
	}
	return terminalWidth()
}

func printListDescription(name, description string) {
	fmt.Println(formatListDescription(name, description, terminalWidth()))
}

func formatListDescription(name, description string, width int) string {
	line := name
	if description != "" {
		line += "\n" + formatDescription(description, 2, width)
	}
	return line
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printColumns prints items in a newspaper-style multi-column layout.
// plain[i] is used for width calculation; styled[i] is what gets printed.
// indent is the number of leading spaces per row.
func printColumns(plain, styled []string, indent, termWidth int) {
	if len(plain) == 0 {
		return
	}
	maxW := 0
	for _, s := range plain {
		if len(s) > maxW {
			maxW = len(s)
		}
	}
	colWidth := maxW + 2
	numCols := max((termWidth-indent)/colWidth, 1)
	numRows := (len(plain) + numCols - 1) / numCols
	prefix := strings.Repeat(" ", indent)
	for row := range numRows {
		fmt.Print(prefix)
		for col := range numCols {
			idx := col*numRows + row
			if idx >= len(plain) {
				break
			}
			isLast := col == numCols-1 || (col+1)*numRows+row >= len(plain)
			if isLast {
				fmt.Print(styled[idx])
			} else {
				fmt.Print(styled[idx] + strings.Repeat(" ", colWidth-len(plain[idx])))
			}
		}
		fmt.Println()
	}
}

// readOverlayDescription returns an image's recorded description, or empty when
// it has no readable metadata. Listing must not fail on such an image, so the
// read error is dropped: the row still appears, just without a description.
func readOverlayDescription(overlayPath string) string {
	rt, err := meta.ReadRuntime(overlayPath)
	if err != nil {
		return ""
	}
	return rt.Description
}

// listDoc is the overlays document for a scan: one record per image, nearest
// directory first, with the keys keysOf reads from it. A name already seen in a nearer directory is shadowed there.
func listDoc(results []DirOverlays, distro string, keysOf func(path string) (*meta.Keys, error)) *report.Overlays[report.Installed] {
	doc := &report.Overlays[report.Installed]{Overlays: report.List[report.Installed]{}}
	distroLower := strings.ToLower(distro)
	seen := map[string]bool{}
	for _, d := range results {
		for _, file := range d.Files {
			rec := report.Installed{Location: plannedLocation(file.Path, true), Shadowed: false}
			rec.Layout = store.LayoutFlat
			fileName := image.DecodeArtifactName(strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path)))
			rec.Name = fileName
			if file.RuntimeErr != nil {
				doc.Problems = append(doc.Problems, report.Problem{Name: fileName,
					Message: fmt.Sprintf("cannot read the runtime of %s: %v", filepath.Base(file.Path), file.RuntimeErr)})
			} else {
				rt := file.Runtime
				rec.Name, rec.Type, rec.Arch, rec.Runtime = rt.Name, rt.Type, rt.Platform.Arch, &rt
				if keys, err := keysOf(file.Path); err != nil {
					doc.Problems = append(doc.Problems, report.Problem{Name: rt.Name,
						Message: fmt.Sprintf("cannot read the manifest of %s: %v", filepath.Base(file.Path), err)})
				} else {
					rec.Keys = keys
				}
				if rt.Name != "" && rt.Name != fileName {
					doc.Problems = append(doc.Problems, report.Problem{Name: rt.Name,
						Message: fmt.Sprintf("the file is named %s; the image says %s", filepath.Base(file.Path), rt.Name)})
				}
			}
			lower := strings.ToLower(rec.Name)
			if alias := catalog.ShortForm(distroLower, lower); alias != lower {
				rec.Alias = alias
			}
			rec.Shadowed = seen[rec.Name]
			seen[rec.Name] = true
			doc.Overlays = append(doc.Overlays, rec)
		}
	}
	return doc
}

// manifestKeys reads the keys an image's manifest records, nil when it records
// none. An image with no manifest has none and is not an error.
func manifestKeys(path string) (*meta.Keys, error) {
	m, err := meta.ReadManifest(path)
	switch {
	case errors.Is(err, meta.ErrNoManifest):
		return nil, nil
	case err != nil:
		return nil, err
	case m.Keys.Identity.Empty() && m.Keys.Equiv.Empty() && m.Keys.Payload.Empty():
		return nil, nil
	}
	return &m.Keys, nil
}
