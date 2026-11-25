package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/sif"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	"github.com/condatainer/condatainer/internal/runtime/container"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/condatainer/condatainer/internal/runtime/usage"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	runWritableImg bool
	// runBaseImage holds the resolved default root once ensureRootBaseImage has
	// run; "" until then, and always "" if the run's own overlays supply a root.
	runBaseImage    string
	runEnvSettings  []string
	runSlots        []string
	runBindPaths    []string
	runFakeroot     bool
	runStdout       string
	runStderr       string
	runAfterOK      string
	runAfterNotOK   string
	runAfterAny     string
	runCPU          int
	runMem          string
	runTime         string
	runGPU          string
	runAccount      string
	runPartition    string
	runExcludeNodes []string
	runDryRun       bool
	runSubmit       bool
	runSubmitOn     string
	runArray        string
	runArrayLimit   int
	runName         string
	runUsage        bool
	runProjectDir   string
)

// errRunAborted signals a handled stop (message already printed); caller returns nil.
var errRunAborted = errors.New("run aborted")

// runJobFlagNames is the set of flags shown under "Job Flags:" in help.
var runJobFlagNames = map[string]bool{
	"output": true, "error": true,
	"afterok": true, "afternotok": true, "afterany": true,
	"cpu": true, "mem": true, "time": true, "gpu": true, "account": true, "partition": true, "exclude-nodes": true,
	"dry-run": true, "name": true, "array": true, "array-limit": true,
	"no-submit": true, "submit": true, "submit-on": true,
}

// Reasons --submit is refused. It never falls back to a local run.
var (
	errSubmitWithNoSubmit  = errors.New("--submit and --no-submit cannot be used together")
	errSubmitNoScheduler   = errors.New("--submit needs a scheduler, and none is available (job submission is off or none was found)")
	errSubmitOnNeedsSubmit = errors.New("--submit-on picks the node the job is submitted from, so it needs --submit inside a container")
)

// jobIDRe matches a single scheduler job ID.
// Slurm/LSF: pure digits. PBS: digits followed by .hostname (e.g. 12345.school.edu). HTCondor: ClusterID.ProcessID (e.g. 12345.0).
var jobIDRe = regexp.MustCompile(`^\d+(\.\S+)?$`)

var runCmd = &cobra.Command{
	Use:   "run [flags] <script> [script-args...]",
	Short: "Run a script with the overlays its #DEP: lines name",
	Long: `Run a script with the overlays its #DEP: lines name.

The first line picks the interpreter:
  Bash       : #!/bin/bash or #!/usr/bin/bash
  Interpreter: #!/bin/env  or #!/usr/bin/env <interpreter>

The script can contain these comment tags in its header:
  - #DEP: name/version    Declares a dependency, loaded automatically
  - #DEP: /path/env.img   Declares an overlay file (.sqf or .img)
  - #SLOT: name default=o An overlay chosen with --slot NAME=OVERLAY
  - #BIND: host:container Extra bind mount, repeatable
  - #WRITABLE:            Mount .img overlays writable
  - #FAKEROOT:            Run with fakeroot privileges
  - #NCPU: 4              CPUs (per rank in an MPI job)
  - #MEM: 16G             Memory (per rank in an MPI job)
  - #TIME: 2h             Walltime
  - #GPU: a100:2          GPUs per node: N, TYPE or TYPE:N
  - #NAME:                Job name
  - #OUTPUT: #ERROR:      Log files (stdout, stderr)
  - #PARTITION: #ACCOUNT: Partition and account

A script with any resource or job tag is submitted as a job with shell env.
Resources come from the first source that sets one:
  Current scheduler | TAG: | another scheduler.
A directive that sets no resource, such as --qos, passes through to the job.

A build recipe, which expands $CNT_BUILD_PREFIX, is refused.

--dry-run shows what would happen, and exits 1 when the run would not start.`,
	Example: `  condatainer run script.sh            # Check dependencies, then run
  condatainer run script.sh arg1 arg2  # Pass arguments to the script
  condatainer run -o log/s1.out run_tool.sh sample1
  condatainer run -g a100:2 gpu_job.sh
  condatainer run -c 8 -m 16G -t 2h job.sh

  # Run one job after another
  JOB=$(condatainer run run_align.sh sample1)
  condatainer run --afterok "$JOB" run_quant.sh sample1`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true, // Runtime errors should not show usage
	RunE:         runScript,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().BoolVarP(&runWritableImg, "writable", "w", false, "Make .img overlays writable (default: read-only)")
	runCmd.Flags().StringVarP(&runStdout, "output", "o", "", "Override job stdout path (creates parent dir if needed)")
	runCmd.Flags().StringVarP(&runStderr, "error", "e", "", "Override job stderr path")
	runCmd.Flags().StringVar(&runAfterOK, "afterok", "", "Run after jobs succeed (colon-separated IDs, e.g. 123:456)")
	runCmd.Flags().StringVar(&runAfterNotOK, "afternotok", "", "Run after jobs fail (colon-separated IDs)")
	runCmd.Flags().StringVar(&runAfterAny, "afterany", "", "Run after jobs finish, any outcome (colon-separated IDs)")
	runCmd.Flags().StringArrayVar(&runEnvSettings, "env", nil, "Set environment variable KEY=VALUE (repeatable)")
	runCmd.Flags().StringArrayVar(&runSlots, "slot", nil, "Bind a #SLOT: to an overlay, SLOT=OVERLAY, once per slot")
	runCmd.Flags().StringSliceVar(&runBindPaths, "bind", nil, "Bind mount HOST:CONTAINER (repeatable)")
	runCmd.Flags().BoolVarP(&runFakeroot, "fakeroot", "f", false, "Run with fakeroot privileges")
	runCmd.Flags().IntVarP(&runCPU, "cpu", "c", 0, "Override CPUs, per rank in an MPI job (e.g. 4)")
	runCmd.Flags().StringVarP(&runMem, "mem", "m", "", "Override memory, per rank in an MPI job (e.g. 4G, 8192M)")
	runCmd.Flags().StringVarP(&runTime, "time", "t", "", "Override walltime (e.g. 4d12h, 2h30m, 01:30:00)")
	runCmd.Flags().StringVarP(&runGPU, "gpu", "g", "", "Override GPUs per node (e.g. 1, a100:2, a100)")
	runCmd.Flags().StringVarP(&runAccount, "account", "A", "", "Override billing/allocation account")
	runCmd.Flags().StringVarP(&runPartition, "partition", "p", "", "Override partition/queue")
	runCmd.Flags().StringSliceVar(&runExcludeNodes, "exclude-nodes", nil, "Override the excluded nodes (comma-separated; empty for none)")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Show what would happen, without doing it")
	runCmd.Flags().BoolVar(&runUsage, usage.Flag, false, "Print the wall time, CPU time and peak memory the run used")
	settings.AddSwitch(runCmd.Flags(), "scheduler.submit_job", "no-submit", "false", settings.Usage("Disable job submission (run locally)"))
	runCmd.Flags().BoolVar(&runSubmit, "submit", false, "Submit a new job from a job or a container (via host-run)")
	runCmd.Flags().StringVar(&runSubmitOn, "submit-on", "", "Node to submit from: localhost, login or a node name (default: host_run.on)")
	runCmd.Flags().StringVarP(&runName, "name", "n", "", "Override job name (letters, digits, '.', '_' and '-')")
	runCmd.Flags().StringVar(&runArray, "array", "", "Input file for array job (one entry per line)")
	runCmd.Flags().IntVar(&runArrayLimit, "array-limit", 0, "Max concurrent array subjobs (0 = unlimited)")
	RegisterProjectFlags(runCmd, &runProjectDir)
	runCmd.Flags().SetInterspersed(false) // Stop flag parsing after script name; remaining args are passed to the script

	// Custom usage: two labeled sections — "Container Flags:" and "Job Flags:"
	runCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), "Usage:\n  %s\n", cmd.UseLine())
		if cmd.HasExample() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nExamples:\n%s\n", cmd.Example)
		}
		container := pflag.NewFlagSet("", pflag.ContinueOnError)
		job := pflag.NewFlagSet("", pflag.ContinueOnError)
		cmd.LocalFlags().VisitAll(func(fl *pflag.Flag) {
			if runJobFlagNames[fl.Name] {
				job.AddFlag(fl)
			} else {
				container.AddFlag(fl)
			}
		})
		if container.HasFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nContainer Flags:\n%s", wrappedFlagUsages(container))
		}
		if job.HasFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nJob Flags:\n%s", wrappedFlagUsages(job))
		}
		if cmd.HasAvailableInheritedFlags() {
			fmt.Fprintf(cmd.OutOrStderr(), "\nGlobal Flags:\n%s", wrappedFlagUsages(cmd.InheritedFlags()))
		}
		return nil
	})
}

func runScript(cmd *cobra.Command, args []string) error {
	switch {
	case runSubmit && runInsideContainer():
		return submitThroughHost(cmd, args)
	case runSubmitOn != "":
		ExitWithError("%v", errSubmitOnNeedsSubmit)
	}
	if err := applyProjectFlag(runProjectDir); err != nil {
		return err
	}
	for _, flagInfo := range []struct{ name, val string }{
		{"afterok", runAfterOK},
		{"afternotok", runAfterNotOK},
		{"afterany", runAfterAny},
	} {
		if cmd.Flags().Changed(flagInfo.name) {
			if flagInfo.val == "" {
				ExitWithError("--%s is empty; the upstream job may have failed to submit or run locally", flagInfo.name)
			}
			for id := range strings.SplitSeq(flagInfo.val, ":") {
				if id = strings.TrimSpace(id); !jobIDRe.MatchString(id) {
					ExitWithError("--%s %q is not a valid job ID", flagInfo.name, id)
				}
			}
		}
	}

	if runName != "" {
		if _, err := scheduler.ParseJobName(runName); err != nil {
			ExitWithError("--name: %v", err)
		}
	}

	resFlags, err := parseResourceFlags(cmd.Flags())
	if err != nil {
		ExitWithError("%v", err)
	}

	// Build array spec early so we can validate before doing expensive work
	arraySpec, err := buildArraySpec()
	if err != nil {
		ExitWithError("%v", err)
	}

	scriptPath := args[0]
	scriptArgs := args[1:] // arguments for the script itself

	// Validate script exists
	if !utils.FileExists(scriptPath) {
		ExitWithError("Script file %s not found", scriptPath)
	}
	if err := requireTextScript(scriptPath); err != nil {
		ExitWithError("%v", err)
	}

	originScriptPath := scriptPath
	scriptPath, err = filepath.Abs(scriptPath)
	if err != nil {
		ExitWithError("Failed to get absolute path: %v", err)
	}

	// Slots are bound first, since a conditional resource header chooses its line by the overlay a slot is bound to.
	// A .sub file has no headers, and its content script is not known until its specs are read.
	var slots *slotPlan
	var slotErr error
	slotsPlanned := !strings.HasSuffix(scriptPath, ".sub")
	subjects := scheduler.Subjects{}
	if slotsPlanned {
		slots, slotErr = planSlots(cmd.Context(), scriptPath, runSlots, projectCoversScript(scriptPath))
		subjects = slotSubjects(scriptPath, slots)
	}
	subjects.Skip = flagResourceHeaders()

	// 1. Read specs → resolve the content script (HTCondor: .sub → .sh; others: identity)
	contentScript, scriptSpecs := resolveScriptAndSpecs(scriptPath, subjects)
	if err := requireInterpreterLine(contentScript); err != nil {
		ExitWithError("%v", err)
	}
	if err := requireNotBuildRecipe(contentScript); err != nil {
		ExitWithError("%v", err)
	}
	if runSubmit {
		scriptSpecs = submitDefaultSpecs(scriptSpecs)
	}

	// CLI -o/-e/-n override script directives (highest priority)
	if scriptSpecs != nil {
		if runName != "" {
			scriptSpecs.Control.JobName = runName
		}
		if runStdout != "" {
			scriptSpecs.Control.Stdout = runStdout
		}
		if runStderr != "" {
			scriptSpecs.Control.Stderr = runStderr
		}
		// CLI -A/-p > script directive > scheduler default.
		if runAccount != "" {
			scriptSpecs.Control.Account = runAccount
		} else if scriptSpecs.Control.Account == "" {
			scriptSpecs.Control.Account = scheduler.Account()
		}
		if runPartition != "" {
			scriptSpecs.Control.Partition = runPartition
		} else if scriptSpecs.Control.Partition == "" {
			scriptSpecs.Control.Partition = scheduler.Partition()
		}
		// CLI --exclude-nodes > script directive > scheduler.exclude_nodes.
		if cmd.Flags().Changed("exclude-nodes") {
			if err := scheduler.ValidateNodeNames(runExcludeNodes); err != nil {
				return err
			}
			scriptSpecs.Control.ExcludeNodes = append([]string{}, runExcludeNodes...)
		}
		if err := useSubmitDir(originScriptPath, scriptSpecs); err != nil {
			return err
		}
	}

	// CLI -c/-m/-t/-g resource overrides (highest priority, only when spec is parsed)
	if jobKeepsResources() {
		warnResourceFlagsInJob(cmd.Flags())
	} else if scriptSpecs != nil && scriptSpecs.Spec == nil && scriptSpecs.HasDirectives {
		// The resource directives could not be fully parsed, so overrides cannot be applied; warn if the user specified any.
		if runCPU > 0 || runMem != "" || runTime != "" || runGPU != "" {
			utils.PrintWarning("Resource flags ignored: the script's directives could not be parsed")
		}
	} else if scriptSpecs != nil && scriptSpecs.Spec != nil {
		scriptSpecs.Spec.Override(resFlags.override(scriptSpecs.Spec))
	}

	// Conflict: --array flag + native scheduler array directive in the script
	if arraySpec != nil {
		if found := detectNativeArrayDirective(scriptSpecs); found != "" {
			ExitWithError("script contains native array directive %q; remove it and use --array flag instead", found)
		}
	}

	if err := checkSubmitFlag(cmd.Flags().Changed("no-submit")); err != nil {
		ExitWithError("%v", err)
	}

	// A dependency the scheduler cannot express is refused here, before any
	// overlay is built or job submitted for this run.
	if deps := runDependencies(); len(deps) > 0 && willSubmitRun(scriptSpecs) {
		if err := scheduler.CheckDependencies(scheduler.ActiveScheduler().GetType(), deps); err != nil {
			ExitWithError("%v", err)
		}
	}

	// Taken before the script's own container headers are applied, which change the same variables: the job reads those itself.
	containerFlags := submittedFlagArgs(cmd.Flags())

	// 2. Container headers + dependency check/install
	if err := applyScriptHeaders(contentScript); err != nil {
		return err
	}
	// A project's lock governs what this script mounts. Resolved before the dry
	// run so a preview reports the identities the run would actually mount, not
	// whatever currently answers to their names.
	projectRun, err := projectRunContext(cmd.Context(), contentScript, !runDryRun && !willSubmitRun(scriptSpecs))
	if err != nil {
		return err
	}

	// Dry run: print summary and exit without executing
	if runDryRun {
		printDryRunSummary(cmd.Context(), contentScript, originScriptPath, scriptSpecs, scriptArgs, arraySpec, projectRun)
		if reasons := dryRunRefusals(cmd.Context(), contentScript, scriptSpecs, arraySpec, projectRun); len(reasons) > 0 {
			return fmt.Errorf("the run would not start: %s", strings.Join(reasons, "; "))
		}
		return nil
	}

	if reasons := runRefusals(scriptSpecs, arraySpec); len(reasons) > 0 {
		ExitWithError("the run cannot start: %s", strings.Join(reasons, "; "))
	}
	var overlays []string
	if projectRun != nil {
		overlays = projectRun.Overlays
		// A project outside $HOME is otherwise unreachable from the container:
		// only the script's own directory is bound, and apptainer's automatic
		// binds cover $HOME and the working directory alone.
		runBindPaths = append(runBindPaths, projectRun.Root)
	} else if overlays, err = resolveDeps(cmd.Context(), contentScript, originScriptPath); err != nil {
		if errors.Is(err, errRunAborted) {
			exit(ExitCodeError)
		}
		return err
	}

	// Slots mount on top of every #DEP:, in the order they are declared. A submitted job repeats them by exact name.
	if !slotsPlanned {
		slots, slotErr = planSlots(cmd.Context(), contentScript, runSlots, projectRun != nil)
	}
	if slotErr != nil {
		if reportSlotError(slotErr) {
			exit(ExitCodeError)
		}
		return slotErr
	}
	noteUnpinnedSlots(slots)
	overlays = append(overlays, slots.Paths()...)
	containerFlags = append(containerFlags, slots.Flags()...)

	// After the dependencies, which may already supply a root, and after the
	// dry run, which must not build one. The script may be submitted to a node
	// that cannot build, so the default root is pinned here rather than
	// resolved there — unless the script's own overlays already name one.
	runBaseImage, err = ensureRootBaseImage(cmd.Context(), overlays)
	if err != nil {
		return err
	}

	// 3. In a job or a container the script runs in place, unless --submit asks for a new job
	if runsInPlace() {
		return runLocally(cmd.Context(), contentScript, overlays, scriptSpecs, scriptArgs)
	}

	// 4. Submit if scheduler specs present and scheduler available
	noScheduler := false
	if scheduler.Enabled() && scheduler.HasSchedulerSpecs(scriptSpecs) {
		sched := scheduler.ActiveScheduler()
		if sched == nil {
			noScheduler = true
		} else {
			return submitRunJob(cmd.Context(), sched, originScriptPath, contentScript, scriptSpecs, runDependencies(), containerFlags, scriptArgs, arraySpec)
		}
	}
	jobFlags := runStdout != "" || runStderr != "" || runTime != "" || runAccount != "" || runPartition != ""
	if note := localRunNote(noScheduler, jobFlags); note != "" {
		utils.PrintNote("%s", note)
	}
	return runLocally(cmd.Context(), contentScript, overlays, scriptSpecs, scriptArgs)
}

// localRunNote is the one line a local run prints, or "" when it has nothing to add.
//   - A script with no resources runs locally by definition and says nothing.
//   - A fallback for lack of a scheduler, and the flags that only a submitted job uses, share one line.
//   - -c, -m and -g change what a local run gets, so they are not in it.
func localRunNote(noScheduler, jobFlags bool) string {
	switch {
	case noScheduler && jobFlags:
		return "No scheduler: running locally, -o/-e/-t/-A/-p ignored"
	case noScheduler:
		return "No scheduler: running locally"
	case jobFlags:
		return "-o/-e/-t/-A/-p ignored: only a submitted job uses them"
	}
	return ""
}

// runDependencies returns the typed dependencies given by --afterok,
// --afternotok and --afterany.
func runDependencies() []scheduler.Dependency {
	var deps []scheduler.Dependency
	for _, flag := range []struct{ val, depType string }{
		{runAfterOK, scheduler.DependencyAfterOK},
		{runAfterNotOK, scheduler.DependencyAfterNotOK},
		{runAfterAny, scheduler.DependencyAfterAny},
	} {
		var ids []string
		for id := range strings.SplitSeq(flag.val, ":") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			deps = append(deps, scheduler.Dependency{Type: flag.depType, JobIDs: ids})
		}
	}
	return deps
}

// runInsideContainer is config.IsInsideContainer, replaceable by tests.
var runInsideContainer = config.IsInsideContainer

// runsInPlace reports whether this process runs the script itself instead of submitting it.
//   - A container does, since it has no scheduler.
//   - A job does, unless --submit asks for a new job.
func runsInPlace() bool {
	return runInsideContainer() || (scheduler.IsInsideJob() && !runSubmit)
}

// checkSubmitFlag refuses --submit where it cannot submit, so it never runs the script locally instead.
//   - noSubmitSet is whether --no-submit was typed. A config that turns submission off is the same refusal as no scheduler.
func checkSubmitFlag(noSubmitSet bool) error {
	switch {
	case !runSubmit:
		return nil
	case noSubmitSet:
		return errSubmitWithNoSubmit
	case !scheduler.Enabled() || scheduler.ActiveScheduler() == nil:
		return errSubmitNoScheduler
	}
	return nil
}

// submitDefaultSpecs gives a script that declares no resources the default job settings (scheduler.ncpu, scheduler.mem and scheduler.time), so --submit submits it as it would any job.
//   - A script that declares resources is returned as it is.
func submitDefaultSpecs(specs *scheduler.ScriptSpecs) *scheduler.ScriptSpecs {
	if scheduler.HasSchedulerSpecs(specs) {
		return specs
	}
	if specs == nil {
		specs = &scheduler.ScriptSpecs{}
	}
	rs := scheduler.GetSpecDefaults()
	rs.Nodes = 1
	specs.Spec = &rs
	specs.HasDirectives = true
	return specs
}

// submitThroughHost runs this `condatainer run --submit` on the host, through host-run, and exits with its exit code.
//   - The host runs the same command with the same flags, except --submit-on, which picks the node.
//   - Output and the job ID come back on the streams they were written to.
func submitThroughHost(cmd *cobra.Command, args []string) error {
	argv := append([]string{"condatainer", "run"}, flagArgv(cmd.Flags(), map[string]bool{"submit-on": true})...)
	return runOnHost(cmd.Context(), append(argv, args...), runSubmitOn, "--submit")
}

// flagArgv is the flags that were set on the command line, as arguments, except the ones in skip.
//   - A bool or a value is --name=value, and a list is one --name=value for each element.
func flagArgv(fs *pflag.FlagSet, skip map[string]bool) []string {
	var argv []string
	fs.Visit(func(fl *pflag.Flag) {
		if skip[fl.Name] {
			return
		}
		values := []string{fl.Value.String()}
		if list, ok := fl.Value.(pflag.SliceValue); ok {
			values = list.GetSlice()
		}
		for _, v := range values {
			argv = append(argv, "--"+fl.Name+"="+v)
		}
	})
	return argv
}

// willSubmitRun reports whether this run goes to a scheduler rather than running
// locally, which is when a dependency has anything to apply to.
func willSubmitRun(specs *scheduler.ScriptSpecs) bool {
	return scheduler.Enabled() && scheduler.HasSchedulerSpecs(specs) &&
		!runsInPlace() && scheduler.ActiveScheduler() != nil
}

// resourceFlags are the typed values of -c, -m, -t and -g. A flag that was not typed is zero.
type resourceFlags struct {
	cpus  int
	memMB int64
	time  time.Duration
	gpu   *scheduler.GpuSpec
}

// parseResourceFlags reads and checks the resource flags that were typed, whether or not they have an effect.
func parseResourceFlags(fs *pflag.FlagSet) (resourceFlags, error) {
	var f resourceFlags
	if fs.Changed("cpu") {
		if runCPU < 1 {
			return f, fmt.Errorf("--cpu %d: must be at least 1", runCPU)
		}
		f.cpus = runCPU
	}
	if runMem != "" {
		mb, err := utils.ParseMemoryMB(runMem)
		if err != nil {
			return f, fmt.Errorf("--mem %q: %v", runMem, err)
		}
		f.memMB = mb
	}
	if runTime != "" {
		d, err := utils.ParseWalltime(runTime)
		if err != nil {
			return f, fmt.Errorf("--time %q: %v", runTime, err)
		}
		f.time = d
	}
	if runGPU != "" {
		gpu, err := scheduler.ParseGPU(runGPU)
		if err != nil {
			return f, fmt.Errorf("--gpu %q: %v", runGPU, err)
		}
		f.gpu = gpu
	}
	return f, nil
}

// override is the flags as a resource spec to lay over script.
//   - -m is memory per rank. With one rank that is the memory of the node, which is set as it is.
//   - With more ranks it is per CPU, over the CPUs that -c gives, else the script's, else 1. That rounds up.
func (f resourceFlags) override(script *scheduler.ResourceSpec) *scheduler.ResourceSpec {
	rs := &scheduler.ResourceSpec{CpusPerTask: f.cpus, Time: f.time, Gpu: f.gpu}
	switch {
	case f.memMB == 0:
	case script.GetNtasks() == 1:
		rs.MemPerNodeMB = f.memMB
	default:
		cpus := int64(overrideCPUs(f.cpus, script.CpusPerTask))
		rs.MemPerCpuMB = (f.memMB + cpus - 1) / cpus
	}
	return rs
}

// jobKeepsResources reports whether this run is in a job, which keeps the resources it was given.
//   - -c, -m, -t and -g have no effect there. --submit starts a new job, which they apply to.
func jobKeepsResources() bool {
	return scheduler.IsInsideJob() && !runSubmit
}

// warnResourceFlagsInJob names the resource flags that were typed and have no effect in a job.
func warnResourceFlagsInJob(fs *pflag.FlagSet) {
	var typed []string
	for _, name := range []string{"cpu", "mem", "time", "gpu"} {
		if fs.Changed(name) {
			typed = append(typed, "--"+name)
		}
	}
	if len(typed) > 0 {
		utils.PrintWarning("Resource flags ignored in a job: %s", strings.Join(typed, ", "))
	}
}

// overrideCPUs is the CPU count that -m is divided over: -c, else the script's #NCPU, else 1.
func overrideCPUs(flag, script int) int {
	switch {
	case flag > 0:
		return flag
	case script > 0:
		return script
	}
	return 1
}

// useSubmitDir makes a job run where it was submitted from, and refuses a script that names another directory.
//   - A local run and a submitted job then start in the same place.
//   - The job script always names the directory, because PBS otherwise starts a job in $HOME.
func useSubmitDir(script string, specs *scheduler.ScriptSpecs) error {
	if specs.Control.WorkDir != "" {
		return fmt.Errorf("%s sets a working directory; remove it and run from that directory", script)
	}
	if cwd, err := os.Getwd(); err == nil {
		specs.Control.WorkDir = cwd
	}
	return nil
}

// flagResourceHeaders lists the resource headers that -c, -m, -t and -g already set.
func flagResourceHeaders() []string {
	var keys []string
	for header, set := range map[string]bool{"#NCPU": runCPU > 0, "#MEM": runMem != "", "#TIME": runTime != "", "#GPU": runGPU != ""} {
		if set {
			keys = append(keys, header)
		}
	}
	return keys
}

// resolveScriptAndSpecs tries to read scheduler specs and resolves the content script path.
//   - For HTCondor .sub files: specs.ScriptPath is the bash executable — returned as contentScript.
//   - For SLURM/PBS/LSF: contentScript == scriptPath.
//   - On failure: prints a warning and returns (scriptPath, nil).
func resolveScriptAndSpecs(scriptPath string, subjects scheduler.Subjects) (string, *scheduler.ScriptSpecs) {
	specs, err := scheduler.ReadScriptSpecsFor(scriptPath, subjects)
	if errors.Is(err, scheduler.ErrInvalidHeader) {
		ExitWithError("%v", err)
	}
	if err != nil {
		utils.PrintWarning("Failed to parse scheduler specs: %v", err)
		return scriptPath, nil
	}
	contentScript := scriptPath
	if specs != nil && specs.ScriptPath != "" && specs.ScriptPath != scriptPath {
		contentScript = specs.ScriptPath
	}
	if !filepath.IsAbs(contentScript) {
		if abs, err := filepath.Abs(contentScript); err == nil {
			contentScript = abs
		}
	}
	return contentScript, specs
}

// applyScriptHeaders adds the script's #BIND:, #WRITABLE: and #FAKEROOT: headers to the run options.
func applyScriptHeaders(scriptPath string) error {
	text, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %w", err)
	}
	utils.WarnStray(scriptPath, utils.StrayAnnotations(text, "#BIND", "#WRITABLE", "#FAKEROOT"))
	headers, err := container.ParseHeaders(utils.ScanAnnotations(text))
	if err != nil {
		return fmt.Errorf("%s: %w", scriptPath, err)
	}
	runBindPaths = append(headers.Binds, runBindPaths...) // before --bind, so an explicit flag wins
	runWritableImg = runWritableImg || headers.Writable
	runFakeroot = runFakeroot || headers.Fakeroot
	return nil
}

// runRefusals lists what stops a run once the script's headers are read, in the order the run checks it.
//   - The real run and the dry run both read it.
//   - An HTCondor job is one slot on one machine, so a submit of more than one rank is refused there.
func runRefusals(specs *scheduler.ScriptSpecs, arraySpec *scheduler.ArraySpec) []string {
	var reasons []string
	if scheduler.IsUnparsed(specs) {
		reasons = append(reasons, "the scheduler directives could not be fully parsed")
	}
	if arraySpec != nil && len(arraySpec.BlankLines) > 0 {
		reasons = append(reasons, fmt.Sprintf("the --array file has blank lines at %v", arraySpec.BlankLines))
	}
	if arraySpec != nil && !scheduler.IsUnparsed(specs) && !willSubmitRun(specs) {
		reasons = append(reasons, "--array needs a new job, but this run stays in place")
	}
	if reason := writableRefusal(specs, arraySpec); reason != "" {
		reasons = append(reasons, reason)
	}
	if ranks := getNtasks(specs); ranks > 1 && willSubmitRun(specs) {
		if scheduler.ActiveScheduler().GetType() == scheduler.SchedulerHTCondor {
			reasons = append(reasons, fmt.Sprintf("HTCondor runs no job with more than one rank (%d ranks)", ranks))
		} else if _, ok := detectMpi(); !ok {
			reasons = append(reasons, fmt.Sprintf("%d ranks need mpiexec, which is not on PATH", ranks))
		}
	}
	return reasons
}

// writableRefusal says why --writable cannot be used, or "" when it can.
//   - Array subjobs and ranks run at the same time, and a writable overlay is locked to one of them.
//   - A run in a job with more than one rank is one of those ranks.
func writableRefusal(specs *scheduler.ScriptSpecs, arraySpec *scheduler.ArraySpec) string {
	if !runWritableImg {
		return ""
	}
	switch ranks := getNtasks(specs); {
	case arraySpec != nil:
		return "--writable cannot be used with --array: subjobs run at the same time"
	case ranks > 1:
		return fmt.Sprintf("--writable cannot be used with more than one rank (%d ranks)", ranks)
	case runsInPlace() && scheduler.InMultiRankJob():
		return "--writable cannot be used in a job with more than one rank"
	}
	return ""
}

// dryRunRefusals lists what would stop the real run before it starts, in the
// order the run checks it: runRefusals, then the slots and the dependencies.
// A project run has already resolved its dependencies.
func dryRunRefusals(ctx context.Context, contentScript string, specs *scheduler.ScriptSpecs, arraySpec *scheduler.ArraySpec, projectRun *projectContext) []string {
	reasons := runRefusals(specs, arraySpec)
	if _, err := planSlots(ctx, contentScript, runSlots, projectRun != nil); err != nil && !errors.Is(err, fs.ErrNotExist) {
		var missing *errSlotMissing
		if errors.As(err, &missing) {
			reasons = append(reasons, "missing slot overlays: "+strings.Join(missing.Names, ", "))
		} else {
			reasons = append(reasons, err.Error())
		}
	}
	if projectRun != nil {
		return reasons
	}
	deps, err := utils.ScriptDependencies(contentScript)
	if err != nil {
		return append(reasons, fmt.Sprintf("its dependencies could not be parsed: %v", err))
	}
	resolved, _, err := scriptDeps(ctx, deps)
	if err != nil {
		return append(reasons, err.Error())
	}
	var missing []string
	for _, d := range resolved {
		if d.Missing() {
			missing = append(missing, d.Request)
		}
	}
	if len(missing) > 0 {
		reasons = append(reasons, "missing dependencies: "+strings.Join(missing, ", "))
	}
	return reasons
}

// resolveDeps parses #DEP dependencies, checks installed overlays, and returns resolved paths.
// Returns errRunAborted (message already printed) if any dependencies are missing.
func resolveDeps(ctx context.Context, contentScript, originScriptPath string) (overlays []string, err error) {
	deps, err := utils.ScriptDependencies(contentScript)
	if err != nil {
		utils.PrintError("Failed to parse dependencies: %v", err)
		return nil, errRunAborted
	}

	resolved, mounts, err := scriptDeps(ctx, deps)
	if err != nil {
		utils.PrintError("%v", err)
		return nil, errRunAborted
	}
	printMounts(mounts, false)

	var missingDeps, externalFiles []string
	for _, d := range resolved {
		if d.Missing() {
			missingDeps = append(missingDeps, d.Request)
			if d.IsFile() {
				externalFiles = append(externalFiles, d.Request)
			}
		}
	}
	if len(missingDeps) > 0 {
		utils.PrintMessage("Missing dependencies:")
		for _, md := range missingDeps {
			utils.PrintMessage("  - %s", md)
		}
		if len(externalFiles) > 0 {
			fileList := strings.Join(externalFiles, ", ")
			utils.PrintError("External overlay(s) %s not found - use `condatainer create --needed-by %s` to create them.", fileList, originScriptPath)
		} else {
			utils.PrintHint("Please run `condatainer create --needed-by %s` to install missing dependencies.", originScriptPath)
		}
		return nil, errRunAborted
	}

	overlays = make([]string, len(resolved))
	for i, d := range resolved {
		overlays[i] = d.Path
	}

	// Check .img availability, and a .sif root's own /bin/bash, before
	// submitting or running locally — a .sif here is always the exec root
	// (ensureAtMostOneSif refuses a second one), and failing now means before
	// a scheduler queue wait, not after one.
	for _, ol := range overlays {
		if utils.IsImg(ol) && utils.FileExists(ol) {
			if err := image.CheckAvailable(ol, runWritableImg); err != nil {
				return nil, err
			}
		}
		if utils.IsSif(ol) {
			if err := sif.RequireBash(ol); err != nil {
				return nil, err
			}
		}
	}

	return overlays, nil
}

// effectiveResourceSpec resolves the resources of a run: the config defaults, then the script's spec with the flags on it, then the allocation of the job it runs in.
//   - The spec of a script with no resources holds the defaults, so the flags apply to it.
//   - A run in place gets the allocation it runs in.
//   - A job with more than one rank is not read. The script's own spec stands.
//   - A submitted job is a new job, so the job that submits it does not apply.
func effectiveResourceSpec(specs *scheduler.ScriptSpecs) *scheduler.ResourceSpec {
	rs := scheduler.GetSpecDefaults()
	if specs != nil && specs.Spec != nil {
		rs = *specs.Spec
	}
	if runsInPlace() && !scheduler.InMultiRankJob() {
		rs.Override(scheduler.CurrentJobResources())
	}
	return &rs
}

// memoryGuessWarning is the warning for a run in a one-rank job whose memory the job does not report, or "".
//   - MEM is then the script's #MEM or the scheduler.mem setting, which may not be what the job has.
func memoryGuessWarning(specs *scheduler.ScriptSpecs) string {
	if specs == nil || specs.Spec == nil || !scheduler.IsInsideJob() || !runsInPlace() || scheduler.InMultiRankJob() {
		return ""
	}
	if job := scheduler.CurrentJobResources(); job != nil && (job.MemPerNodeMB > 0 || job.MemPerCpuMB > 0) {
		return ""
	}
	return "Job memory is unknown. MEM is set from the script and config."
}

// resourceEnvSettings is the resource variables a script run here gets.
//   - A job with more than one rank gets OMP_NUM_THREADS when it is unset, and nothing else.
//   - When the script's directives could not be parsed (Spec == nil) the live job's resources are used.
//   - In a job they are the job's size, set every time, so NCPU always equals CNT_NCPU. Outside a job the script and the flags decide.
//   - -c replaces an OMP_NUM_THREADS that is already set, except in a job, where -c has no effect.
func resourceEnvSettings(specs *scheduler.ScriptSpecs) []string {
	if scheduler.InMultiRankJob() {
		return scheduler.MultiRankEnvVars()
	}
	if specs == nil || specs.Spec == nil {
		return liveJobResourceEnvSettings()
	}
	return scheduler.RunEnvVars(effectiveResourceSpec(specs), runCPU > 0 && !scheduler.IsInsideJob())
}

// liveJobResourceEnvSettings returns the resource variables of the job this process runs in, for a run whose script directives did not parse.
// Returns nil when not in a job or when the scheduler gives no resource value.
func liveJobResourceEnvSettings() []string {
	jobRes := scheduler.CurrentJobResources()
	if jobRes == nil || (jobRes.CpusPerTask == 0 && jobRes.MemPerNodeMB == 0 && jobRes.MemPerCpuMB == 0) {
		return nil
	}
	return scheduler.RunEnvVars(jobRes, false)
}

// runLocally executes the script in the container with the given overlays.
func runLocally(ctx context.Context, contentScript string, overlays []string, specs *scheduler.ScriptSpecs, scriptArgs []string) error {
	usage.Configure(usage.KindRun, runUsage)
	usage.Worked()
	command, err := scriptCommand(contentScript, scriptArgs)
	if err != nil {
		ExitWithError("%v", err)
	}

	// Auto-bind the script's directory so it's accessible inside the container.
	// (Scheduler will copy the script to a temp location)
	scriptDir := filepath.Dir(contentScript)
	bindPaths := append(append([]string{scriptDir}, configBinds()...), runBindPaths...)

	rs := effectiveResourceSpec(specs)
	gpuRequested := rs.Gpu != nil && rs.Gpu.Count > 0
	if warning := memoryGuessWarning(specs); warning != "" {
		utils.PrintWarning("%s", warning)
	}

	options := execpkg.Options{
		Overlays:     overlays,
		Command:      command,
		WritableImg:  runWritableImg,
		EnvSettings:  append(resourceEnvSettings(specs), runEnvSettings...),
		BindPaths:    bindPaths,
		Fakeroot:     runFakeroot,
		BaseImage:    runBaseImage,
		HidePrompt:   true,
		GpuRequested: gpuRequested,
		HostRun:      hostrun.KindRun,
	}

	if err := execpkg.Run(ctx, options, execpkg.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		// Propagate exit code from container command
		if appErr, ok := err.(*apptainer.ApptainerError); ok {
			if code := appErr.ExitCode(); code >= 0 {
				exit(code)
			}
		}
		return err
	}
	return nil
}

// scriptCommand returns the command that runs a script inside the container.
//   - The interpreter and its argument come from the script's first line, as the kernel reads them. The executable bit is not read.
func scriptCommand(script string, args []string) ([]string, error) {
	text, err := os.ReadFile(script)
	if err != nil {
		return nil, fmt.Errorf("failed to read script: %w", err)
	}
	interpreter, argument, err := utils.ShebangCommand(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", script, err)
	}
	command := []string{interpreter}
	if argument != "" {
		command = append(command, argument)
	}
	return append(append(command, script), args...), nil
}

// printDryRunSummary prints what condatainer run would do without executing.
// printProjectDependencies lists what a project run will mount.
//
// Nothing is checked here: every path came from the lock, and resolution has
// already failed if any declaration could not be satisfied. baseImg is the
// caller's own resolution (see printDryRunSummary), not derived again here.
func printProjectDependencies(baseImg string, projectRun *projectContext) {
	check, cross := utils.StyleSuccess("✓"), utils.StyleError("✗")
	fmt.Printf("Project: %s\n", projectRun.Root)
	fmt.Printf("%s (%d locked):\n", utils.StyleTitle("Dependencies:"), len(projectRun.Overlays))
	if utils.FileExists(baseImg) {
		fmt.Printf("  Base:       %s %s\n", check, baseImg)
	} else {
		fmt.Printf("  Base:       %s %s %s\n", cross, baseImg, utils.StyleWarning("(not found)"))
	}
	for _, overlay := range projectRun.Overlays {
		fmt.Printf("    %s %s\n", check, overlay)
	}
}

func printDryRunSummary(ctx context.Context, contentScript, originScript string, specs *scheduler.ScriptSpecs, scriptArgs []string, arraySpec *scheduler.ArraySpec, projectRun *projectContext) {
	fmt.Printf("Dry run: %s\n", specs.ScriptPath)

	// Dependencies. A dry run reports the base rather than building it, so an
	// uninstalled one shows as the error the real run would hit. Standing in a
	// project this is its pinned root, not the configured default_distro —
	// previewRootBaseImage is ensureRootBaseImage's read-only counterpart.
	baseImg := runBaseImage
	if baseImg == "" {
		var overlays []string
		if projectRun != nil {
			overlays = projectRun.Overlays
		}
		switch resolved, err := previewRootBaseImage(ctx, overlays); {
		case err != nil:
			baseImg = "(" + err.Error() + ")"
		case resolved == "":
			baseImg = "(supplied by one of the mounted overlays)"
		default:
			baseImg = resolved
		}
	}

	deps, err := utils.ScriptDependencies(contentScript)
	var resolved []scriptDep
	if projectRun == nil && err == nil {
		resolved, _, err = scriptDeps(ctx, deps)
	}
	if projectRun != nil {
		printProjectDependencies(baseImg, projectRun)
	} else if err != nil {
		fmt.Printf("%s Could not read dependencies: %v\n", utils.StyleError("[ERR]"), err)
	} else {
		check := utils.StyleSuccess("✓")
		cross := utils.StyleError("✗")

		// First pass: collect results and compute max dep name width for alignment
		type depEntry struct {
			dep, path string
			ok        bool
		}
		entries := make([]depEntry, 0, len(resolved))
		for _, d := range resolved {
			entries = append(entries, depEntry{dep: d.Request, path: d.Path, ok: !d.Missing()})
		}

		found, missing := 0, 0
		if utils.FileExists(baseImg) {
			found++
		} else {
			missing++
		}
		for _, e := range entries {
			if e.ok {
				found++
			} else {
				missing++
			}
		}
		fmt.Printf("%s (%d found, %d missing):\n", utils.StyleTitle("Dependencies:"), found, missing)

		// Base image
		if utils.FileExists(baseImg) {
			fmt.Printf("  Base:       %s %s\n", check, baseImg)
		} else {
			fmt.Printf("  Base:       %s %s %s\n", cross, baseImg, utils.StyleWarning("(not found)"))
		}

		// External overlays (direct path): show under "External:" header; installed: group by folder
		var externals []string
		dirOrder := []string{}
		dirDeps := map[string][]string{}
		for _, e := range entries {
			if !e.ok {
				continue
			}
			if utils.IsOverlay(e.dep) || utils.IsSif(e.dep) {
				externals = append(externals, e.dep)
			} else {
				dir := filepath.Dir(e.path)
				if _, exists := dirDeps[dir]; !exists {
					dirOrder = append(dirOrder, dir)
				}
				dirDeps[dir] = append(dirDeps[dir], e.dep)
			}
		}
		if len(externals) > 0 {
			fmt.Printf("  %s\n", "External:")
			for _, dep := range externals {
				fmt.Printf("    %s %s\n", check, dep)
			}
		}
		for _, dir := range dirOrder {
			fmt.Printf("  %s\n", (dir + "/"))
			for _, dep := range dirDeps[dir] {
				fmt.Printf("    %s %s\n", check, dep)
			}
		}
		// Missing deps listed after found
		for _, e := range entries {
			if !e.ok {
				suffix := utils.StyleWarning("(not installed)")
				if utils.IsOverlay(e.dep) || utils.IsSif(e.dep) {
					suffix = utils.StyleWarning("(not found)")
				}
				fmt.Printf("  %s %s  %s\n", cross, e.dep, suffix)
			}
		}
	}

	printDryRunSlots(ctx, contentScript, projectRun != nil)

	// Scheduler specs
	if specs != nil && specs.Spec == nil && specs.HasDirectives {
		var hostType scheduler.SchedulerType
		if s := scheduler.ActiveScheduler(); s != nil {
			hostType = s.GetType()
		} else {
			hostType = scheduler.DetectType()
		}
		if hostType != scheduler.SchedulerUnknown && specs.ScriptType != hostType {
			fmt.Printf("Resource specs: %s\n",
				utils.StyleError(fmt.Sprintf("(not parsed — %s directives cannot be translated to %s)", specs.ScriptType, hostType)))
		} else {
			fmt.Printf("Resource specs: %s\n", utils.StyleWarning("(not parsed — directives could not be read)"))
		}
	} else if specs != nil && specs.Spec != nil {
		rs := effectiveResourceSpec(specs)
		fmt.Printf("%s\n", utils.StyleTitle("Resource specs:"))
		ntasks := getNtasks(specs)
		isMPI := ntasks > 1

		if isMPI {
			// MPI or Hybrid: show rank geometry first
			fmt.Printf("  MPI Ranks:  %d\n", ntasks)
			if rs.Nodes > 0 {
				fmt.Printf("  Nodes:      %d\n", rs.Nodes)
			}
			if rs.TasksPerNode > 0 {
				fmt.Printf("  Ranks/Node: %d\n", rs.TasksPerNode)
			}
			if rs.CpusPerTask > 1 {
				// Hybrid: also show per-rank thread count
				fmt.Printf("  CPU/Rank:   %d\n", rs.CpusPerTask)
			}
		} else {
			// Pure OpenMP / single-rank
			if rs.CpusPerTask > 0 {
				fmt.Printf("  CPU:        %d\n", rs.CpusPerTask)
			}
		}

		if memPerTaskMB := rs.GetMemPerTaskMB(); memPerTaskMB > 0 {
			memLabel := "Mem/Rank:  "
			if !isMPI {
				memLabel = "Mem:       "
			}
			fmt.Printf("  %s %s\n", memLabel, utils.FormatMemoryMB(memPerTaskMB))
			if warning := memoryGuessWarning(specs); warning != "" {
				utils.PrintWarning("%s", warning)
			}
		}
		// Only a submitted job has a time. A run neither enforces nor exports one.
		if rs.Time > 0 && willSubmitRun(specs) {
			fmt.Printf("  Time:       %s\n", utils.FormatTimeDetail(rs.Time))
		}
		if rs.Gpu != nil {
			if rs.Gpu.Type != "" {
				fmt.Printf("  GPU:        %s x%d\n", rs.Gpu.Type, rs.Gpu.Count)
			} else {
				fmt.Printf("  GPU:        %d\n", rs.Gpu.Count)
			}
		}
		if rs.Exclusive {
			fmt.Printf("  Exclusive:  yes\n")
		}
		if isMPI {
			mpiexecPath, ok := detectMpi()
			if !ok {
				fmt.Printf("  mpiexec:    %s\n", utils.StyleError("not found"))
			} else {
				fmt.Printf("  mpiexec:    %s\n", mpiexecPath)
			}
		}

		printDryRunConditions(specs.Conditions)

		fmt.Printf("%s\n", utils.StyleTitle("Job control:"))
		scriptBase := strings.TrimSuffix(filepath.Base(originScript), filepath.Ext(originScript))
		if specs.Control.JobName != "" {
			fmt.Printf("  Name:       %s\n", specs.Control.JobName)
		} else {
			fmt.Printf("  Name:       %s (default)\n", scriptBase)
		}
		if specs.Control.WorkDir != "" {
			fmt.Printf("  WorkDir:    %s\n", specs.Control.WorkDir)
		} else {
			fmt.Printf("  WorkDir:    . (default)\n")
		}
		if specs.Control.Account != "" {
			fmt.Printf("  Account:    %s\n", specs.Control.Account)
		}
		if specs.Control.Partition != "" {
			fmt.Printf("  Partition:  %s\n", specs.Control.Partition)
		}
		if nodes := specs.Control.ExcludedNodes(); len(nodes) > 0 {
			fmt.Printf("  Excluded:   %s\n", strings.Join(nodes, ","))
		}
		defaultOut := filepath.Join(submitDir(), scriptBase+"_<timestamp>.out")
		if specs.Control.Stdout != "" {
			fmt.Printf("  Stdout:     %s\n", specs.Control.AbsStdout())
		} else {
			fmt.Printf("  Stdout:     %s (default)\n", defaultOut)
		}
		if specs.Control.Stderr != "" {
			fmt.Printf("  Stderr:     %s\n", specs.Control.AbsStderr())
		} else if specs.Control.Stdout != "" {
			fmt.Printf("  Stderr:     %s (default)\n", specs.Control.AbsStdout())
		} else {
			fmt.Printf("  Stderr:     %s (default)\n", defaultOut)
		}

		if arraySpec != nil {
			arrayLine := fmt.Sprintf("  Array:      %d subjobs from %s", arraySpec.Count, arraySpec.InputFile)
			if arraySpec.Limit > 0 {
				arrayLine += fmt.Sprintf(" (max %d concurrent)", arraySpec.Limit)
			}
			fmt.Println(arrayLine)
			if len(arraySpec.BlankLines) > 0 {
				fmt.Printf("  %s blank lines at %v — remove before submitting\n",
					utils.StyleError("[ERR]"), arraySpec.BlankLines)
			}
		}
		if runAfterOK != "" {
			fmt.Printf("  AfterOK:     %s\n", runAfterOK)
		}
		if runAfterNotOK != "" {
			fmt.Printf("  AfterNotOK:  %s\n", runAfterNotOK)
		}
		if runAfterAny != "" {
			fmt.Printf("  AfterAny:    %s\n", runAfterAny)
		}

		// Email Notifications
		var events []string
		if specs.Control.EmailOnBegin {
			events = append(events, "BEGIN")
		}
		if specs.Control.EmailOnEnd {
			events = append(events, "END")
		}
		if specs.Control.EmailOnFail {
			events = append(events, "FAIL")
		}
		if len(events) > 0 {
			mailStr := strings.Join(events, ",")
			if specs.Control.MailUser != "" {
				mailStr += " to " + specs.Control.MailUser
			}
			fmt.Printf("  Mail:       %s\n", mailStr)
		}
	}

	// Container args
	hasContainerArgs := len(runEnvSettings) > 0 || len(runBindPaths) > 0 || runFakeroot || runWritableImg
	if hasContainerArgs {
		fmt.Printf("%s\n", utils.StyleTitle("Container args:"))
		for _, env := range runEnvSettings {
			fmt.Printf("  Env:        %s\n", env)
		}
		for _, bind := range runBindPaths {
			fmt.Printf("  Bind:       %s\n", bind)
		}
		if runFakeroot {
			fmt.Printf("  Fakeroot:   yes\n")
		}
		if runWritableImg {
			fmt.Printf("  Writable:   yes\n")
		}
	}

	// Script args (array args prepended, then fixed CLI args)
	hasScriptArgs := arraySpec != nil || len(scriptArgs) > 0
	if hasScriptArgs {
		fmt.Printf("%s\n", utils.StyleTitle("Script args:"))
		if arraySpec != nil {
			fileBase := filepath.Base(arraySpec.InputFile)
			maxLen := 0
			for _, token := range arraySpec.SampleArgs {
				if len(token) > maxLen {
					maxLen = len(token)
				}
			}
			for i, token := range arraySpec.SampleArgs {
				fmt.Printf("  %-*s  (arg%d from %s)\n", maxLen, token, i+1, fileBase)
			}
		}
		for i := 0; i < len(scriptArgs); i++ {
			arg := scriptArgs[i]
			fmt.Printf("  %s\n", arg)
		}
	}

	// Action
	if scheduler.IsInsideJob() && !runSubmit {
		fmt.Printf("Action: Would run locally (in job)\n")
	} else if runInsideContainer() {
		fmt.Printf("Action: Would run locally (in container)\n")
	} else if scheduler.IsUnparsed(specs) {
		fmt.Printf("Action: %s\n",
			"Would fail: the scheduler directives could not be fully parsed")
	} else if scheduler.Enabled() && scheduler.HasSchedulerSpecs(specs) {
		sched := scheduler.ActiveScheduler()
		if sched == nil {
			fmt.Printf("Action: Would run locally (scheduler not available)\n")
		} else {
			fmt.Printf("Action: Would submit to %s\n", sched.GetType())
		}
	} else {
		fmt.Printf("Action: Would run locally\n")
	}
}

// buildArraySpec reads and validates the array input file, returning an ArraySpec.
//   - All non-empty lines must shell-split into the same number of tokens.
//   - Returns nil when --array was not provided.
func buildArraySpec() (*scheduler.ArraySpec, error) {
	if runArray == "" {
		return nil, nil
	}
	absFile, err := filepath.Abs(runArray)
	if err != nil {
		return nil, fmt.Errorf("--array: cannot resolve path %q: %w", runArray, err)
	}
	data, err := os.ReadFile(absFile)
	if err != nil {
		return nil, fmt.Errorf("--array: cannot read input file %q: %w", absFile, err)
	}
	var sampleArgs []string
	expectedArgCount := -1
	count := 0
	var blankLines []int
	for lineNum, line := range strings.Split(strings.TrimRight(string(data), "\r\n"), "\n") {
		line = strings.TrimRight(line, "\r") // strip \r from Windows line endings
		if strings.TrimSpace(line) == "" {
			blankLines = append(blankLines, lineNum+1)
			continue
		}
		count++
		tokens := shellSplitLine(line)
		if expectedArgCount == -1 {
			expectedArgCount = len(tokens)
			sampleArgs = tokens
		} else if len(tokens) != expectedArgCount {
			return nil, fmt.Errorf("--array: line %d has %d arg(s) but line 1 has %d; all lines must have the same number of arguments",
				lineNum+1, len(tokens), expectedArgCount)
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("--array: input file %q is empty", absFile)
	}
	return &scheduler.ArraySpec{
		InputFile:  absFile,
		Count:      count,
		Limit:      runArrayLimit,
		ArgCount:   expectedArgCount,
		SampleArgs: sampleArgs,
		BlankLines: blankLines,
	}, nil
}

// getNtasks returns the total number of MPI ranks using the priority chain:
// JobResources > ScriptSpec > Defaults. (Skipped when the directives could not be parsed)
func getNtasks(specs *scheduler.ScriptSpecs) int {
	if specs == nil || specs.Spec == nil {
		return 1
	}
	return effectiveResourceSpec(specs).GetNtasks()
}

// detectMpi checks whether mpiexec is available in the current PATH.
// Returns the full path and true when found; empty string and false otherwise.
func detectMpi() (string, bool) {
	path, err := osexec.LookPath("mpiexec")
	if err != nil {
		return "", false
	}
	return path, true
}

// submittedFlagArgs is what a submitted run's command repeats: the flags typed, and the usage flag when record_usage names run.
func submittedFlagArgs(fs *pflag.FlagSet) []string {
	return append(forwardedFlagArgs(fs), usage.JobFlags(usage.KindRun, runUsage)...)
}

// forwardedFlagArgs returns the flags given on the command line that a submitted job must run with, as shell words.
//   - A submitted job runs `condatainer run` again on the compute node, so a flag left out of that command is lost there.
//   - Every flag that was set is forwarded, except the job flags, which the generated job script carries.
//   - --project is forwarded as the absolute project root, since the job starts in the directory the command was run from, which need not be in the project.
func forwardedFlagArgs(fs *pflag.FlagSet) []string {
	var args []string
	fs.VisitAll(func(fl *pflag.Flag) {
		if !fl.Changed || runJobFlagNames[fl.Name] {
			return
		}
		if fl.Name == "slot" || fl.Name == usage.Flag {
			return
		}
		if fl.Name == "project" {
			if projectRootFlag != "" {
				args = append(args, "--project="+shellQuote(projectRootFlag))
			}
			return
		}
		values := []string{fl.Value.String()}
		if list, ok := fl.Value.(pflag.SliceValue); ok {
			values = list.GetSlice()
		}
		for _, v := range values {
			args = append(args, "--"+fl.Name+"="+shellQuote(v))
		}
	})
	return args
}

// buildMpiRunCommand returns the shell command to embed in the job script.
//   - When ntasks > 1 it requires mpiexec in PATH and wraps the condatainer run invocation with the full mpiexec path so compute nodes don't need a matching PATH.
//   - Returns an error when ntasks > 1 but mpiexec cannot be found.
//   - The user is responsible for installing the same MPI version inside the container.
func buildMpiRunCommand(contentScript string, containerFlags, scriptArgs []string, specs *scheduler.ScriptSpecs, prependArrayArgs bool) (string, error) {
	runCmd := utils.SelfCommand() + " run"
	if len(containerFlags) > 0 {
		runCmd += " " + strings.Join(containerFlags, " ")
	}
	runCmd += " " + contentScript
	if prependArrayArgs {
		runCmd += " $ARRAY_ARGS"
	}
	if len(scriptArgs) > 0 {
		quoted := make([]string, len(scriptArgs))
		for i, a := range scriptArgs {
			quoted[i] = shellQuote(a)
		}
		runCmd += " " + strings.Join(quoted, " ")
	}
	if getNtasks(specs) <= 1 {
		return runCmd, nil
	}
	mpiexecPath, ok := detectMpi()
	if !ok {
		return "", fmt.Errorf("%d ranks need mpiexec, which is not on PATH", getNtasks(specs))
	}
	utils.PrintDebug("Detected mpiexec: %s", mpiexecPath)
	return fmt.Sprintf("%s -n %d %s", mpiexecPath, getNtasks(specs), runCmd), nil
}

// maxRunNameSuffix is the last -N a run's file name takes before the submit fails.
const maxRunNameSuffix = 99

// createRunJobScript writes a run's job script under base, else base-2, base-3, ..., whichever is free first.
//   - A name is taken by creating its script, so two runs of one script in the same second never share one.
//   - control is reset before each try, since writing a script sets its log paths from the name.
func createRunJobScript(create func(name string) (string, error), base string, control *scheduler.RuntimeConfig) (path, name string, err error) {
	saved := *control
	name = base
	for i := 2; ; i++ {
		*control = saved
		path, err = create(name)
		if !errors.Is(err, os.ErrExist) || i > maxRunNameSuffix {
			return path, name, err
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// shellQuote returns a single-quoted shell-safe version of s.
// Embedded single quotes are escaped as '\'.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellSplitLine splits a shell-like line into tokens.
//   - Single- and double-quoted strings are kept as one token (quotes stripped).
//   - Unquoted whitespace is the delimiter.
func shellSplitLine(line string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	for _, ch := range line {
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case (ch == ' ' || ch == '\t') && !inSingle && !inDouble:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(ch)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// detectNativeArrayDirective returns a non-empty string describing a native scheduler
// array directive found in specs, or "" if none. Used to detect conflicts when the
// --array CLI flag is also in use.
func detectNativeArrayDirective(specs *scheduler.ScriptSpecs) string {
	if specs == nil {
		return ""
	}
	for _, f := range specs.RemainingFlags {
		// SLURM: --array=N-M  --array N-M  -a N-M  -a=N-M
		if strings.HasPrefix(f, "--array") || strings.HasPrefix(f, "-a=") ||
			f == "-a" || strings.HasPrefix(f, "-a ") {
			return f
		}
		// PBS: -J N-M (PBS job-name flag is -N, not -J)
		if strings.HasPrefix(f, "-J ") || strings.HasPrefix(f, "-J\t") ||
			strings.HasPrefix(f, "-J=") {
			return f
		}
	}
	// LSF: -J name[N-M] — array range embedded in job name with brackets
	if strings.Contains(specs.Control.JobName, "[") {
		return fmt.Sprintf("-J %s", specs.Control.JobName)
	}
	return ""
}

// submitRunJob creates and submits a scheduler job to run the script.
// contentScript is the bash script containing the #DEP and container headers — for HTCondor this is the
// executable referenced in the .sub file, for other schedulers it equals scriptPath.
func submitRunJob(ctx context.Context, sched scheduler.Scheduler, originScriptPath, contentScript string, specs *scheduler.ScriptSpecs, deps []scheduler.Dependency, containerFlags, scriptArgs []string, arraySpec *scheduler.ArraySpec) error {
	// Capture separate-output intent before CreateScriptWithSpec overrides Stdout/Stderr to /dev/null
	arraySeparateOutput := arraySpec != nil && specs.Control.Stderr != "" && specs.Control.Stderr != specs.Control.Stdout

	// Determine log directory - use spec's Stdout path if set, otherwise the submit directory
	var logsDir string
	if specs.Control.Stdout != "" {
		logsDir = filepath.Dir(specs.Control.AbsStdout())
	} else {
		logsDir = submitDir()
	}

	// Ensure logs directory exists. MkdirAllShared so a group-shared logs dir (2775,
	// e.g. set via config for a lab-wide run) is group-readable/writable for reviewers.
	if err := utils.MkdirAllShared(logsDir); err != nil {
		return fmt.Errorf("failed to create logs directory %s: %w", logsDir, err)
	}

	// Build the condatainer run command pointing at the bash script.
	// When the user provided a .sub file, contentScript is the executable .sh — the submitted
	// job must re-invoke condatainer run on the bash script, not the submit file.
	runCommand, err := buildMpiRunCommand(contentScript, containerFlags, scriptArgs, specs, arraySpec != nil)
	if err != nil {
		utils.PrintError("%v", err)
		return err
	}

	// The proxy tunnels through the login node, which a job submitted from a job inherits. With none known, no proxy starts.
	if scheduler.ProxyPerJob(scheduler.ProxyRun) {
		specs.ProxyVia = scheduler.LoginNode()
	}

	// Generate names: short name for job, timestamped name for files
	baseName := filepath.Base(originScriptPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	timestamp := time.Now().Format("20060102_150405")
	fileBaseName := fmt.Sprintf("%s_%s", nameWithoutExt, timestamp)

	// Set job name if not already set in script (short name for squeue)
	if specs.Control.JobName == "" {
		specs.Control.JobName = nameWithoutExt
	}

	// Collect all dep IDs for JobSpec metadata (DepJobIDs is afterok-only build chain field)
	var allDepIDs []string
	for _, dep := range deps {
		allDepIDs = append(allDepIDs, dep.JobIDs...)
	}

	jobSpec := &scheduler.JobSpec{
		Command:    runCommand,
		Specs:      specs,
		DepJobIDs:  allDepIDs,
		Metadata:   map[string]string{},
		Array:      arraySpec,
		UniqueName: true,
	}

	// Create the job script in the same directory as logs, under a name no other run holds
	jobScriptPath, fileBaseName, err := createRunJobScript(func(name string) (string, error) {
		jobSpec.Name = name
		return sched.CreateScriptWithSpec(jobSpec, logsDir)
	}, fileBaseName, &specs.Control)
	if err != nil {
		return fmt.Errorf("failed to create job script: %w", err)
	}

	// Submit the job with typed dependencies
	jobID, err := sched.Submit(ctx, jobScriptPath, deps)
	if err != nil {
		return fmt.Errorf("failed to submit job: %w", err)
	}

	fmt.Fprintln(os.Stdout, jobID)
	if len(deps) > 0 {
		var depSummary []string
		for _, dep := range deps {
			depSummary = append(depSummary, fmt.Sprintf("%s(%s)", dep.Type, strings.Join(dep.JobIDs, ",")))
		}
		utils.PrintSuccess("Submitted %s job %s for %s (after: %s)", sched.GetType(), jobID, originScriptPath, strings.Join(depSummary, " "))
	} else {
		utils.PrintSuccess("Submitted %s job %s for %s", sched.GetType(), jobID, originScriptPath)
	}

	if arraySpec != nil {
		// Array jobs redirect output per-subjob inside the script; show glob pattern
		safeName := strings.ReplaceAll(fileBaseName, "/", "--")
		if arraySeparateOutput {
			utils.PrintMessage("Per-subjob stdout/err => %s", filepath.Join(logsDir, safeName+"_*.out/*.err"))
		} else {
			utils.PrintMessage("Per-subjob stdout&err => %s", filepath.Join(logsDir, safeName+"_*.log"))
		}
	} else {
		stdoutPath := jobSpec.Specs.Control.AbsStdout()
		stderrPath := jobSpec.Specs.Control.AbsStderr()
		if stdoutPath != "" {
			if stderrPath == "" || stdoutPath == stderrPath {
				utils.PrintMessage("Stdout & Stderr => %s", stdoutPath)
			} else {
				utils.PrintMessage("Stdout => %s", stdoutPath)
				utils.PrintMessage("Stderr => %s", stderrPath)
			}
		}
	}

	return nil
}

// submitDir is where a run job's logs go when -o is not given.
func submitDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
