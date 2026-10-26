package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	ui "github.com/condatainer/condatainer/cmd/internal/ui"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type postScriptHelperFlags struct {
	cpusSet      bool
	cpus         int
	memSet       bool
	mem          string
	timeSet      bool
	time         string
	gpuSet       bool
	gpu          string
	envSet       bool
	env          string
	overlaySet   bool
	overlays     []string
	cwdSet       bool
	cwd          string
	accountSet   bool
	account      string
	partitionSet bool
	partition    string
	excludeSet   bool
	exclude      []string
	newSet       bool
	noProjectSet bool
	waitSet      bool
}

var helperCmd = &cobra.Command{
	Use:   "helper",
	Short: "Run web apps like RStudio on HPC",
	Long: `Run web apps like RStudio inside CondaTainer on HPC.

- A helper submits a job, starts the app, and opens an SSH tunnel to your browser.
- With no arguments, it shows the apps you have running.
- It cannot run inside a container or a scheduler job.`,
	Example: `  condatainer helper                       # Show running apps
  condatainer helper run code-server       # Start code-server
  condatainer helper run code-server -h    # Show its flags and defaults
  condatainer helper config code-server    # Show or save its defaults
  condatainer helper stop                  # Stop a running app
  condatainer helper avail                 # List the helpers the sources offer
  condatainer helper add code-server       # Add one
  condatainer helper upgrade               # Bring added helpers up to date`,
	SilenceUsage: true,
	RunE:         runHelper,
}

var helperRunCmd = &cobra.Command{
	Use:   "run [flags] <name> [script-args...]",
	Short: "Start a helper",
	Long: `Start a helper on a compute node.

- It submits a job, starts the app, and opens an SSH tunnel to your browser.
- It returns once the job is submitted, and prints the access URL when the app is ready.
- Each helper has its own flags, shown by 'condatainer helper run <name> -h'.
- Scheduler options (cpu, mem, time, gpu, account, partition) can be set per run or saved as defaults.`,
	Example: `  condatainer helper run code-server
  condatainer helper run code-server -h
  condatainer helper run rstudio-server --cpu 8 --mem 32G`,
	Args:              cobra.MinimumNArgs(1),
	SilenceUsage:      true,
	RunE:              runHelperRun,
	ValidArgsFunction: completeHelperScripts,
}

var helperStopAll bool

var helperStopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop a running helper",
	Long: `Stop a running helper.

- With no name, it asks which running helper to stop.`,
	Example: `  condatainer helper stop
  condatainer helper stop code-server
  condatainer helper stop --all`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		refuseInsideJob(cmd)
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return runHelperStop(cmd.Context(), name, helperStopAll)
	},
	ValidArgsFunction: completeHelperScripts,
}

var helperConfigCmd = &cobra.Command{
	Use:   "config <name> [path|list|get <key>|set <key> <value>]",
	Short: "List or save a helper's defaults",
	Example: `  condatainer helper config code-server
  condatainer helper config code-server set cpu 4
  condatainer helper config code-server get cpu`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		refuseInsideJob(cmd)
		scriptPath, _ := config.FindHelperScript(args[0])
		return runHelperConfig(args[0], scriptPath, args[1:])
	},
	ValidArgsFunction: completeHelperScripts,
}

func init() {
	rootCmd.AddCommand(helperCmd)
	helperCmd.AddCommand(helperRunCmd, helperStopCmd, helperConfigCmd)
	helperStopCmd.Flags().BoolVarP(&helperStopAll, "all", "a", false, "Stop every match without asking")
	settings.AddSwitch(helperRunCmd.Flags(), "scheduler.submit_job", "no-submit", "false", settings.Usage("Disable job submission (run headless on this node)"))

	// Stop flag parsing after the first positional argument so helper-specific flags
	// (e.g. -c/--cpu) are passed through to parsePostScriptHelperFlags rather than cobra.
	helperRunCmd.Flags().SetInterspersed(false)

	// Usage: the flags before the name, then the flags after it, which are read by hand.
	helperRunCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		out := cmd.OutOrStderr()
		fmt.Fprintf(out, "Usage:\n  %s\n", cmd.UseLine())
		if cmd.HasExample() {
			fmt.Fprintf(out, "\nExamples:\n%s\n", cmd.Example)
		}
		fmt.Fprintf(out, "\nFlags:\n%s\n", cmd.LocalFlags().FlagUsages())
		helper.PrintRunFlags(out, nil)
		if cmd.HasAvailableInheritedFlags() {
			fmt.Fprintf(out, "\nGlobal Flags:\n%s", cmd.InheritedFlags().FlagUsages())
		}
		return nil
	})
	helperConfigCmd.Flags().SetInterspersed(false)
}

// completeHelperScripts provides shell completion for helper script names
func completeHelperScripts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Only complete the first argument (script name), then return to default file completion
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	// Collect scripts from all search paths (user → system)
	seen := make(map[string]bool)
	var scripts []string

	for _, dir := range config.GetHelperScriptSearchPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			// Skip hidden files and directories
			if !entry.IsDir() && !strings.HasPrefix(name, ".") {
				if !seen[name] {
					seen[name] = true
					scripts = append(scripts, name)
				}
			}
		}
	}

	return scripts, cobra.ShellCompDirectiveNoFileComp
}

func parsePostScriptHelperFlags(args []string) (postScriptHelperFlags, []string, error) {
	var flags postScriptHelperFlags
	var passthrough []string

	valueFor := func(name string, i *int, inline string, hasInline bool) (string, error) {
		if hasInline {
			return inline, nil
		}
		*i = *i + 1
		if *i >= len(args) {
			return "", fmt.Errorf("flag %s requires a value", name)
		}
		return args[*i], nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			passthrough = append(passthrough, args[i:]...)
			break
		}

		if strings.HasPrefix(arg, "--") {
			name, val, hasVal := strings.Cut(arg, "=")
			switch name {
			case "--cpu":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				n, err := parsePositiveIntFlag(name, v)
				if err != nil {
					return flags, nil, err
				}
				flags.cpusSet = true
				flags.cpus = n
			case "--mem":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.memSet = true
				flags.mem = v
			case "--time":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.timeSet = true
				flags.time = v
			case "--gpu":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.gpuSet = true
				flags.gpu = v
			case "--env":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.envSet = true
				flags.env = v
			case "--overlay":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.overlaySet = true
				flags.overlays = append(flags.overlays, v)
			case "--cwd":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.cwdSet = true
				flags.cwd = v
			case "--account":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.accountSet = true
				flags.account = v
			case "--partition":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				flags.partitionSet = true
				flags.partition = v
			case "--exclude-nodes":
				v, err := valueFor(name, &i, val, hasVal)
				if err != nil {
					return flags, nil, err
				}
				names := splitNodeFlag(v)
				if err := scheduler.ValidateNodeNames(names); err != nil {
					return flags, nil, fmt.Errorf("flag %s: %w", name, err)
				}
				flags.excludeSet = true
				flags.exclude = names
			case "--new":
				if hasVal {
					return flags, nil, fmt.Errorf("flag %s does not take a value", name)
				}
				flags.newSet = true
			case "--no-project":
				if hasVal {
					return flags, nil, fmt.Errorf("flag %s does not take a value", name)
				}
				flags.noProjectSet = true
			case "--wait":
				if hasVal {
					return flags, nil, fmt.Errorf("flag %s does not take a value", name)
				}
				flags.waitSet = true
			default:
				passthrough = append(passthrough, arg)
			}
			continue
		}

		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			name := arg[:2]
			attached := arg[2:]
			hasAttached := attached != ""
			switch name {
			case "-c":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				n, err := parsePositiveIntFlag(name, v)
				if err != nil {
					return flags, nil, err
				}
				flags.cpusSet = true
				flags.cpus = n
			case "-m":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.memSet = true
				flags.mem = v
			case "-t":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.timeSet = true
				flags.time = v
			case "-g":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.gpuSet = true
				flags.gpu = v
			case "-e":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.envSet = true
				flags.env = v
			case "-o":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.overlaySet = true
				flags.overlays = append(flags.overlays, v)
			case "-w":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.cwdSet = true
				flags.cwd = v
			case "-A":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.accountSet = true
				flags.account = v
			case "-p":
				v, err := valueFor(name, &i, attached, hasAttached)
				if err != nil {
					return flags, nil, err
				}
				flags.partitionSet = true
				flags.partition = v
			default:
				passthrough = append(passthrough, arg)
			}
			continue
		}

		passthrough = append(passthrough, arg)
	}

	return flags, passthrough, nil
}

func parsePositiveIntFlag(name, value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s value %q", name, value)
	}
	return n, nil
}

// refuseInsideJob exits when a helper command runs inside a container or a scheduler job.
func refuseInsideJob(cmd *cobra.Command) {
	if config.IsInsideContainer() {
		cmd.SilenceUsage = true
		ExitWithError("helper commands are not available inside a container")
	}
	if scheduler.IsInsideJob() {
		cmd.SilenceUsage = true
		ExitWithError("helper commands are not available inside a scheduler job")
	}
}

func runHelper(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if len(args) > 0 {
		ExitWithError("unknown command %q: start a helper with `condatainer helper run %s`", args[0], args[0])
	}
	refuseInsideJob(cmd)
	helper.EnsureServer()
	return showHelperStatus("")
}

func runHelperRun(cmd *cobra.Command, args []string) error {
	refuseInsideJob(cmd)
	scriptName := args[0]
	scriptArgs := args[1:]

	// Intercept -h/--help in the remaining args to show helper-specific usage.
	for _, a := range scriptArgs {
		if a == "-h" || a == "--help" {
			scriptPath, findErr := config.FindHelperScript(scriptName)
			if findErr != nil {
				return fmt.Errorf("helper script '%s' not found", scriptName)
			}
			return showHelperHelp(scriptName, scriptPath)
		}
	}

	postFlags, scriptArgs, err := parsePostScriptHelperFlags(scriptArgs)
	if err != nil {
		return err
	}

	// startedWithArgs is true when the user supplied explicit resource/path/param flags,
	// meaning we skip the history-reuse menu and go straight to settings.
	startedWithArgs := postFlags.cpusSet || postFlags.memSet ||
		postFlags.timeSet || postFlags.gpuSet ||
		postFlags.envSet || postFlags.overlaySet || postFlags.cwdSet ||
		postFlags.accountSet || postFlags.partitionSet || postFlags.excludeSet ||
		postFlags.newSet || postFlags.noProjectSet || len(scriptArgs) > 0

	// Find script in all search paths.
	scriptPath, err := config.FindHelperScript(scriptName)
	if err != nil {
		cmd.SilenceUsage = true
		ExitWithError("helper script '%s' not found\nSearched: %s\n%s",
			scriptName, strings.Join(config.GetHelperScriptSearchPaths(), ", "), missingHelperHint(cmd.Context(), scriptName))
	}

	// Check apptainer is available.
	if _, err := apptainer.Normal(); err != nil {
		cmd.SilenceUsage = true
		ExitWithError("apptainer is required to run helper scripts: %v", err)
	}

	// Build resource overrides from explicit flags.
	var overrides *scheduler.ResourceSpec
	if postFlags.cpusSet || postFlags.memSet || postFlags.timeSet || postFlags.gpuSet {
		overrides = &scheduler.ResourceSpec{}
		if postFlags.cpusSet {
			overrides.CpusPerTask = postFlags.cpus
		}
		if postFlags.memSet {
			mb, parseErr := utils.ParseMemoryMB(postFlags.mem)
			if parseErr != nil {
				return fmt.Errorf("invalid --mem value %q: %w", postFlags.mem, parseErr)
			}
			overrides.MemPerNodeMB = mb
		}
		if postFlags.timeSet {
			d, parseErr := utils.ParseWalltime(postFlags.time)
			if parseErr != nil {
				return fmt.Errorf("invalid --time value %q: %w", postFlags.time, parseErr)
			}
			overrides.Time = d
		}
		if postFlags.gpuSet {
			gpu, parseErr := scheduler.ParseGPU(postFlags.gpu)
			if parseErr != nil {
				return fmt.Errorf("invalid --gpu value %q: %w", postFlags.gpu, parseErr)
			}
			overrides.Gpu = gpu
		}
	}

	cwd := ""
	if postFlags.cwdSet {
		abs, err := filepath.Abs(postFlags.cwd)
		if err != nil {
			return fmt.Errorf("invalid --cwd path %q: %w", postFlags.cwd, err)
		}
		cwd = abs
	}

	meta, _ := helper.ParseHelperScriptMeta(scriptPath)
	versions := meta.ParamValues

	// -e - means "no env overlay; skip auto-resolve".
	noEnv := postFlags.envSet && postFlags.env == "-"
	envImg := postFlags.env
	if noEnv {
		envImg = ""
	}

	var excludeNodes []string
	if postFlags.excludeSet {
		excludeNodes = append([]string{}, postFlags.exclude...)
	}

	opts := helper.RunOptions{
		ScriptPath:   scriptPath,
		ScriptName:   scriptName,
		Resources:    overrides,
		EnvImg:       envImg,
		Overlays:     postFlags.overlays,
		CWD:          cwd,
		Account:      postFlags.account,
		Partition:    postFlags.partition,
		ForceNew:     postFlags.newSet,
		ExcludeNodes: excludeNodes,
		NoProject:    postFlags.noProjectSet,
		FlagArgs:     scriptArgs,
	}

	ctx := cmd.Context()
	cmd.SilenceUsage = true

	// Check for running instances; enforce singleton.
	if !opts.ForceNew {
		running, _ := helper.RunningHelpers(opts.ScriptName)
		if helper.SingletonBlocked(meta, running) {
			return ui.OfferSingletonBlocked(opts.ScriptName, running, config.GetRunningServerPort())
		}
		if !startedWithArgs {
			// Combined running + history session list.
			serverPort := config.GetRunningServerPort()
			chosen, isRunning, startNew, sesErr := ui.OfferRecentSessions(ctx, opts.ScriptName, running, serverPort)
			if sesErr != nil {
				if errors.Is(sesErr, context.Canceled) {
					return nil
				}
				return sesErr
			}
			if isRunning && chosen != nil {
				if chosen.Status == "pending" || chosen.Status == "starting" {
					// Not up yet — re-attach and wait for the service URL.
					return ui.MonitorHelper(ctx, chosen.ID, false)
				}
				if url := chosen.AccessURL(serverPort); url != "" {
					utils.PrintSuccess("Access at: %s", url)
				}
				return nil
			}
			if !startNew && chosen != nil {
				ui.ApplyHistoryRun(&opts, chosen)
			}
		} else if len(running) > 0 {
			// User supplied explicit flags — just show a brief notice about running jobs.
			serverPort := config.GetRunningServerPort()
			for _, r := range running {
				if url := r.AccessURL(serverPort); url != "" {
					fmt.Printf("%s already running: %s\n", opts.ScriptName, url)
				} else if r.Status == "pending" || r.Status == "starting" {
					fmt.Printf("%s already submitted (%s); check with `condatainer helper`\n",
						opts.ScriptName, r.Status)
				}
			}
		}
	}

	// Ensure server is running.
	helper.EnsureServer()

	// When -e is unset, look in cwd (preferring env-$USER.img over env.img).
	// An explicit -e value is used literally; -e - opts out.
	if !noEnv && !postFlags.envSet && (opts.EnvImg == "" || opts.EnvImg == "env.img") {
		resolvedCwd := cwd
		if resolvedCwd == "" {
			resolvedCwd, _ = os.Getwd()
		}
		opts.EnvImg = helper.ResolveEnvOverlayInDir(opts.EnvImg, resolvedCwd)
	}

	// Offer to reuse this project's shared history for this helper at this
	// location, ahead of the settings prompt. A no-op outside a project or when
	// -o already set opts.Overlays.
	reused, err := ui.OfferProjectReuse(ctx, opts.ScriptName, &opts)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	// Resolve params and resources together in one combined prompt.
	scriptParams, _ := helper.ParseHelperParams(opts.ScriptPath)
	if err := helper.ValidateHelperParamConflicts(scriptParams); err != nil {
		return err
	}
	flagValues, _, err := helper.ApplyParamFlags(scriptParams, opts.FlagArgs)
	if err != nil {
		return fmt.Errorf("parsing param flags: %w", err)
	}
	for k, v := range reused {
		if _, ok := flagValues[k]; !ok {
			flagValues[k] = v
		}
	}
	saved, _ := helper.LoadHelperConfig(opts.ScriptName)
	spec := helper.ResolveHelperSpec(opts.ScriptPath)
	filled, err := ui.PromptSettings(ctx, scriptParams, flagValues, saved, versions, &opts, spec)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	opts.Params = filled

	// Guided overlay creation for helpers that require a writable conda env.
	// A resolved env.sqf (no .img yet) does not count as writable, so this
	// still runs the wizard — CreateCondaOverlay itself is snapshot-aware, so
	// installing here does not duplicate what the snapshot already has.
	if meta.EnvPackages != "" && !utils.IsImg(opts.EnvImg) {
		created, err := ui.GuidedOverlayCreate(ctx, opts.ScriptName, meta, opts.Params, versions, opts.CWD)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		opts.EnvImg = created
	}

	// Plan: non-interactive validation + resource resolution.
	plan, err := helper.PlanRun(ctx, opts)
	// Offer to install missing conda packages, then retry planning once.
	if err != nil {
		var em *helper.ErrMissingPackages
		if errors.As(err, &em) {
			if installErr := ui.GuidedInstallMissing(ctx, em); installErr != nil {
				if errors.Is(installErr, context.Canceled) {
					return nil
				}
				return installErr
			}
			plan, err = helper.PlanRun(ctx, opts)
		}
	}
	if err != nil {
		var ep *helper.ErrMissingParam
		var es *helper.ErrSingletonRunning
		var ee *helper.ErrEnvInUse
		switch {
		case errors.As(err, &ep):
			return fmt.Errorf("missing required params: %s", ep.Error())
		case errors.As(err, &es):
			return ui.OfferSingletonBlocked(opts.ScriptName, es.Existing, config.GetRunningServerPort())
		case errors.As(err, &ee):
			return fmt.Errorf("env overlay in use: %s", ee.Path)
		}
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	// Print launch spec and wait before submit (when the user supplied explicit flags).
	if startedWithArgs {
		ui.PrintLaunchSpec(plan)
		if err := ui.WaitBeforeStart(ctx, 3*time.Second); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}

	// Execute: submit to scheduler or run headless.
	helperID, err := helper.ExecutePlan(ctx, plan)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	// Without --wait, a detached watcher tells this terminal when the service is
	// ready and records when it stops; a caller with no terminal waits here instead.
	if !postFlags.waitSet {
		if tty := stdoutTTY(); tty != "" {
			if err := startHelperWatcher(helperID, tty); err != nil {
				utils.PrintWarning("Could not start the background watcher: %v", err)
			} else {
				utils.PrintMessage("Started %s. It is printed here when the service is ready; stop it with `condatainer helper stop`.", helperID)
				return nil
			}
		}
	}
	if err := ui.MonitorHelper(ctx, helperID, false); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}

// stdoutTTY is the terminal device this command prints to, or "" when stdout
// is not a terminal.
func stdoutTTY() string {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return ""
	}
	tty, err := os.Readlink("/proc/self/fd/1")
	if err != nil {
		return ""
	}
	return tty
}

// startHelperWatcher launches the hidden _helper_watch command, which prints to
// tty when the helper is ready and ends when the launching shell does.
func startHelperWatcher(id, tty string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	watch := exec.Command(exe, "_helper_watch", "--id", id, "--tty", tty,
		"--shell-pid", strconv.Itoa(os.Getppid()))
	watch.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := watch.Start(); err != nil {
		return err
	}
	return watch.Process.Release()
}

// showHelperStatus lists running helpers (optionally filtered by name) from JSONL history.
func showHelperStatus(name string) error {
	running, err := helper.RunningHelpers(name)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		if name != "" {
			fmt.Printf("No %s helpers running. Use `condatainer helper run %s` to start.\n", name, name)
		} else {
			fmt.Println("No helpers running. Use `condatainer helper run <name>` to start.")
		}
		return nil
	}
	serverPort := config.GetRunningServerPort()
	for _, r := range running {
		url := r.AccessURL(serverPort)
		age := ui.FormatAge(time.Since(r.StartedAt))
		res := ui.FormatRunResources(r)
		cwd := r.CWD
		if cwd == "" {
			cwd = "(no cwd)"
		}
		tag, verb := ui.RunStatusTag(r)
		runnerTag := ""
		if r.Runner != "" {
			runnerTag = " [" + r.Runner + "]"
		}
		fmt.Printf("  %-14s  %-14s  %-40s  %s %s %s ago%s\n", r.Name, res, cwd, tag, verb, age, runnerTag)
		if url != "" {
			fmt.Printf("    %s\n", utils.StyleDim("→ "+url))
		}
	}
	return nil
}

// runHelperConfig handles: condatainer helper config <name> [list|get <key>|set <key> <val>|path|-h]
func runHelperConfig(name, scriptPath string, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	// Load params and spec once (needed for list and -h).
	var params []helper.HelperParam
	var spec *scheduler.ResourceSpec
	if scriptPath != "" {
		params, _ = helper.ParseHelperParams(scriptPath)
		spec = helper.ResolveHelperSpec(scriptPath)
	}

	switch sub {
	case "-h", "--help", "":
		keys := []string{"cpu", "mem", "time", "gpu", "account", "partition"}
		for _, p := range params {
			keys = append(keys, p.Key)
		}
		// Laid out to match cobra's help format; this cannot be a real cobra
		// command because the helper name is only known at runtime.
		fmt.Printf("Show or save default settings for the %s helper.\n\n", name)
		fmt.Printf("Usage:\n  condatainer helper config %s <command>\n\n", name)
		fmt.Println("Available Commands:")
		fmt.Println("  get         Print a single saved value")
		fmt.Println("  path        Print the config file path")
		fmt.Println("  set         Save a default value")
		fmt.Println("  list        List saved defaults")
		fmt.Printf("\nExamples:\n")
		fmt.Printf("  condatainer helper config %s list\n", name)
		fmt.Printf("  condatainer helper config %s set cpu 4\n", name)
		fmt.Printf("  condatainer helper config %s get cpu\n", name)
		fmt.Printf("\nSaveable keys: %s\n", strings.Join(keys, ", "))

	case "path":
		p := helper.HelperConfigPath(name)
		if p == "" {
			return fmt.Errorf("cannot determine config path")
		}
		fmt.Println(p)

	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: `condatainer helper config %s get <key>`", name)
		}
		v, ok := helper.GetHelperConfigKey(name, args[1])
		if !ok {
			return fmt.Errorf("key %q not found in %s config", args[1], name)
		}
		fmt.Println(v)

	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: `condatainer helper config %s set <key> <value>`", name)
		}
		if err := helper.SetHelperConfigKey(name, args[1], args[2]); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

	case "list", "ls", "show":
		cfg, err := helper.LoadHelperConfig(name)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		p := helper.HelperConfigPath(name)
		fmt.Printf("# %s\n", p)

		type configRow struct {
			key, def string
		}
		var rows []configRow
		// Resource rows with spec defaults.
		resDefault := func(v string) string {
			if v == "" {
				return "(none)"
			}
			return v
		}
		cpuDef, memDef, timeDef := "", "", ""
		if spec != nil {
			if spec.CpusPerTask > 0 {
				cpuDef = fmt.Sprintf("%d", spec.CpusPerTask)
			}
			if mb := spec.GetMemPerNodeMB(); mb > 0 {
				memDef = utils.FormatMemoryMB(mb)
			}
			if spec.Time > 0 {
				timeDef = utils.FormatDuration(spec.Time)
			}
		}
		rows = append(rows,
			configRow{"cpu", resDefault(cpuDef)},
			configRow{"mem", resDefault(memDef)},
			configRow{"time", resDefault(timeDef)},
			configRow{"gpu", "(none)"},
			configRow{"account", resDefault(scheduler.Account())},
			configRow{"partition", resDefault(scheduler.Partition())},
		)
		for _, pp := range params {
			rows = append(rows, configRow{strings.ToLower(pp.Key), pp.Default})
		}

		maxKey := 4 // len("time")
		for _, r := range rows {
			if len(r.key) > maxKey {
				maxKey = len(r.key)
			}
		}
		for _, r := range rows {
			val, ok := cfg[strings.ToLower(r.key)]
			valDisp := "(not set)"
			if ok && val != "" {
				valDisp = val
			}
			defDisp := ""
			if r.def != "" {
				defDisp = utils.StyleDim(fmt.Sprintf("(default: %s)", r.def))
			}
			fmt.Printf("  %-*s  %-20s  %s\n", maxKey, r.key, valDisp, defDisp)
		}

	default:
		return fmt.Errorf("unknown config command %q — try `condatainer helper config %s -h`", sub, name)
	}
	return nil
}

func showHelperHelp(scriptName, scriptPath string) error {
	params, err := helper.ParseHelperParams(scriptPath)
	if err != nil {
		return err
	}
	if err := helper.ValidateHelperParamConflicts(params); err != nil {
		return err
	}
	meta, _ := helper.ParseHelperScriptMeta(scriptPath)
	helper.PrintHelperUsage(scriptName, params, helper.ResolveHelperSpec(scriptPath), meta.ParamValues)
	return nil
}

// runHelperStop stops running instances of a helper. name filters to one helper
// script; empty means all. stopAll stops every match without prompting.
//
//   - name set and one instance running: it is stopped directly.
//   - name empty: the picker is always shown, as confirmation.
//   - several instances and stopAll false: the user chooses one or all via OfferStopPicker.
func runHelperStop(ctx context.Context, name string, stopAll bool) error {
	running, err := helper.RunningHelpers(name)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		if name == "" {
			utils.PrintMessage("No running helpers.")
		} else {
			utils.PrintMessage("No running %s helpers.", name)
		}
		return nil
	}

	var toStop []*helper.HelperRun
	// Skip picker only when a specific name is given and there is exactly one match.
	if stopAll || (name != "" && len(running) == 1) {
		toStop = running
	} else {
		chosen, pickErr := ui.OfferStopPicker(ctx, name, running)
		if pickErr != nil {
			return pickErr
		}
		if len(chosen) == 0 {
			return nil // user cancelled
		}
		toStop = chosen
	}

	for _, r := range toStop {
		if err := stopHelperRun(ctx, r); err != nil {
			utils.PrintWarning("Failed to stop %s: %v", r.ID, err)
		} else {
			utils.PrintSuccess("Stopped %s", r.ID)
		}
	}
	return nil
}

// stopHelperRun stops a single helper run with helper.StopRun and marks its
// history entry "done", even if the signal fails (process already dead). A
// helper on another host is left running and the error returned.
func stopHelperRun(ctx context.Context, r *helper.HelperRun) error {
	if err := helper.StopRun(ctx, r); errors.Is(err, helper.ErrOtherHost) {
		return err
	} else if err != nil {
		utils.PrintDebug("stop %s: %v", r.ID, err)
	}
	return helper.UpdateHistoryStatus(r.ID, "done", time.Now())
}

// splitNodeFlag splits a comma-separated --exclude-nodes value. An empty value is an empty, non-nil list.
func splitNodeFlag(v string) []string {
	names := []string{}
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// missingHelperHint says how to get a helper that is not installed: add it when a source offers it, else list what is offered.
func missingHelperHint(ctx context.Context, name string) string {
	if offers, err := helper.RefreshRemoteMetadata(ctx, false, nil); err == nil {
		if _, ok := offers[name]; ok {
			return fmt.Sprintf("Add it with `condatainer helper add %s`", name)
		}
	}
	return "See the helpers a source offers with `condatainer helper avail`"
}
