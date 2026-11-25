package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/restore"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/runtime/usage"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectRestoreCmd() *cobra.Command {
	var (
		jsonOutput    bool
		dryRun        bool
		noPrebuilt    bool
		keepBuildDeps bool
		replace       bool
		only          string
		usageFlag     bool
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Make every locked overlay available on this machine",
		Long: `Make every locked overlay available, by reusing, downloading, or rebuilding it.

- A named pin is installed under its name. A path pin goes to its own path.
- An overlay is downloaded only from a registry the lock records, else rebuilt.
- A rebuild with scheduler directives is submitted as a job.
- A path that already holds a different file is refused unless --replace.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRoot(!jsonOutput)
			if err != nil {
				return err
			}
			current, err := lock.Load(root)
			if err != nil {
				return err
			}
			opts := restore.Options{
				SkipPrebuilt: noPrebuilt, KeepBuildDeps: keepBuildDeps,
				Replace: replace, Only: only, SubmitJobs: scheduler.Enabled(), Usage: usageFlag,
			}

			if dryRun {
				return reportPlan(cmd, current, restore.Compute(root, current, opts), jsonOutput)
			}
			usage.Configure(usage.KindCreate, usageFlag)
			run, runErr := restore.Run(cmd.Context(), root, current, opts)
			if err := reportRestore(cmd, current, run, jsonOutput); err != nil {
				return err
			}
			if errors.Is(runErr, restore.ErrJobsSubmitted) {
				utils.PrintNote("%d scheduler job(s) submitted. exiting with code %d",
					len(run.Submitted), ExitCodeJobsSubmitted)
				exit(ExitCodeJobsSubmitted)
			}
			return runErr
		},
	}
	jsonFlag(cmd.Flags(), &jsonOutput)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would happen, without doing it")
	cmd.Flags().BoolVar(&noPrebuilt, "no-prebuilt", false,
		"Rebuild missing overlays instead of downloading it (snapshot still downloaded; refused for one whose tools run on another architecture)")
	cmd.Flags().BoolVar(&keepBuildDeps, "keep-build-deps", false,
		"Keep the build dependencies of a rebuild")
	settings.AddSwitch(cmd.Flags(), "scheduler.submit_job", "no-submit", "false", settings.Usage("Disable job submission (rebuild locally)"))
	cmd.Flags().BoolVar(&usageFlag, usage.Flag, false, "Print the wall time, CPU time and peak memory the restore used")
	cmd.Flags().BoolVar(&replace, "replace", false,
		"Overwrite a project path holding something the lock does not name")
	// Set by the job a submitted rebuild runs, so it produces exactly what it
	// was sent for. Nothing a person types.
	cmd.Flags().StringVar(&only, "only", "", "Restore one recorded overlay and its dependencies")
	_ = cmd.Flags().MarkHidden("only")
	return cmd
}

// reportPlan prints a --dry-run plan and fails if it could not run.
func reportPlan(cmd *cobra.Command, l *lock.Lock, plan *restore.Plan, jsonOutput bool) error {
	if jsonOutput {
		if err := writeRestore(cmd, l, plan, nil); err != nil {
			return err
		}
	} else {
		for _, step := range plan.Steps {
			line := fmt.Sprintf("  %-11s %s → %s", step.Action, utils.StyleName(step.Name), planDestination(step))
			if !step.Found.Empty() {
				line += utils.StyleWarning(fmt.Sprintf(" (equivalent, not %s)", identityPrefix(step.Identity.Digest())))
			}
			utils.PrintMessage("%s", line)
			if step.Replaces != "" {
				utils.PrintWarning("    replaces %s already there", identityPrefix(step.Replaces))
			}
		}
		for _, problem := range plan.Problems {
			utils.PrintError("%s", problem)
		}
		if plan.Complete() {
			utils.PrintSuccess("%d overlay(s) to acquire, %d already available.",
				plan.Work(), len(plan.Steps)-plan.Work())
		}
	}
	if !plan.Complete() {
		return fmt.Errorf("restore would not complete: %d problem(s)", len(plan.Problems))
	}
	return nil
}

// reportRestore prints what a restore did. The run's own error is the exit
// status; this only explains it.
func reportRestore(cmd *cobra.Command, l *lock.Lock, report *restore.Report, jsonOutput bool) error {
	if jsonOutput {
		return writeRestore(cmd, l, report.Plan, report)
	}
	for _, result := range report.Results {
		line := fmt.Sprintf("  %-8s %s → %s", result.Outcome, utils.StyleName(result.Name), result.Path)
		if result.Transient {
			line += " (build dep, discarded)"
		}
		if !result.Found.Empty() {
			line += utils.StyleWarning(equivalentNote(result))
		}
		utils.PrintMessage("%s", line)
		if result.PayloadDrift {
			utils.PrintWarning("%s rebuilt with the locked identity but different files: its recipe does not produce the same output twice",
				result.Name)
		}
	}
	for _, problem := range report.Problems {
		utils.PrintError("%s", problem)
	}
	for _, failure := range report.Failures {
		utils.PrintError("%s: %s", failure.Name, failure.Reason)
		for _, diff := range failure.Diffs {
			utils.PrintMessage("      %s", diff)
		}
	}
	// Blocked artifacts follow the failures and name the one cause, so a broken
	// dependency reads as one problem rather than as one per artifact above it.
	for _, blocked := range report.Blocked {
		utils.PrintWarning("%s: not attempted, %s failed", blocked.Name, blocked.Cause)
	}
	for _, submitted := range report.Submitted {
		verb := "submitted as"
		if submitted.Queued {
			verb = "already queued as"
		}
		line := fmt.Sprintf("  %-8s %s %s job %s", "pending", utils.StyleName(submitted.Name), verb, submitted.JobID)
		if len(submitted.DependsOn) > 0 {
			line += fmt.Sprintf(" (after %s)", strings.Join(submitted.DependsOn, ", "))
		}
		utils.PrintMessage("%s", line)
	}
	if report.Complete() {
		utils.PrintSuccess("Project restored: %d overlay(s).", len(report.Results))
	}
	return nil
}

// equivalentNote says an equivalent artifact stands in, and which inputs it was
// built from differently. Names only: the digests are in --json, and a line per
// input would bury a restore of many artifacts.
func equivalentNote(result restore.Result) string {
	note := fmt.Sprintf(" (equivalent, not %s", identityPrefix(result.Identity.Digest()))
	if fields := diffFields(result.Diffs); len(fields) > 0 {
		note += "; differs: " + strings.Join(fields, ", ")
	}
	return note + ")"
}

// diffFields names the inputs in compare's "field: want -> got" lines. The field
// itself holds colons (src:gtf, ph:gencode_version), so the split is on the
// colon-and-space that ends it.
func diffFields(diffs []string) []string {
	var out []string
	for _, diff := range diffs {
		field, _, _ := strings.Cut(diff, ": ")
		out = append(out, field)
	}
	return out
}

// planDestination says where a step's result will land, which differs by what
// asked for it: a project path, an images root, or nowhere at all.
func planDestination(step restore.Step) string {
	switch {
	case step.Destination != "":
		return step.Destination
	case !step.Direct:
		return "build dep, discarded after"
	default:
		return "store or flat"
	}
}

// writeRestore prints the steps document of a restore plan, or of the run that followed it.
func writeRestore(cmd *cobra.Command, l *lock.Lock, plan *restore.Plan, run *restore.Report) error {
	return report.Write(cmd.OutOrStdout(), restoreDoc(plan.Root, l, plan, run))
}

// restoreDoc is a restore as the steps document: the plan, and with run the outcome of each step.
//   - A step the run never reached has outcome skipped, and no outcome at all when planning stopped the run.
//   - A step with no recorded result was built inside the job of the step that needs it.
func restoreDoc(root string, l *lock.Lock, plan *restore.Plan, run *restore.Report) *report.ProjectSteps {
	doc := &report.ProjectSteps{Root: plan.Root, Match: string(plan.Match),
		Steps: report.List[report.ProjectStep]{}, Problems: problemList(plan.Problems)}
	verified, _ := lock.Verify(root, l)

	var results = map[stepAt]restore.Result{}
	var failures = map[stepAt]restore.Failure{}
	var blocked = map[stepAt]restore.Blocked{}
	var submitted = map[stepAt]restore.Submitted{}
	if run != nil {
		doc.Problems = problemList(run.Problems)
		for _, r := range run.Results {
			results[stepAt{r.Artifact, r.Destination}] = r
		}
		for _, f := range run.Failures {
			failures[stepAt{f.Artifact, f.Destination}] = f
		}
		for _, b := range run.Blocked {
			blocked[stepAt{b.Artifact, b.Destination}] = b
		}
		for _, s := range run.Submitted {
			submitted[stepAt{s.Artifact, s.Destination}] = s
		}
	}
	ran := run != nil && plan.Complete()

	for _, s := range plan.Steps {
		id := stepAt{s.Artifact, s.Destination}
		step := restoreStep(s, verified)
		switch r, f, b, j := results[id], failures[id], blocked[id], submitted[id]; {
		case r.Artifact != "":
			step.Outcome = report.OutcomeDone
			step.Action = restoreAction(r.Outcome)
			step.Found, step.Diffs, step.PayloadDrift = r.Found, r.Diffs, r.PayloadDrift
			step.Transient = r.Transient
			step.Location = report.Location{}
			if !r.Transient {
				step.Location = plannedLocation(r.Path, true)
				step.Layout = r.Layout
			}
			if r.Outcome == restore.OutcomeBuilt {
				step.Runs = report.RunsLocal
			}
		case f.Artifact != "":
			step.Outcome, step.Error, step.Diffs, step.Rejected = report.OutcomeFailed, f.Reason, f.Diffs, f.Rejected
		case b.Artifact != "":
			step.Outcome, step.Cause = report.OutcomeSkipped, b.Cause
			step.Reason = fmt.Sprintf("not attempted, %s failed", b.Cause)
		case j.Artifact != "":
			step.Outcome, step.JobID, step.Queued, step.WaitsFor = report.OutcomeSubmitted, j.JobID, j.Queued, j.DependsOn
			step.Runs = report.RunsJob
		case ran:
			step.Outcome = report.OutcomeSkipped
			step.Reason = "not reached"
			if s.Action == restore.ActionBuild && !s.Direct && len(run.Submitted) > 0 {
				step.Reason = "built by the job of the step that needs it"
			}
		}
		doc.Steps = append(doc.Steps, step)
	}
	return doc
}

// restoreStep is one planned step, before any outcome.
func restoreStep(s restore.Step, verified *lock.Verified) report.ProjectStep {
	step := report.ProjectStep{
		Step: report.Step{
			Overlay:       report.Overlay{Name: s.Name, Arch: s.Arch},
			Action:        restoreAction(s.Action),
			Identity:      s.Identity,
			DependsOn:     s.DependsOn,
			RequiresInput: s.RequiresInput,
			Location:      plannedLocation(s.Path, s.Path != ""),
		},
		Artifact: s.Artifact, Requests: s.Requests, Direct: s.Direct,
		Destination: s.Destination, Replaces: s.Replaces, Found: s.Found,
		StoreOnly: s.StoreOnly, Unbuildable: s.Unbuildable,
	}
	step.Layout = s.Layout
	if entry, ok := verified.Entries[s.Artifact]; ok {
		step.Type, step.Equiv = entry.Manifest.Type, entry.Equiv
	}
	for _, remote := range s.Remotes {
		step.Remotes = append(step.Remotes, report.Remote{Repository: remote.Repository, ManifestDigest: remote.ManifestDigest})
	}
	return step
}

// restoreAction names how a step makes its overlay available, from the plan or from what the run did.
func restoreAction[T ~string](action T) report.Action {
	switch string(action) {
	case string(restore.ActionFetch), string(restore.OutcomeFetched):
		return report.ActionFetch
	case string(restore.ActionBuild), string(restore.OutcomeBuilt):
		return report.ActionBuild
	case string(restore.ActionUnavailable):
		return report.ActionUnavailable
	}
	return report.ActionAdopt
}
