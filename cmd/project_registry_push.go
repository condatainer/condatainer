package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/publish"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectRegistryPushCmd() *cobra.Command {
	var all, dryRun, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Publish pinned overlays and record where they landed",
		Long: `Publish the locked overlays to the project's registry.

- A later restore downloads them instead of rebuilding.
- The project must pass ` + "`condatainer project validate`" + `, or nothing is uploaded.
- Only built overlays are published. Run ` + "`project restore`" + ` first for the rest.
- Pins that a recipe source already serves are skipped.`,
		Example: `  condatainer project registry push --dry-run
  condatainer project registry push
  condatainer project registry push --all`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			l, err := lock.Load(root)
			if err != nil {
				return err
			}
			verified, problems := verifyLock(root, l)
			scanned, err := lock.Scan(root, lock.ScanOptions{})
			if err != nil {
				return err
			}
			// Push holds a project to what `project validate` asks, not to the
			// lock's internal consistency alone: what is published has to be what
			// the checkout describes, and a declaration the lock cannot reproduce
			// is missing from it. A dry run reports these and still shows the plan.
			invalid := projectProblems(l, scanned, problems)
			if len(invalid) > 0 && !dryRun {
				if jsonOutput {
					if err := writePushRefused(cmd, root, invalid); err != nil {
						return err
					}
				}
				return fmt.Errorf("the project is not valid, so nothing was published:\n  %s",
					strings.Join(invalid, "\n  "))
			}

			opts := publish.Options{All: all}
			plan, err := publish.Build(cmd.Context(), root, l, verified, opts)
			if err != nil {
				return err
			}
			cat, err := config.OpenCatalog(cmd.Context())
			if err != nil {
				logging.FromContext(cmd.Context()).Debug("no catalog, so upstream copies were not checked", "err", err)
			}
			publish.Refine(cmd.Context(), root, plan, cat, opts)

			if dryRun {
				plan.Problems = append(plan.Problems, invalid...)
				return reportPushPlan(cmd, root, plan, verified, jsonOutput)
			}
			if !plan.Complete() {
				if jsonOutput {
					if err := writePush(cmd, root, plan, verified, nil); err != nil {
						return err
					}
				}
				return fmt.Errorf("nothing was published:\n  %s", strings.Join(plan.Problems, "\n  "))
			}
			run, runErr := publish.Run(cmd.Context(), root, plan, opts)
			if err := reportPush(cmd, root, plan, verified, run, jsonOutput); err != nil {
				return err
			}
			return runErr
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Publish every pin, including ones a recipe source already serves")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be published, without uploading")
	jsonFlag(cmd.Flags(), &jsonOutput)
	return cmd
}

// pushLabel is step's `path:` pin key when it has one, else its name — every
// frozen environment shares the manifest name "env", so the name alone can't
// tell two pinned copies apart, but the key each was declared under always
// can.
func pushLabel(step publish.Step) string {
	if step.Destination == "" {
		return utils.StyleName(step.Name)
	}
	return utils.StyleName(lock.PathPrefix + step.Destination)
}

// reportPushPlan prints a --dry-run publish plan and fails, in either format,
// when the project could not be published as it stands.
func reportPushPlan(cmd *cobra.Command, root string, plan *publish.Plan, verified *lock.Verified, jsonOutput bool) error {
	if jsonOutput {
		if err := writePush(cmd, root, plan, verified, nil); err != nil {
			return err
		}
	} else {
		printPushPlan(plan)
	}
	if !plan.Complete() {
		return fmt.Errorf("this project cannot be published as it stands")
	}
	return nil
}

// printPushPlan prints a publish plan as text.
func printPushPlan(plan *publish.Plan) {
	utils.PrintMessage("Publishing to %s (%s)", plan.Repository, audienceOrDefault(plan.Audience))
	if plan.Source != "" {
		utils.PrintMessage("  packages link to %s", plan.Source)
	}
	for _, step := range plan.Steps {
		switch step.Disposition {
		case publish.Upload:
			utils.PrintMessage("  upload   %s", pushLabel(step))
		case publish.Present:
			utils.PrintMessage("  present  %s  already published", pushLabel(step))
		case publish.Served:
			utils.PrintMessage("  upstream %s  %s", pushLabel(step), step.Remote.Repository)
		case publish.Refused:
			utils.PrintWarning("  refused  %s  %s", pushLabel(step), step.Reason)
		}
		// Reported, never judged: no allowlist of channels could be maintained
		// honestly, and interpreting a few hundred licence strings is what
		// #LICENSE: exists not to do. The operator sees what went into the solve.
		if len(step.Channels) > 0 {
			utils.PrintMessage("           channels: %s", strings.Join(step.Channels, ", "))
		}
	}
	for _, name := range plan.Ambiguous {
		utils.PrintWarning("  two pins are named %s, so neither takes the plain tag", name)
	}
	utils.PrintMessage("%d to upload", plan.Uploads())
	for _, problem := range plan.Problems {
		utils.PrintError("%s", problem)
	}
}

func reportPush(cmd *cobra.Command, root string, plan *publish.Plan, verified *lock.Verified, report *publish.Report, jsonOutput bool) error {
	if jsonOutput {
		return writePush(cmd, root, plan, verified, report)
	}
	for _, step := range report.Published {
		utils.PrintMessage("  %-8s %s", step.Disposition, pushLabel(step))
	}
	for _, name := range report.AmbiguousNames {
		utils.PrintWarning("  two pins are named %s, so neither takes the plain tag", name)
	}
	for _, failure := range report.Failures {
		utils.PrintError("  %s", failure)
	}
	if len(report.Failures) == 0 {
		utils.PrintSuccess("Published %d overlay(s) to %s", len(report.Published), report.Repository)
	}
	return nil
}

// writePush prints the pushes document of a publish plan, or of the run that followed it.
func writePush(cmd *cobra.Command, root string, plan *publish.Plan, verified *lock.Verified, run *publish.Report) error {
	return report.Write(cmd.OutOrStdout(), pushDoc(root, plan, verified, run))
}

// writePushRefused prints the document of a push that was refused before it was planned.
func writePushRefused(cmd *cobra.Command, root string, problems []string) error {
	return report.Write(cmd.OutOrStdout(), &report.ProjectPushes{Root: root,
		AmbiguousNames: report.List[string]{}, Pushes: report.List[report.ProjectPush]{}, Problems: problemList(problems)})
}

// pushDoc is a publish plan as the pushes document, and with run the outcome of each step.
//   - A refused step is refused in a preview too: nothing is attempted for it.
//   - A step the run never reached is skipped.
//   - A copy that is already there or served elsewhere is recorded in the lock and not uploaded, so it is done or skipped.
func pushDoc(root string, plan *publish.Plan, verified *lock.Verified, run *publish.Report) *report.ProjectPushes {
	doc := &report.ProjectPushes{Root: root, Repository: plan.Repository, Audience: plan.Audience, Source: plan.Source,
		AmbiguousNames: report.List[string](plan.Ambiguous), Pushes: report.List[report.ProjectPush]{},
		Problems: problemList(plan.Problems)}
	if doc.AmbiguousNames == nil {
		doc.AmbiguousNames = report.List[string]{}
	}

	published := map[stepAt]publish.Step{}
	failed := map[stepAt]publish.Failure{}
	if run != nil {
		for _, s := range run.Published {
			published[stepAt{s.Artifact, s.Destination}] = s
		}
		for _, f := range run.Failures {
			failed[stepAt{f.Step.Artifact, f.Step.Destination}] = f
		}
	}

	for _, s := range plan.Steps {
		id := stepAt{s.Artifact, s.Destination}
		push := report.ProjectPush{
			Push: report.Push{Overlay: report.Overlay{Name: s.Name}, Path: s.Path, Repository: plan.Repository,
				Tags: s.Tags, Audience: plan.Audience, Existing: s.Disposition == publish.Present},
			Artifact: s.Artifact, Destination: s.Destination, Channels: s.Channels,
		}
		if entry, ok := verified.Entries[s.Artifact]; ok {
			push.Type, push.Arch = entry.Manifest.Type, entry.Manifest.Platform.Arch
			push.Identity, push.Equiv = entry.Identity, entry.Equiv
		}
		if info, err := os.Stat(s.Path); s.Path != "" && err == nil {
			push.Size = info.Size()
		}
		if s.Remote != nil {
			remote := report.Remote{Repository: s.Remote.Repository, ManifestDigest: s.Remote.ManifestDigest}
			if s.Disposition == publish.Served {
				push.Served = &remote
			} else {
				push.Digest = remote.ManifestDigest
			}
		}

		switch done, f := published[id], failed[id]; {
		case s.Disposition == publish.Refused:
			push.Outcome, push.Error = report.OutcomeRefused, s.Reason
		case done.Artifact != "":
			push.Path = done.Path
			if done.Remote != nil {
				push.Digest = done.Remote.ManifestDigest
			}
			push.Outcome = report.OutcomeDone
			if s.Disposition == publish.Served {
				push.Outcome = report.OutcomeSkipped
			}
		case f.Step.Artifact != "":
			push.Outcome, push.Error = report.OutcomeFailed, f.Reason
		case run != nil:
			push.Outcome = report.OutcomeSkipped
		}
		doc.Pushes = append(doc.Pushes, push)
	}
	return doc
}
