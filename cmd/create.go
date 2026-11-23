package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chzyer/readline"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/conda"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Variables to hold flag values
var (
	createName       string
	createPrefix     string
	createFile       string
	createFrom       string
	createSources    []string
	createUpdate     bool
	createStore      bool
	createNoPrebuilt bool
	createLayer      string
	createDryRun     bool
	createDesc       string
	createJSON       bool
	createNeededBy   bool
	createLatest     bool

	// createFlagSet is createCmd.Flags(), held here so createJobFlags does not refer to createCmd.
	createFlagSet *pflag.FlagSet

	// compFlagNames is the set of compression flags, generated from build.CompressOptions.
	compFlagNames map[string]bool

	// targetFlagNames is the set of flags shown under "Target Flags:" in help: what to create, from where, and where it lands.
	targetFlagNames = map[string]bool{
		"name": true, "prefix": true, "file": true, "from": true,
		"layer": true, "desc": true, "source": true, "channel": true,
	}

	// buildFlagNames is the set of flags shown under "Build Flags:" in help.
	buildFlagNames = map[string]bool{
		"block-size":      true,
		"data-block-size": true, "always-submit-data": true, "no-submit": true, "store": true, "no-prebuilt": true,
	}
)

// jobRepeats marks a flag in a job rule table that the job's command repeats. Any other text is the reason it does not.
const jobRepeats = "repeated"

// createJobRules says, for every create flag, whether a submitted build repeats it.
//   - A new create flag must be listed here, and TestEveryCreateFlagHasAJobRule fails until it is.
//   - A repeated flag also goes into createJobFlags. The compression flags are repeated through compFlagNames.
var createJobRules = map[string]string{
	"source": jobRepeats, "layer": jobRepeats, "channel": jobRepeats,
	"block-size": jobRepeats, "data-block-size": jobRepeats,
	"desc": "the build it describes repeats it",
	"name": "the job builds by target", "prefix": "the job builds by target",
	"file": "the job builds by target", "from": "the job builds by target",
	"update": "the command builder adds it", "store": "the command builder adds it",
	"no-prebuilt":        "the command builder adds it",
	"no-submit":          "the job is the submission",
	"dry-run":            "a dry run submits no job",
	"json":               "output format of this run",
	"always-submit-data": "decides whether to submit, not what is built",
	"needed-by":          "the job builds by target",
	"latest":             "the job builds by target",
	"no-project":         "the job builds by target",
}

// createJobFlags are the flags a submitted build has to repeat: the node re-runs
// create from the name alone, so anything that changes what is built or where it
// lands has to travel with it. Submission and mode flags are left out — the job
// is the submission, and it builds by name.
func createJobFlags() []string { return createJobFlagsFor(createLayer) }

// createJobFlagsFor is createJobFlags for a build placed in layer, or "" for one a prefix places.
func createJobFlagsFor(layer string) []string {
	var flags []string
	for _, source := range createSources {
		flags = append(flags, "--source", source)
	}
	if layer != "" {
		flags = append(flags, "--layer", layer)
	}
	return append(flags, settings.ChangedFlags(createFlagSet,
		"channels", "build.block_size", "build.data_block_size", "build.compress_args")...)
}

var createCmd = &cobra.Command{
	Use:     "create [flags] [packages... | scripts...]",
	Aliases: []string{"install", "i"},
	Short:   "Create a read-only SquashFS overlay",
	Long: `Create a read-only SquashFS overlay.

--desc sets the description of the resulting overlay.
  It takes one Conda package, or --name, --prefix or --file.
  It does not take a recipe name, such as orad/2.7.0.

--needed-by takes scripts and directories, not packages.
  It installs each #DEP: and each overlay a #SLOT: lists.
  A script in a project is refused. Use project restore.

--latest takes the newest version of a bare or partial name, such as star.
  Without it, an installed version is used.

--dry-run shows what would be built, pulled or submitted, and does none of it.
Exits with code 3 if build jobs were submitted to a scheduler.`,
	Example: `  condatainer create samtools/1.22              # From Conda
  condatainer create orad/2.7.0                 # From a recipe source
  condatainer create --source lab star/2.7.11b  # From the lab source
  condatainer create code-server --latest       # The newest, not an installed
  condatainer create -n nvim nvim nodejs        # Several packages, one overlay
  condatainer create matplotlib pandas -p /path
  condatainer create -f environment.yml -p myenv
  condatainer create --from docker://ubuntu:22.04 -p ubuntu
  condatainer create --needed-by run.sh         # Install what a script declares
  condatainer create --needed-by src/ -y        # Skip the question`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return config.SelectSources(createSources)
	},
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		// 1. Validation Logic
		if createNeededBy {
			if err := checkNeededByFlags(args); err != nil {
				ExitWithError("%v", err)
			}
		}
		if createLatest && (createName != "" || createPrefix != "" || createFile != "" || createFrom != "") {
			ExitWithError("--latest takes package names, not --name, --prefix, --file or --from")
		}
		if len(args) == 0 && createFile == "" && createFrom == "" {
			ExitWithError("At least one of [packages], --file, or --from must be provided.")
		}
		if createFrom != "" && createName == "" && createPrefix == "" {
			ExitWithError("When using --from, either --name or --prefix must be provided.")
		}
		if createJSON && !createDryRun {
			ExitWithError("create prints JSON only with --dry-run")
		}
		createDesc = strings.TrimSpace(createDesc)
		if strings.ContainsAny(createDesc, "\r\n") {
			ExitWithError("--desc must be one line")
		}
		// A dry run plans without asking a recipe's questions.
		if createDryRun {
			ctx = build.PlanOnly(ctx)
		}
		if derived := derivePrefixFromFile(createFile, createPrefix, createName); derived != "" {
			createPrefix = derived
		}
		if createPrefix != "" {
			resolved, note, err := resolvePrefix(createPrefix, createName, createFile)
			if err != nil {
				ExitWithError("%v", err)
			}
			createPrefix = resolved
			if note != "" {
				utils.PrintNote("%s", note)
			}
		}
		// Checked after the derivation above, which turns a bare `-f env.yml` into
		// a prefix: it is the prefix that conflicts, however it arrived. A prefix
		// names an exact output path, while the store generates its filename from
		// the artifact's keys — the filename there is the address. Everything else
		// builds into an images directory and can be filed by identity.
		if createPrefix != "" {
			if createStore {
				ExitWithError("--store cannot be used with --prefix: a prefix names the file, and in the store the identity does. Use --name instead.")
			}
			if createLayer != "" {
				ExitWithError("Cannot use both --layer and --prefix: --prefix already names where the overlay goes.")
			}
		}
		if createPrefix != "" && createFile == "" && len(args) == 0 && createFrom == "" {
			ExitWithError("--prefix requires either packages, --file, or --from to be specified.")
		}
		if createDesc != "" && createName == "" && createPrefix == "" && len(args) > 1 {
			ExitWithError("--desc describes one overlay: give one name, or --name or --prefix.")
		}

		// 2. Apptainer runs every build, and what a build makes is mounted to use,
		// so fail here rather than after resolution has already fetched recipes.
		// The base image is not checked: it is an implicit prerequisite of the
		// plan, built with everything else.
		if _, err := apptainer.Resolve(apptainer.Need{Overlays: true}); err != nil {
			ExitWithError("cannot build an overlay: %v", err)
		}

		if createNeededBy {
			runCreateNeededBy(ctx, args)
			return
		}

		// 6. Normalize package names (only for build-script mode, not for conda/prefix/source modes)
		normalizedArgs := args
		if createName == "" && createPrefix == "" && createFrom == "" {
			normalizedArgs = make([]string, len(args))
			for i, arg := range args {
				solved, err := solveCreate(cmd.Context(), arg, createLatest)
				if err != nil {
					ExitWithError("%v", err)
				}
				typedName(solved.Name, utils.NormalizeModule(arg))
				if solved.Newer != "" {
					createNewer[solved.Name] = solved.Newer
				}
				normalizedArgs[i] = solved.Name
			}
		}

		// 7b. Handle --always-submit-data
		if createNoPrebuilt {
			config.Global.SkipPrebuilt = true
		}

		// 8. Announce update mode
		if createUpdate {
			utils.PrintNote("Update mode: existing overlays will be rebuilt.")
		}

		// 9. Execute create based on mode
		if createFrom != "" {
			// Mode: --from (external image like docker://ubuntu)
			runCreateFromSource(ctx)
		} else if createPrefix != "" && len(args) > 0 {
			// Mode: --prefix + packages (conda env at custom path, like conda create -p)
			runCreateWithPrefixAndPackages(ctx, args)
		} else if createPrefix != "" {
			// Mode: --prefix (with --file for YAML/def/sh)
			runCreateWithPrefix(ctx)
		} else if createName != "" {
			// Mode: --name (multiple packages or YAML into one sqf)
			// Pass original args for conda package names (not normalized)
			runCreateWithName(ctx, args)
		} else {
			// Mode: Default (each package gets its own sqf via BuildObject)
			runCreatePackages(ctx, normalizedArgs)
		}
	},
}

func init() {
	rootCmd.AddCommand(createCmd)

	// Register Flags
	f := createCmd.Flags()
	createFlagSet = f
	f.StringVarP(&createName, "name", "n", "", "Custom name for the overlay (with --prefix: the name recorded in it)")
	f.StringVarP(&createPrefix, "prefix", "p", "", "Custom prefix path for the overlay")
	f.StringVarP(&createFile, "file", "f", "", "Path to definition file (.yaml, .txt, .sh, .def, .sif, or a sandbox dir)")
	f.StringVar(&createDesc, "desc", "", "Description of the overlay (Conda package, --name or --prefix)")
	f.StringVar(&createFrom, "from", "", "Build from an external image URI (e.g., docker://ubuntu:22.04)")
	settings.AddFlag(f, "build.block_size", "block-size", settings.Usage("SquashFS block size of all overlays except data (4k to 1m, e.g. 256k)"))
	settings.AddFlag(f, "build.data_block_size", "data-block-size", settings.Usage("SquashFS block size of data overlays (4k to 1m, e.g. 512k)"))
	settings.AddFlag(f, "channels", "channel", settings.Short("c"), settings.Usage("Conda channel to use (overrides config; repeatable)"))
	f.StringArrayVarP(&createSources, "source", "s", nil,
		"Use only this configured recipe source, in flag order (repeatable)")
	f.BoolVarP(&createUpdate, "update", "u", false, "Rebuild overlays even if they already exist")
	f.BoolVar(&createStore, "store", false, "Build into the store, filed under its identity")
	f.BoolVar(&createNoPrebuilt, "no-prebuilt", false, "Build from the recipe instead of pulling a prebuilt overlay")
	f.StringVarP(&createLayer, "layer", "l", "", "Build into this layer: "+layerChoices)
	f.BoolVar(&createDryRun, "dry-run", false, "Show what would be built, pulled or submitted, without doing it")
	f.BoolVar(&createNeededBy, "needed-by", false, "Take scripts and directories, and install what they declare")
	f.BoolVar(&createLatest, "latest", false, "Take the newest version, not an installed one")
	f.BoolVar(&noProjectRequested, "no-project", false, "Ignore the project: its lock, root and distro")
	jsonFlag(f, &createJSON)
	settings.AddSwitch(f, "build.always_submit_data", "always-submit-data", "true", settings.Usage("Submit data builds as scheduler jobs, even without directives"))
	settings.AddSwitch(f, "scheduler.submit_job", "no-submit", "false", settings.Usage("Disable job submission (build locally)"))

	for _, opt := range build.CompressOptions {
		settings.AddSwitch(f, "build.compress_args", opt.Name, opt.Name, settings.Usage(opt.Description))
	}
	registerSettingFlagCompletions(createCmd)
	createCmd.RegisterFlagCompletionFunc("source", sourceHandleCompletion) //nolint:errcheck

	// Mark compression flags in their own section
	compFlagNames = make(map[string]bool, len(build.CompressOptions))
	for _, opt := range build.CompressOptions {
		compFlagNames[opt.Name] = true
	}

	// Custom usage: two labeled sections — "Flags:" and "Build Flags:"
	createCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), "Usage:\n  %s\n", cmd.UseLine())
		if len(cmd.Aliases) > 0 {
			fmt.Fprintf(cmd.OutOrStderr(), "\nAliases:\n  %s\n", cmd.NameAndAliases())
		}
		if cmd.HasExample() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nExamples:\n%s\n", cmd.Example)
		}
		general := pflag.NewFlagSet("", pflag.ContinueOnError)
		target := pflag.NewFlagSet("", pflag.ContinueOnError)
		build := pflag.NewFlagSet("", pflag.ContinueOnError)
		compress := pflag.NewFlagSet("", pflag.ContinueOnError)
		cmd.LocalFlags().VisitAll(func(fl *pflag.Flag) {
			if compFlagNames[fl.Name] {
				compress.AddFlag(fl)
			} else if buildFlagNames[fl.Name] {
				build.AddFlag(fl)
			} else if targetFlagNames[fl.Name] {
				target.AddFlag(fl)
			} else {
				general.AddFlag(fl)
			}
		})
		if general.HasFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nFlags:\n%s", flagUsages(general))
		}
		if target.HasFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nTarget Flags:\n%s", flagUsages(target))
		}
		if build.HasFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nBuild Flags:\n%s", flagUsages(build))
		}
		if compress.HasFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nCompression Flags:\n%s", flagUsages(compress))
		}
		if cmd.HasAvailableInheritedFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nGlobal Flags:\n%s", wrappedFlagUsages(cmd.InheritedFlags()))
		}
		return nil
	})
}

// writableImagesDirFor resolves where a build installs: the layer named, or the
// furthest-out writable directory when none is.
//
// A layer is the only placement control offered, and it is local — LayerUser is
// $SCRATCH on one machine and the XDG data dir on another — so it is a choice
// made per invocation and never recorded anywhere.
func writableImagesDirFor(layer string) (string, error) {
	if strings.TrimSpace(layer) == "" {
		dir, err := config.GetWritableImagesDir()
		if err != nil {
			return "", fmt.Errorf("no writable images directory found: %w", err)
		}
		return dir, nil
	}
	selected, err := config.ParseDataLayer(layer)
	if err != nil {
		return "", err
	}
	return config.GetWritableImagesDirIn(selected)
}

// getWritableImagesDir returns the writable images directory or exits with an error.
// The plan names it with its data layer, since which directories are writable decides
// whether an overlay is installed for everyone or only for the user.
func getWritableImagesDir() string {
	dir, err := writableImagesDirFor(createLayer)
	if err != nil {
		ExitWithError("%v", err)
	}
	createPlanDir = dir
	return dir
}

// readLineWithCompletion reads a line from stdin with tab-completion over completions.
// Ctrl-C / EOF and context cancellation all return context.Canceled.
func readLineWithCompletion(ctx context.Context, prompt string, completions []string) (string, error) {
	items := make([]readline.PrefixCompleterInterface, len(completions))
	for i, v := range completions {
		items[i] = readline.PcItem(v)
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:       prompt,
		AutoComplete: readline.NewPrefixCompleter(items...),
	})
	if err != nil {
		return "", err
	}
	defer rl.Close()

	type result struct {
		s   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := rl.Readline()
		ch <- result{strings.TrimSpace(s), err}
	}()

	select {
	case <-ctx.Done():
		rl.Close()
		return "", context.Canceled
	case r := <-ch:
		if r.err == readline.ErrInterrupt || r.err == io.EOF {
			return "", context.Canceled
		}
		return r.s, r.err
	}
}

// resolveTemplateInteractively prompts the user to choose a value for each placeholder
// in a PH template script and returns the interpolated concrete name (from #TARGET:).
// When --yes is set, defaults are used without prompting.
func resolveTemplateInteractively(ctx context.Context, info *catalog.Entry) (string, error) {
	utils.PrintMessage("Placeholder template: %s", info.Name)
	if info.Description != "" {
		utils.PrintMessage("%s", info.Description)
	}
	if info.TargetTemplate != "" {
		fmt.Fprintf(os.Stdout, "Target: %s\n", info.TargetTemplate)
	}
	tmpl := catalog.NewTemplate(info.TargetTemplate)
	names := tmpl.Names()
	chosenVars := make(map[string]string, len(names))

	// Build per-placeholder installed defaults from single-slash tool #DEP: patterns.
	installedDefaults := map[string]string{}
	if overlays, err := container.InstalledOverlays(); err == nil {
		installedVals := map[string][]string{}
		for _, dep := range info.Deps {
			if !strings.Contains(dep, "{") || strings.Count(dep, "/") != 1 {
				continue
			}
			depTmpl := catalog.NewTemplate(dep)
			for name := range overlays {
				// The dep's tokens are the parent's placeholders, so its declared
				// values are what an installed name has to be one of.
				if vars, ok := depTmpl.Match(name, info.PH); ok {
					for k, v := range vars {
						installedVals[k] = append(installedVals[k], v)
					}
				}
			}
		}
		for k, vals := range installedVals {
			installedDefaults[k] = utils.SortVersionsDescending(vals)[0]
		}
	}

	for _, key := range names {
		vals, ok := info.PH[key]
		if !ok {
			continue
		}

		// Separate concrete values from "*"
		var concrete []string
		hasOpen := false
		for _, v := range vals {
			if v == "*" {
				hasOpen = true
			} else {
				concrete = append(concrete, v)
			}
		}

		// Determine the default: prefer latest installed, fall back to latest available.
		var defaultVal string
		if len(concrete) > 0 {
			defaultVal = concrete[0]
		}
		if iv, ok := installedDefaults[key]; ok {
			if hasOpen || slices.Contains(concrete, iv) {
				defaultVal = iv
			}
		}

		// Build the prompt string
		var prompt string
		n := len(concrete)
		switch {
		case hasOpen && n > 0:
			if n > 8 {
				prompt = fmt.Sprintf("  %s [suggested: %s-%s, or any value] (default: %s): ",
					key, concrete[n-1], concrete[0], defaultVal)
			} else {
				prompt = fmt.Sprintf("  %s [suggested: %s, or any value] (default: %s): ",
					key, strings.Join(concrete, ", "), defaultVal)
			}
		case hasOpen && n == 0:
			prompt = fmt.Sprintf("  %s [any value]: ", key)
		case n > 8:
			prompt = fmt.Sprintf("  %s [%s-%s] (default: %s): ",
				key, concrete[n-1], concrete[0], defaultVal)
		default:
			prompt = fmt.Sprintf("  %s [%s] (default: %s): ",
				key, strings.Join(concrete, ", "), defaultVal)
		}

		if utils.ShouldAnswerYes() {
			if defaultVal != "" {
				fmt.Printf("%s%s\n", prompt, defaultVal)
				chosenVars[key] = defaultVal
			}
			continue
		}

		for {
			input, err := readLineWithCompletion(ctx, prompt, concrete)
			if err != nil {
				return "", err
			}

			// Empty input → use default
			if input == "" {
				if defaultVal == "" {
					utils.PrintWarning("No default available for %q — please enter a value.", key)
					continue
				}
				chosenVars[key] = defaultVal
				break
			}

			// For closed lists, validate against known values
			if !hasOpen {
				valid := false
				for _, v := range concrete {
					if v == input {
						valid = true
						break
					}
				}
				if !valid {
					utils.PrintWarning("Invalid value %q for %s. Valid values: %s",
						input, key, strings.Join(concrete, ", "))
					continue
				}
			}

			chosenVars[key] = input
			break
		}
	}

	concrete, err := tmpl.Fill(chosenVars)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stdout, "  → Creating %s\n", concrete)
	return concrete, nil
}

// createSolved is what a create argument means.
type createSolved struct {
	// Name is the concrete module.
	Name string
	// Expanded is true when Name is not what was typed.
	Expanded bool
	// Newer is a version newer than the installed one that the recipe sources offer for a bare or partial name.
	Newer string
}

// solveCreate resolves a create argument to one concrete module: catalog.SolveName, then anaconda.org's package index, so "r", "rstudio-server" and a bare or partial Conda name like "openjdk/17" each land on one version.
//   - A bare or partial name resolves to the newest installed version it admits, and to the newest anywhere only when none is installed. latest skips the installed ones.
//   - A name that landed on an installed version carries the newer version the recipe sources offer, if there is one. Conda is not asked.
//   - A channel-annotated or fully qualified name that resolves nowhere is returned unchanged for the normal Conda fallback.
func solveCreate(ctx context.Context, nameVersion string, latest bool) (createSolved, error) {
	normalized := utils.NormalizeModule(nameVersion)
	if normalized == "" || strings.Contains(normalized, "::") {
		return createSolved{Name: normalized}, nil
	}
	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return createSolved{Name: normalized}, nil
	}
	var have catalog.Have
	if !latest {
		have = build.InstalledVersions(nil)
	}
	distro := projectDefaultDistro()
	if res, found, err := cat.SolveName(ctx, have, distro, normalized); err == nil && found {
		solved := createSolved{Name: res.Name, Expanded: res.Name != normalized}
		if res.Installed != "" && solved.Expanded {
			solved.Newer = newerVersion(ctx, cat, distro, normalized, res.Name)
		}
		return solved, nil
	}

	resolved, err := solveCondaName(normalized)
	if err != nil {
		return createSolved{Name: normalized}, err
	}
	if resolved != "" {
		return createSolved{Name: resolved, Expanded: resolved != normalized}, nil
	}
	return createSolved{Name: normalized}, nil
}

// solveCreateName is solveCreate for the newest version, installed or not: it returns the module and whether it differs from what was typed.
func solveCreateName(ctx context.Context, nameVersion string) (string, bool, error) {
	solved, err := solveCreate(ctx, nameVersion, true)
	return solved.Name, solved.Expanded, err
}

// newerVersion is the version of what the recipe sources offer for normalized when it is newer than installed, or "".
func newerVersion(ctx context.Context, cat catalog.Catalog, distro, normalized, installed string) string {
	res, found, err := cat.SolveName(ctx, nil, distro, normalized)
	if err != nil || !found || res.Name == installed {
		return ""
	}
	have, errHave := utils.ParseDep(installed)
	want, errWant := utils.ParseDep(res.Name)
	if errHave != nil || errWant != nil || have.Name != want.Name || utils.CompareVersions(want.Version, have.Version) <= 0 {
		return ""
	}
	return want.Version
}

// solveCondaName resolves name against the first configured channel that carries it, picking the newest version Dep.Satisfies admits, so a bare or partial name lands on an exact version before micromamba solves.
//   - It returns "" when the search fails, so a network hiccup does not block an install, or when name carries its own version.
//   - A miss on a name with no version is an error: micromamba could not resolve it either, and this fails faster.
func solveCondaName(name string) (string, error) {
	dep, err := utils.ParseDep(name)
	if err != nil {
		return "", nil
	}
	results, _, err := utils.SearchCondaPackages(dep.Name, conda.Channels(), false, 0)
	if err != nil {
		return "", nil
	}
	if len(results) > 0 {
		for _, v := range results[0].Versions {
			if dep.Satisfies(v) {
				return dep.Name + "/" + v, nil
			}
		}
	}
	if dep.Version != "" {
		return "", nil
	}
	if len(results) == 0 {
		return "", fmt.Errorf("no conda package named %q found in %s", dep.Name, strings.Join(conda.Channels(), ", "))
	}
	return "", fmt.Errorf("no version of %q in channel %s satisfies %q", dep.Name, results[0].Channel, name)
}

// runCreatePackages creates separate sqf files for each package using BuildGraph, and exits when jobs were submitted.
// Example: condatainer create samtools/1.16 bcftools/1.15
func runCreatePackages(ctx context.Context, packages []string) {
	graph, built := buildPackages(ctx, packages)
	finishBuild(graph, built)
}

// buildPackages builds the packages as separate overlays. It returns the graph and how many overlays it builds, pulls or rebuilds, or nil for a dry run.
func buildPackages(ctx context.Context, packages []string) (*build.BuildGraph, int) {
	imagesDir := getWritableImagesDir()

	buildObjects := make([]*build.BuildObject, 0, len(packages))
	for _, pkg := range packages {
		// A bare template name does not identify an artifact; ask which member.
		if cat, err := config.OpenCatalog(ctx); err == nil {
			if m, found, err := cat.Lookup(ctx, pkg); err == nil && found &&
				m.Entry.IsTemplate && len(m.Vars) == 0 {
				if createJSON {
					ExitWithError("%s is a template; with --json, give the full name of one variant, as `condatainer avail -e %s` lists them", pkg, pkg)
				}
				resolved, err := resolveTemplateInteractively(ctx, m.Entry)
				if err != nil {
					ExitWithError("Template resolution cancelled for %s: %v", pkg, err)
				}
				pkg = resolved
			}
		}
		if _, ok := createTyped[pkg]; !ok {
			typedName(pkg, pkg)
		}

		var bo *build.BuildObject
		var err error
		if createStore {
			bo, err = build.NewStoreBuildObject(ctx, pkg, imagesDir, createUpdate)
		} else {
			bo, err = build.NewBuildObject(ctx, pkg, false, imagesDir, createUpdate)
		}
		if err != nil {
			ExitWithError("Failed to create build object for %s: %v", pkg, err)
		}
		if createDesc != "" {
			if bo.BuildType() != build.BuildTypeConda && !bo.IsInstalled() {
				ExitWithError("--desc does not take a recipe name (%s). It takes a Conda package, --name, --prefix or --file.", pkg)
			}
			bo.SetDescription(createDesc)
		}
		applyStoreOverflow(bo)
		utils.PrintDebug("[CREATE] BuildObject created:\n%s", bo)
		buildObjects = append(buildObjects, bo)
	}

	graph, err := build.NewBuildGraph(ctx, buildObjects, imagesDir, scheduler.Enabled(), createUpdate)
	if err != nil {
		ExitWithError("Failed to create build graph: %v", err)
	}
	graph.SetJobFlags(createJobFlags())
	steps := graph.Plan()
	if createDryRun {
		reportCreatePlan(steps)
		return nil, 0
	}
	if !planShown {
		showPlan(steps)
	}

	if err := graph.Run(ctx); err != nil {
		exitOnBuildError(err)
	}
	return graph, builtCount(steps)
}

// derivePrefixFromFile returns the prefix --file implies, or "" when the user
// already named a target. The mode dispatch tests --prefix before --name, so a
// prefix derived while --name is set would silently win over it.
func derivePrefixFromFile(file, prefix, name string) string {
	if file == "" || prefix != "" || name != "" {
		return ""
	}
	return file[:len(file)-len(filepath.Ext(file))]
}

// scriptTarget is the name a shell script or definition declares with #TARGET:,
// or "".
func scriptTarget(file string) string {
	if !isExternalBuildFile(file) {
		return ""
	}
	target, _ := build.GetTargetFromScript(file)
	return target
}

// resolvePrefix settles --prefix against what names the artifact: --name, else
// the script's or definition's #TARGET:, else the basename itself. The first two leave the
// prefix a plain path, except directly in an images directory, where the
// filename is the address and has to be the name's. The basename is spelled as
// the name it reads as (my--env and my=1 are name/version spellings), and
// note says so when that differs from what was typed.
func resolvePrefix(prefix, name, file string) (resolved, note string, err error) {
	if name == "" {
		name = scriptTarget(file)
	}
	if name != "" {
		return prefix, "", checkImageDirPrefix(prefix, utils.NormalizeModule(name))
	}
	base := filepath.Base(prefix)
	named := utils.NormalizeModule(base)
	if named == base {
		return prefix, "", nil
	}
	spelled := image.EncodeArtifactName(named)
	note = fmt.Sprintf("--prefix '%s' names the overlay '%s'", base, named)
	if spelled != base {
		note += fmt.Sprintf("; the file is %s.sqf", spelled)
	}
	return filepath.Join(filepath.Dir(prefix), spelled), note, nil
}

// checkImageDirPrefix refuses a prefix directly in an images directory whose
// filename is not the one name encodes to.
func checkImageDirPrefix(prefix, name string) error {
	abs, err := filepath.Abs(prefix)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(abs)
	base := strings.TrimSuffix(filepath.Base(abs), ".sqf")
	want := image.EncodeArtifactName(name)
	for _, d := range config.GetImageSearchPaths() {
		if filepath.Clean(d) == dir && base != want {
			return fmt.Errorf("--prefix %s is in an images directory, where the filename is the address: it must be %s.sqf to hold %s",
				prefix, want, name)
		}
	}
	return nil
}

// sourceBuildOptions are the build options the flags give one overlay: --desc,
// and --name when --prefix places the file. Without --prefix the file is named
// for the artifact already.
func sourceBuildOptions() []build.Option {
	var opts []build.Option
	if createName != "" && createPrefix != "" {
		opts = append(opts, build.WithName(createName))
	}
	if createDesc != "" {
		opts = append(opts, build.WithDescription(createDesc))
	}
	return opts
}

// prefixJobArgs are the create arguments that rebuild a --prefix script.
func prefixJobArgs(absPrefix, absFile string) []string {
	args := []string{"--prefix", absPrefix, "--file", absFile}
	if createName != "" {
		args = append(args, "--name", createName)
	}
	return args
}

// normalizedTargetName returns --name in catalog form.
//   - Any depth is allowed: a restore recreates the catalog's own names, and a data image is several levels deep (grch38/star/2.7.11b/gencode47-101).
//   - `/` becomes `--` in the filename and Normalize turns it back.
func normalizedTargetName() string {
	return utils.NormalizeModule(createName)
}

// isExternalBuildFile reports whether a file builds through the external-source
// path: a shell recipe or an Apptainer definition.
func isExternalBuildFile(path string) bool {
	return strings.HasSuffix(path, ".sh") ||
		strings.HasSuffix(path, ".bash") ||
		strings.HasSuffix(path, ".def")
}

// isForeignRoot reports whether path is an already-built container root — a
// .sif or an Apptainer sandbox directory — that FromForeignRoot packs
// directly rather than handing to Apptainer's own build.
func isForeignRoot(path string) bool {
	return utils.IsSif(path) || utils.IsSandboxDir(path)
}

// buildForeignSource imports a .sif or sandbox directory into targetPrefix,
// exiting on failure. outputDir holds the image and its scratch space.
func buildForeignSource(ctx context.Context, targetPrefix, source, outputDir string) {
	bo, err := build.FromForeignRoot(ctx, targetPrefix, source, outputDir, createUpdate, sourceBuildOptions()...)
	if err != nil {
		ExitWithError("Failed to import %s: %v", source, err)
	}
	applyStoreOverflow(bo)

	graph, err := build.NewBuildGraph(ctx, []*build.BuildObject{bo}, outputDir,
		scheduler.Enabled(), createUpdate)
	if err != nil {
		ExitWithError("Failed to create build graph: %v", err)
	}
	steps := graph.Plan()
	if createDryRun {
		reportCreatePlan(steps)
		return
	}
	showPlan(steps)
	if err := graph.Run(ctx); err != nil {
		exitOnBuildError(err)
	}
	finishBuild(graph, builtCount(steps))
}

// applyStoreOverflow marks a build to be filed under its identity when --store
// was given.
func applyStoreOverflow(bo *build.BuildObject) {
	if !createStore {
		return
	}
	bo.SetStoreOverflow(true)
}

// buildExternalSource builds one script or definition into targetPrefix, exiting
// on failure. outputDir holds the image and its scratch space. jobArgs are the
// create arguments that rebuild it, for a script that a scheduler job is to run.
func buildExternalSource(ctx context.Context, targetPrefix, source string, isApptainer bool, outputDir string, jobArgs []string) {
	bo, err := build.FromExternalSource(ctx, targetPrefix, source, isApptainer, outputDir, createUpdate, sourceBuildOptions()...)
	if err != nil {
		ExitWithError("Failed to create build object from %s: %v", source, err)
	}
	applyStoreOverflow(bo)
	bo.SetJobArgs(jobArgs)

	graph, err := build.NewBuildGraph(ctx, []*build.BuildObject{bo}, outputDir,
		scheduler.Enabled(), createUpdate)
	if err != nil {
		ExitWithError("Failed to create build graph: %v", err)
	}
	graph.SetJobFlags(createJobFlags())
	steps := graph.Plan()
	if createDryRun {
		reportCreatePlan(steps)
		return
	}
	showPlan(steps)

	if err := graph.Run(ctx); err != nil {
		exitOnBuildError(err)
	}
	finishBuild(graph, builtCount(steps))
}

// runCreateWithName creates a single sqf with multiple packages or from a file
// Example: condatainer create -n myenv nvim nodejs
// Example: condatainer create -n myenv -f environment.yml
// Example: condatainer create -n myenv -f build.sh
func runCreateWithName(ctx context.Context, packages []string) {
	imagesDir := getWritableImagesDir()

	normalizedName := normalizedTargetName()

	// Check if already exists (search all paths), skip only when not updating.
	// --store is asking for a build filed under its own identity, so an installed
	// artifact of that name is what it expects to find, not a reason to stop.
	if !createUpdate && !createStore {
		// Exact names only: an alias would make a bare name look installed when
		// only <base>/<name> is.
		scan, _ := image.ScanOverlays(image.ScanOptions{})
		if existingPath := image.FirstPaths(scan)[normalizedName]; existingPath != "" {
			installed := []build.PlannedStep{installedStep(normalizedName, existingPath)}
			if createDryRun {
				reportCreatePlan(installed)
			} else {
				showPlan(installed)
			}
			return
		}
	}

	utils.PrintDebug("[CREATE] Creating overlay with name: %s", createName)

	// A script or definition is not a conda input. It builds the same way --prefix
	// builds one, targeting the managed images dir instead of a path the user typed.
	if createFile != "" && !utils.IsCondaFile(createFile) {
		if !isForeignRoot(createFile) && !isExternalBuildFile(createFile) {
			ExitWithError("File must be .yml, .yaml, .txt, .sh, .bash, .def, .sif, or a sandbox directory")
		}
		absFile, _ := filepath.Abs(createFile)
		targetPrefix := filepath.Join(imagesDir, strings.ReplaceAll(normalizedName, "/", "--"))
		if isForeignRoot(createFile) {
			buildForeignSource(ctx, targetPrefix, absFile, imagesDir)
		} else {
			buildExternalSource(ctx, targetPrefix, absFile, strings.HasSuffix(createFile, ".def"), imagesDir,
				[]string{"--name", createName, "--file", absFile})
		}
		return
	}

	// Create a conda BuildObject with buildSource set appropriately
	// The buildSource field will contain either:
	// - Path to YAML file (if -f flag used)
	// - Comma-separated package list (if packages provided)
	var buildSource string
	if createFile != "" {
		buildSource, _ = filepath.Abs(createFile)
	} else if len(packages) > 0 {
		// Multiple packages mode - join with commas
		buildSource = strings.Join(packages, ",")
	}

	// Create CondaBuildObject using the new factory function
	bo, err := build.NewCondaObjectWithSource(normalizedName, buildSource, imagesDir, createUpdate, sourceBuildOptions()...)
	if err != nil {
		ExitWithError("Failed to create build object: %v", err)
	}
	applyStoreOverflow(bo)
	buildObject(ctx, bo)
}

// runCreateWithPrefix creates a sqf from external source file (.sh, .def, .yml)
// Example: condatainer create -p myprefix -f environment.yml
// Example: condatainer create -p myprefix -f build.sh
func runCreateWithPrefix(ctx context.Context) {
	absPrefix, _ := filepath.Abs(createPrefix)
	// Use the directory from prefix path as output directory
	outputDir := filepath.Dir(absPrefix)

	if !utils.FileExists(createFile) && !utils.IsSandboxDir(createFile) {
		ExitWithError("File %s not found", createFile)
	}

	utils.PrintDebug("[CREATE] Creating overlay with prefix: %s", createPrefix)

	// Determine file type and create appropriate BuildObject
	if utils.IsCondaFile(createFile) {
		// Conda env/spec file - use NewCondaObjectWithSource
		absFile, _ := filepath.Abs(createFile)
		bo, err := build.NewCondaObjectWithSource(filepath.Base(absPrefix), absFile, outputDir, createUpdate, sourceBuildOptions()...)
		if err != nil {
			ExitWithError("Failed to create build object: %v", err)
		}
		buildObject(ctx, bo)
	} else if isForeignRoot(createFile) {
		// Already-built .sif or sandbox directory
		absFile, _ := filepath.Abs(createFile)
		buildForeignSource(ctx, absPrefix, absFile, outputDir)
	} else if isExternalBuildFile(createFile) {
		// Shell script or apptainer def file
		absFile, _ := filepath.Abs(createFile)
		buildExternalSource(ctx, absPrefix, absFile, strings.HasSuffix(createFile, ".def"), outputDir,
			prefixJobArgs(absPrefix, absFile))
	} else {
		ExitWithError("File must be .yml, .yaml, .txt, .sh, .bash, .def, .sif, or a sandbox directory")
	}
}

// runCreateWithPrefixAndPackages creates a conda sqf from packages at a custom prefix path.
// Example: condatainer create python=3.11 numpy -p /scratch/myenv
func runCreateWithPrefixAndPackages(ctx context.Context, packages []string) {
	absPrefix, _ := filepath.Abs(createPrefix)
	outputDir := filepath.Dir(absPrefix)
	baseName := filepath.Base(absPrefix)
	buildSource := strings.Join(packages, ",")

	bo, err := build.NewCondaObjectWithSource(baseName, buildSource, outputDir, createUpdate, sourceBuildOptions()...)
	if err != nil {
		ExitWithError("Failed to create build object: %v", err)
	}
	buildObject(ctx, bo)
}

// runCreateFromSource creates a sqf from an external source (def file or remote URI)
// Example: condatainer create --from docker://ubuntu:22.04 -n myubuntu
func runCreateFromSource(ctx context.Context) {
	imagesDir := getWritableImagesDir()

	if createPrefix == "" && createName == "" {
		ExitWithError("--from requires either --name or --prefix")
	}

	var targetPrefix string
	if createPrefix != "" {
		targetPrefix, _ = filepath.Abs(createPrefix)
	} else {
		fileName := strings.ReplaceAll(normalizedTargetName(), "/", "--")
		targetPrefix = filepath.Join(imagesDir, fileName)
	}

	source := createFrom
	isRemote := strings.Contains(source, "://")
	if !isRemote {
		source, _ = filepath.Abs(source)
		if !utils.FileExists(source) && !utils.IsSandboxDir(source) {
			ExitWithError("Source %s not found", source)
		}
		if isForeignRoot(source) {
			ExitWithError("--from does not import an already-built .sif or sandbox; use -f %s instead", source)
		}
	}

	isApptainer := strings.HasSuffix(source, ".def") || isRemote
	targetOverlayPath := targetPrefix + ".sqf"

	if !createDryRun {
		utils.PrintMessage("Creating overlay %s from %s", filepath.Base(targetOverlayPath), source)
	}

	buildExternalSource(ctx, targetPrefix, source, isApptainer, imagesDir, nil)
}

func exitOnBuildError(err error) {
	if errors.Is(err, build.ErrBuildCancelled) ||
		strings.Contains(err.Error(), "signal: killed") ||
		strings.Contains(err.Error(), "signal: interrupt") ||
		strings.Contains(err.Error(), "context canceled") {
		// Suppress output as inner layers handle the "cancelled" messaging
	} else {
		utils.PrintError("Build failed: %v", err)
	}
	os.Exit(ExitCodeError)
}
