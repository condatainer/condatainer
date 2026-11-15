package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/orchestrate"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectLockCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Create or update the lock from #DEP: declarations",
		Long: `Pin the overlay named by each #DEP: in the project's scripts.

- A script is a file that starts with #!/bin/bash or #!/bin/env <interpreter>.
- Creates cnt-lock/ if needed, and drops pins that no script declares any more.
- It pins this architecture and keeps the others. Run it on each to cover all.
- Skips dot directories and what .gitignore and ` + "`project ignore`" + ` list.
- Never follows a symlink.
- Fails if a declared overlay is not installed.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRootOrInit(!jsonOutput)
			if err != nil {
				return err
			}
			result, err := orchestrate.Lock(cmd.Context(), root, orchestrate.LockOptions{})
			if err != nil {
				return err
			}
			return reportLockState(cmd, result, jsonOutput)
		},
	}
	jsonFlag(cmd.Flags(), &jsonOutput)
	return cmd
}

// projectRootOrInit resolves the root like projectRoot, but treats a directory with no cnt-lock/ as one to create.
//   - Only `project lock` uses it: scanning for declarations is what makes a directory a project.
//   - Every other subcommand acts on existing pins, and would create an empty project from a mistyped path.
func projectRootOrInit(announce bool) (string, error) {
	root, err := projectRoot(false)
	if err != nil && !errors.Is(err, lock.ErrNoProject) {
		return "", err
	}
	created := err != nil
	if created {
		if root, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	if announce {
		if created {
			utils.PrintMessage("New project: %s", root)
		} else {
			utils.PrintMessage("Project: %s", root)
		}
	}
	return root, nil
}

// pinAddress is where a request went, written as the store writes an address, name@identity.
// A pin whose records cannot be read shows its vendored directory instead.
func pinAddress(report pinReport, pin lock.PinEntry) string {
	if report.Name == "" || report.Identity == "" {
		return pinTarget(pin)
	}
	return report.Name + "@" + identityPrefix(report.Identity)
}

// pinTarget is the artifact a pin has for this machine, for a line that shows
// where a request went.
func pinTarget(pin lock.PinEntry) string {
	if artifact, ok := pin.For(meta.NativeArch()); ok {
		return artifact
	}
	return utils.StyleWarning("no " + meta.NativeArch() + " artifact")
}

// reportLockState prints what a reconcile left behind and fails while anything
// is needPin, so a partial lock is published but never reported as complete.
func reportLockState(cmd *cobra.Command, result *orchestrate.LockResult, jsonOutput bool) error {
	root, l, scanned, pinned, failed := result.Root, result.Current, result.Scanned, result.Pinned, result.Failed
	if jsonOutput {
		if err := writeLock(cmd, result); err != nil {
			return err
		}
		return pinFailure(failed)
	}

	verified, _ := lock.Verify(root, l)
	reports := map[string]pinReport{}
	for _, report := range pinReports(l, verified) {
		reports[report.Request] = report
	}
	if pin, ok := l.Pins[lock.BaseKey]; ok {
		utils.PrintMessage("  %s → %s", utils.StyleName("base"), pinAddress(reports[lock.BaseKey], pin))
	}
	for _, request := range scanned.Requests {
		switch key, pin, ok := lock.MatchPin(l, request); {
		case ok:
			utils.PrintMessage("  %s → %s", utils.StyleName(key), pinAddress(reports[key], pin))
		case !request.Kind.Pinnable():
			utils.PrintMessage("  %s → %s", utils.StyleName(request.Key), utils.StyleWarning("cannot be pinned"))
		}
	}
	// Reported after the map, because an alternative only makes sense once the
	// reader can see which one was taken.
	alternatives := false
	for _, entry := range pinned {
		for _, other := range entry.Others {
			alternatives = true
			utils.PrintMessage("    also installed: %s %s",
				other.Identity.Digest(), other.Path)
		}
	}
	if alternatives {
		utils.PrintHint("Pin one of those instead with `condatainer project pin <request> <identity>`.")
	}
	scriptsOf := make(map[string][]string, len(scanned.Requests))
	for _, request := range scanned.Requests {
		scriptsOf[request.Key] = request.Scripts
	}
	for _, entry := range pinned {
		if entry.Shadowed != "" {
			shadowed, pinnedPath := strings.TrimPrefix(entry.Shadowed, lock.PathPrefix), strings.TrimPrefix(entry.Request, lock.PathPrefix)
			utils.PrintWarning("%s declares %s, which matches both %s and %s; pinned %s (relative to the script)",
				strings.Join(scriptsOf[entry.Request], ", "), shadowed, pinnedPath, shadowed, pinnedPath)
		}
	}
	for _, finding := range scanned.Findings {
		utils.PrintWarning("%s:%d: %s", finding.Script, finding.Line, finding.Reason)
	}
	for _, reason := range result.NotBuiltHere {
		utils.PrintWarning("%s", reason)
	}
	printUnpublishedNote(l, pinned)
	printUnpinnedHelperOverlays(result.UnpinnedHelperOverlays)
	printManualPinUsage(result.ManualPinUsage)
	printSymlinks(scanned.SymlinkedScripts, scanned.SymlinkedDirs)
	if len(failed) == 0 {
		utils.PrintSuccess("Every declaration is pinned (%d).", len(l.Pins))
		return nil
	}
	for _, err := range failed {
		utils.PrintError("%v", err)
	}
	return pinFailure(failed)
}

// pinFailure is the exit status: what could be pinned already was and is
// published, so this says the project is incomplete, never that nothing ran.
func pinFailure(failed []error) error {
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%d declaration(s) could not be pinned", len(failed))
}

func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}

func writeLock(cmd *cobra.Command, result *orchestrate.LockResult) error {
	return report.Write(cmd.OutOrStdout(), lockDoc(result))
}

// lockDoc is what `project lock` left behind.
// A declaration the lock could not pin has no artifact, and the errors go to problems.
func lockDoc(result *orchestrate.LockResult) *report.ProjectLock {
	l, scanned := result.Current, result.Scanned
	doc := &report.ProjectLock{Root: result.Root, Requests: report.List[report.Declaration]{},
		Findings: findings(scanned.Findings), UnpinnedHelperOverlays: result.UnpinnedHelperOverlays,
		ManualPinUsage: result.ManualPinUsage, SymlinkedScripts: scanned.SymlinkedScripts,
		SymlinkedDirs: scanned.SymlinkedDirs, Problems: problemList(errorStrings(result.Failed))}
	if pin, ok := l.Pins[lock.BaseKey]; ok {
		doc.Base, _ = pin.For(meta.NativeArch())
	}
	for _, request := range scanned.Requests {
		var artifact string
		if _, pin, ok := lock.MatchPin(l, request); ok {
			artifact, _ = pin.For(meta.NativeArch())
		}
		doc.Requests = append(doc.Requests, declaration(request, artifact, request.Kind.Pinnable()))
	}
	for _, reason := range result.NotBuiltHere {
		doc.Problems = append(doc.Problems, report.Problem{Message: reason})
	}
	return doc
}
