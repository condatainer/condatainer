package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

// projectDir is the --project flag, shared by every subcommand.
var projectDir string

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Pin, restore and publish a project's overlays",
	Long: `Pin, restore and publish the overlays a project's scripts depend on.

- A project is a folder holding a cnt-lock/ directory.
- Locking pins every #DEP: in its scripts to one exact overlay.
- The same scripts then run the same way on another machine.

Every overlay has two keys:
- identity:     one exact build.
- equivalence:  builds that differ only in ways that should not matter.

A project accepts equivalent builds by default. See ` + "`project set-match`" + `.`,
}

// pinReport is one pin as the listing renders it. Name and Identity come from
// the vendored records rather than from the key, which addresses an artifact
// without describing it.
type pinReport struct {
	Request string
	// Artifacts maps each architecture the pin covers to its vendored directory.
	Artifacts map[string]string
	Name      string
	// Identity is this machine's artifact, else the first architecture's.
	Identity string
	// Entry is the vendored record, nil when it did not verify.
	Entry  *lock.Entry
	Manual bool
	// SlotOnly is set for a pin only a #SLOT: names.
	SlotOnly bool
	// Problem is why the artifact could not be read, empty when it verified.
	Problem string
}

// restoreJobRules says, for every restore flag, whether a submitted rebuild repeats it.
//   - A new restore flag must be listed here, and TestEveryRestoreFlagHasAJobRule fails until it is.
//   - A repeated flag also goes into restoreCommand (internal/project/restore), which builds the job's command from Options.
var restoreJobRules = map[string]string{
	"no-prebuilt": jobRepeats, "keep-build-deps": jobRepeats, "replace": jobRepeats,
	"only":      "the command builder sets it to the artifact the job is for",
	"dry-run":   "a plan is shown and nothing is submitted",
	"no-submit": "the job is the submission",
	"usage":     jobRepeats,
	"json":      "output format of this run",
}

// stepAt identifies one restore step: a path pin is one step per path.
type stepAt struct{ artifact, destination string }

func init() {
	rootCmd.AddCommand(projectCmd)
	projectCmd.PersistentFlags().StringVar(&projectDir, "project", "", "Project root (default: the current directory)")
	projectCmd.AddCommand(newProjectLockCmd(), newProjectStatusCmd(), newProjectPinCmd(), newProjectUnpinCmd(),
		newProjectIgnoreCmd(),
		newProjectSelectDistroCmd(), newProjectSelectMatchCmd(), newProjectListCmd(), newProjectValidateCmd(),
		newProjectRestoreCmd(), newProjectRegistryCmd())
}

// projectRoot resolves the root a command acts on — the current directory, or
// the one --project names — and reports it once.
func projectRoot(announce bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := lock.RootFor(projectDir, cwd)
	if errors.Is(err, lock.ErrNoProject) {
		return "", fmt.Errorf("%w\na project is a directory containing %s; create one with %s, or name it with %s",
			err, "cnt-lock/", "condatainer project lock", "--project DIR")
	}
	if err != nil {
		return "", err
	}
	if announce {
		utils.PrintMessage("Project: %s", root)
	}
	return root, nil
}

// printSymlinks reports what the scan skipped as symlinks: a script is a warning, since its #DEP: is never pinned, and a directory is a note.
func printSymlinks(scripts, dirs []string) {
	if len(scripts) > 0 {
		utils.PrintWarning("Symlink not scanned: %s", strings.Join(scripts, ", "))
	}
	if len(dirs) > 0 {
		utils.PrintMessage("Symlinked folders skipped: %s", strings.Join(dirs, ", "))
	}
}

// printUnpinnedHelperOverlays prints the overlays used by helpers but not
// pinned, under a heading apart from the unpinned #DEP: list.
func printUnpinnedHelperOverlays(names []string) {
	if len(names) == 0 {
		return
	}
	utils.PrintMessage("Overlays used by helpers but not pinned:")
	for _, name := range names {
		utils.PrintMessage("  %s", name)
	}
	utils.PrintHint("Run `condatainer project pin <name> <identity>` to pin one, if it should be locked.")
}

// printManualPinUsage prints, next to the manual pins report, which
// helper(s) are recorded using each one — a fact, never a suggestion to
// unpin. Shared between `project status` and `project lock`'s report.
func printManualPinUsage(usage map[string][]string) {
	if len(usage) == 0 {
		return
	}
	keys := make([]string, 0, len(usage))
	for key := range usage {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	utils.PrintMessage("Manual pin usage:")
	for _, key := range keys {
		names := usage[key]
		if len(names) == 0 {
			utils.PrintMessage("  %s: no recorded helper usage", key)
			continue
		}
		utils.PrintMessage("  %s: used by %s", key, strings.Join(names, ", "))
	}
}

func pinReports(l *lock.Lock, verified *lock.Verified) []pinReport {
	out := make([]pinReport, 0, len(l.Pins))
	for _, request := range l.Requests() {
		pin := l.Pins[request]
		report := pinReport{Request: request, Artifacts: pin.Artifacts, Manual: pin.Manual}
		shown, ok := pin.For(meta.NativeArch())
		if !ok {
			shown = pin.Paths()[0]
		}
		if entry, ok := verified.Entries[shown]; ok {
			report.Name, report.Identity, report.Entry = entry.Manifest.Name, entry.Identity.Digest(), entry
		}
		for _, artifact := range pin.Paths() {
			if _, ok := verified.Entries[artifact]; !ok {
				report.Problem = artifact + " is missing or does not verify"
			}
		}
		out = append(out, report)
	}
	return out
}

// printUnpublishedNote prints the same note `project lock`'s old
// noteUnpublished did, once, when pinned left any frozen-environment
// artifact without a recorded remote — the one case a pin cannot be rebuilt
// from what it vendored, so a registry copy is the only route another
// checkout has to obtain it.
func printUnpublishedNote(l *lock.Lock, pinned []*lock.Pinned) {
	if len(project.UnpublishedFrozenEnv(l, pinned)) == 0 {
		return
	}
	utils.PrintNote("A snapshot cannot be rebuilt; run `condatainer project registry push` so another checkout can restore this project")
}

// audienceOrDefault renders what an unrecorded audience means, rather than
// printing nothing and leaving the reader to guess which way it falls.
func audienceOrDefault(v string) string {
	if strings.TrimSpace(v) == "" {
		return "public (default)"
	}
	return v
}

// verifyLock is the lock's own problems, plus the one a machine outside the
// lock's architectures adds: the pins have no artifact for it yet.
func verifyLock(root string, l *lock.Lock) (*lock.Verified, []lock.Problem) {
	verified, problems := lock.Verify(root, l)
	for _, gap := range verified.Select(l, meta.NativeArch()).Gaps {
		if gap.Request == "" {
			problems = append(problems, lock.Problem{Gap: true, Reason: gap.Reason})
		}
	}
	return verified, problems
}

// projectProblems is everything `project validate` reports, as lines: a lock
// inconsistent with what it vendors, a declaration nothing can pin, and a
// declaration with no pin. Push and validate share it so a project cannot be
// publishable and invalid at once.
func projectProblems(l *lock.Lock, scanned *lock.ScanResult, problems []lock.Problem) []string {
	out := problemStrings(problems)
	if reason := missingBaseProblem(l); reason != "" {
		out = append(out, reason)
	}
	for _, finding := range scanned.Findings {
		out = append(out, fmt.Sprintf("%s:%d: %s", finding.Script, finding.Line, finding.Reason))
	}
	for _, request := range requestsNeedingPin(l, scanned) {
		out = append(out, fmt.Sprintf("%s is declared but not pinned (%s)",
			request.Key, strings.Join(request.Scripts, ", ")))
	}
	for _, key := range lock.StalePins(l, scanned) {
		out = append(out, stalePinProblem(key))
	}
	return out
}

// stalePinProblem is the line for a pin no script declares any more.
func stalePinProblem(key string) string {
	return key + " is pinned but no script declares it; run `condatainer project lock` to drop it, or `condatainer project pin` to keep it as a manual pin"
}

// missingBaseProblem is the one line `project validate`/`push` add when no
// root is pinned, or "" when one is. `project lock` pins one when there is
// none, so the only way to reach this is a hand-edited lock.
func missingBaseProblem(l *lock.Lock) string {
	if _, ok := l.Pins[lock.BaseKey]; ok {
		return ""
	}
	return "no root is pinned; run `condatainer project lock`"
}

func problemStrings(problems []lock.Problem) []string {
	out := make([]string, 0, len(problems))
	for _, problem := range problems {
		out = append(out, problem.String())
	}
	return out
}

// requestsNeedingPin reports declarations with no pin, without mutating
// the lock the way Reconcile does. An unpinnable request is not needPin:
// there is nowhere for restore to put an answer, and it is already a scan
// finding rather than a missing pin.
func requestsNeedingPin(l *lock.Lock, scanned *lock.ScanResult) []lock.Request {
	var out []lock.Request
	for _, request := range scanned.Requests {
		if !request.Kind.Pinnable() {
			continue
		}
		if _, _, ok := lock.MatchPin(l, request); !ok {
			out = append(out, request)
		}
	}
	return out
}

// problemList is plain-text problems as the problems of a document.
func problemList(messages []string) report.List[report.Problem] {
	out := report.List[report.Problem]{}
	for _, message := range messages {
		out = append(out, report.Problem{Message: message})
	}
	return out
}

func declaration(request lock.Request, artifact string, pinnable bool) report.Declaration {
	return report.Declaration{Request: request.Key, Kind: string(request.Kind),
		Scripts: request.Scripts, Pinnable: pinnable, Artifact: artifact}
}

func findings(found []lock.Finding) report.List[report.Finding] {
	out := report.List[report.Finding]{}
	for _, f := range found {
		out = append(out, report.Finding{Script: f.Script, Line: f.Line, Text: f.Text, Reason: f.Reason})
	}
	return out
}
