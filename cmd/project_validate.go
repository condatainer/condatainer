package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/restore"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectValidateCmd() *cobra.Command {
	var jsonOutput, lockOnly, payload bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the lock is complete and consistent with the scripts",
		Long: `Check that the lock is complete and consistent with the scripts.

- Every declaration in the scripts has a pin.
- Every pin is declared by a script, or is manual.
- Every pin points at a recorded overlay.
- Every pin has an overlay for each locked architecture that its recipe builds.
- Every recorded overlay still matches its keys.
- Every dependency resolves to another recorded overlay.
- Every pin is installed on this machine.

--payload needs squashfuse and unshare.`,
		Example: `  condatainer project validate  # Lock, scripts and installed overlays
  condatainer project validate --lock-only`,
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
			scanned, err := lock.Scan(root, lock.ScanOptions{})
			if err != nil {
				return err
			}
			_, problems := verifyLock(root, current)
			needPin := requestsNeedingPin(current, scanned)

			// An installed check is only meaningful over a lock that verifies.
			var notInstalled []string
			if !lockOnly && len(problems) == 0 {
				notInstalled = installedProblems(cmd.Context(), root, current, payload)
			}

			total := len(problems) + len(scanned.Findings) + len(needPin) + len(notInstalled) + len(lock.StalePins(current, scanned))
			if missingBaseProblem(current) != "" {
				total++
			}
			if jsonOutput {
				if err := writeValidation(cmd, root, current, problems, needPin, scanned, notInstalled); err != nil {
					return err
				}
				// The exit status is the answer; the format only changes how it
				// is explained.
				if total > 0 {
					return fmt.Errorf("project is not valid: %d problem(s)", total)
				}
				return nil
			}
			for _, problem := range projectProblems(current, scanned, problems) {
				utils.PrintError("%s", problem)
			}
			for _, problem := range notInstalled {
				utils.PrintError("%s", problem)
			}
			printSymlinks(scanned.SymlinkedScripts, scanned.SymlinkedDirs)
			if total > 0 {
				utils.PrintHint("Run `condatainer project pin <request> <identity>` to choose an overlay for each unpinned request.")
				return fmt.Errorf("project is not valid: %d problem(s)", total)
			}
			utils.PrintSuccess("Project is valid: %d pin(s), %d script(s)",
				len(current.Pins), len(scanned.Scripts))
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &jsonOutput)
	cmd.Flags().BoolVar(&lockOnly, "lock-only", false, "Check the lock and scripts only; no installed overlays needed (for CI)")
	cmd.Flags().BoolVar(&payload, "payload", false,
		"Also check installed file contents (reads the whole overlays)")
	cmd.MarkFlagsMutuallyExclusive("lock-only", "payload")
	return cmd
}

// installedProblems reports each pin that is not installed here, as an artifact
// that agrees with the lock under its match mode, and with payload each installed one
// whose files do not match its recorded payload key. A build dependency that
// only a rebuild would need is not a problem.
func installedProblems(ctx context.Context, root string, l *lock.Lock, payload bool) []string {
	plan := restore.Compute(root, l, restore.Options{})
	problems := append([]string(nil), plan.Problems...)
	for _, step := range plan.Steps {
		if !step.Direct {
			continue
		}
		if step.Action != restore.ActionAdopt {
			problems = append(problems, fmt.Sprintf("%s is not installed here (restore would %s it)", step.Name, step.Action))
			continue
		}
		if !payload || step.Path == "" {
			continue
		}
		if err := freeze.VerifyPayload(ctx, step.Path); err != nil && !errors.Is(err, freeze.ErrNoPayloadKey) {
			problems = append(problems, fmt.Sprintf("%s: %v", step.Name, err))
		}
	}
	return problems
}

func writeValidation(cmd *cobra.Command, root string, l *lock.Lock, problems []lock.Problem, needPin []lock.Request, scanned *lock.ScanResult, notInstalled []string) error {
	return report.Write(cmd.OutOrStdout(), validationDoc(root, l, problems, needPin, scanned, notInstalled))
}

// validationDoc is the verdict of `project validate`.
// A problem of the lock names its pin, else its artifact; the installed problems are plain messages.
func validationDoc(root string, l *lock.Lock, problems []lock.Problem, needPin []lock.Request, scanned *lock.ScanResult, notInstalled []string) *report.ProjectValidation {
	doc := &report.ProjectValidation{Root: root, Scripts: report.List[string](scanned.Scripts),
		Findings: findings(scanned.Findings), Unpinned: report.List[report.Declaration]{},
		SymlinkedScripts: scanned.SymlinkedScripts, SymlinkedDirs: scanned.SymlinkedDirs,
		Problems: report.List[report.Problem]{}}
	if doc.Scripts == nil {
		doc.Scripts = report.List[string]{}
	}
	for _, p := range problems {
		name := p.Request
		if name == "" {
			name = p.Artifact
		}
		doc.Problems = append(doc.Problems, report.Problem{Name: name, Message: p.Reason})
	}
	if reason := missingBaseProblem(l); reason != "" {
		doc.Problems = append(doc.Problems, report.Problem{Message: reason})
	}
	doc.Problems = append(doc.Problems, problemList(notInstalled)...)
	for _, key := range lock.StalePins(l, scanned) {
		doc.Problems = append(doc.Problems, report.Problem{Name: key, Message: stalePinProblem(key)})
	}
	for _, request := range needPin {
		doc.Unpinned = append(doc.Unpinned, declaration(request, "", true))
	}
	doc.Valid = len(doc.Problems) == 0 && len(doc.Findings) == 0 && len(doc.Unpinned) == 0
	return doc
}
