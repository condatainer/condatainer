package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/runtime/usage"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// createTyped maps each name a build was asked for to what the user typed, so
// the plan can show where an expanded name came from and which lines the user
// did not ask for.
var createTyped = map[string]string{}

// createNewer maps an installed name a bare or partial name resolved to the newer version the recipe sources offer.
var createNewer = map[string]string{}

// newerPending is set when the hint about a newer version waits for a build that has not finished.
var newerPending bool

// planShown is set when the caller has printed the plan, so a build it starts prints none of its own.
var planShown bool

// createPlanDir is the images directory a build installs into. A plan line
// whose file is elsewhere is shown by its path.
var createPlanDir string

// typedName records that name was asked for, as typed.
func typedName(name, typed string) {
	createTyped[name] = typed
}

// reportCreatePlan ends a dry run: the plan as text, or the steps document with
// --json. It builds, pulls and submits nothing.
func reportCreatePlan(steps []build.PlannedStep) {
	if !createJSON {
		printPlan(os.Stdout, steps, true)
		return
	}
	doc := &report.Steps{Steps: make(report.List[report.Step], 0, len(steps))}
	dependents := dependentsOf(steps)
	for _, step := range steps {
		out := reportStep(step)
		out.Dependents = dependents[step.Name]
		doc.Steps = append(doc.Steps, out)
	}
	if err := report.Write(os.Stdout, doc); err != nil {
		ExitWithError("%v", err)
	}
}

// showPlan prints the plan of a run that is about to build.
func showPlan(steps []build.PlannedStep) {
	printPlan(os.Stdout, steps, false)
}

// printPlan writes the plan to w unless -q is set. An installed name is a line
// above the block, and a plan with nothing left to build has no block.
func printPlan(w io.Writer, steps []build.PlannedStep, dryRun bool) {
	if utils.QuietMode {
		return
	}
	writePlan(w, steps, planContext{typed: createTyped, newer: createNewer, dir: createPlanDir, update: createUpdate}, dryRun)
}

// planContext is what a plan shows beyond the steps.
//   - typed holds each name that was asked for, mapped to what was typed.
//   - newer maps an installed name to a newer version on offer.
//   - dir is the images directory. A file there is shown by name, any other by its path.
//   - from maps a file's path to the source that builds it.
type planContext struct {
	typed  map[string]string
	newer  map[string]string
	dir    string
	update bool
	from   map[string]string
}

// writePlan lays out the steps for w.
//   - The hint about a newer version follows the block. When a local build comes next it waits until the build has finished.
func writePlan(w io.Writer, steps []build.PlannedStep, pc planContext, dryRun bool) {
	installed, block, newer := planLines(steps, pc)
	for _, line := range installed {
		fmt.Fprintln(w, line)
	}
	if len(installed) > 0 && len(block) > 0 {
		fmt.Fprintln(w)
	}
	for _, line := range block {
		fmt.Fprintln(w, line)
	}
	if newer {
		if dryRun || len(block) == 0 || slices.ContainsFunc(steps, func(s build.PlannedStep) bool { return s.InJob }) {
			printNewerHint()
		} else {
			newerPending = true
		}
	}
	if dryRun {
		fmt.Fprintln(w, "Dry run: nothing was built, pulled or submitted.")
	}
}

// planRow is one overlay of the plan block.
type planRow struct {
	name, action string
	notes        []string
}

// planLines returns the installed lines and the plan block for steps, and whether an installed line says a newer version exists.
//   - An installed step has a line, and no row. It says what was typed when that differs, and the newer version when there is one.
//   - A file in dir is shown by its name, any other by its path.
//   - A name that was typed has no "for" note. One that was expanded says what was typed.
func planLines(steps []build.PlannedStep, pc planContext) (installed, block []string, newer bool) {
	dependents := dependentsOf(steps)
	var rows []planRow
	byName := false
	for _, s := range steps {
		if s.Installed {
			installed = append(installed, installedLine(s.Name, pc))
			newer = newer || pc.newer[s.Name] != ""
			continue
		}
		row := planRow{name: s.Name, action: planAction(s, pc.update && utils.FileExists(s.Path))}
		inDir := pc.dir != "" && filepath.Dir(s.Path) == pc.dir
		byName = byName || inDir
		if !inDir {
			row.name = planPath(s.Path)
			if filepath.Base(s.Path) != image.EncodeArtifactName(s.Name)+filepath.Ext(s.Path) {
				row.notes = append(row.notes, "named "+s.Name)
			}
		}
		if from, ok := pc.typed[s.Name]; ok {
			if from != s.Name {
				row.notes = append(row.notes, "typed "+from)
			}
		} else if deps := dependents[s.Name]; len(deps) > 0 {
			row.notes = append(row.notes, forNote(deps))
		}
		if src := pc.from[s.Path]; src != "" {
			row.notes = append(row.notes, "from "+src)
		}
		if s.RequiresInput {
			row.notes = append(row.notes, "requires input")
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return installed, nil, newer
	}
	return installed, append([]string{planHeader(len(rows), byName, pc.dir)}, planTable(rows)...), newer
}

// dependentsOf maps each step to the steps that wait for it. An installed step waits for nothing.
func dependentsOf(steps []build.PlannedStep) map[string][]string {
	dependents := map[string][]string{}
	for _, s := range steps {
		if s.Installed {
			continue
		}
		for _, d := range s.DependsOn {
			dependents[d] = append(dependents[d], s.Name)
		}
	}
	return dependents
}

// installedLine says an overlay is already installed, as what was typed when that differs, and with the newer version on offer.
func installedLine(name string, pc planContext) string {
	line := name + " is already installed"
	if from := pc.typed[name]; from != "" && from != name {
		line = from + " is already installed as " + name
	}
	if v := pc.newer[name]; v != "" {
		line += " (newer: " + v + ")"
	}
	return line
}

// printNewerHint says how to install the newer version an installed line named.
func printNewerHint() {
	newerPending = false
	utils.PrintHint("a newer version exists; --latest installs it")
}

// planHeader is the count line, with the images directory when a row is shown by name.
func planHeader(n int, byName bool, dir string) string {
	noun := "overlays"
	if n == 1 {
		noun = "overlay"
	}
	head := fmt.Sprintf("%s %d %s", utils.StyleTitle("Plan:"), n, noun)
	if byName {
		head += fmt.Sprintf(" into %s (%s layer)", dir, config.ClassifyDataDir(dir))
	}
	return head
}

// planTable aligns the rows in columns: name, action, notes.
func planTable(rows []planRow) []string {
	nameW, actionW := 0, 0
	for _, r := range rows {
		nameW = max(nameW, len(r.name))
		if len(r.notes) > 0 {
			actionW = max(actionW, len(r.action))
		}
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		line := "  " + r.name
		if len(r.notes) == 0 {
			line += strings.Repeat(" ", 3+nameW-len(r.name)) + r.action
		} else {
			line += strings.Repeat(" ", 3+nameW-len(r.name)) + r.action +
				strings.Repeat(" ", 4+actionW-len(r.action)) + utils.StyleDim(strings.Join(r.notes, "  "))
		}
		lines = append(lines, line)
	}
	return lines
}

// planAction is what is done to one overlay: pulled from a registry host, or built here or by a job, and rebuilt when it replaces an installed one.
func planAction(s build.PlannedStep, rebuild bool) string {
	if s.Pull {
		host, _, _ := strings.Cut(s.Endpoint, "/")
		return "pull from " + host
	}
	verb := "build"
	if rebuild {
		verb = "rebuild"
	}
	if s.InJob {
		return verb + " by job"
	}
	return verb + " locally"
}

// forNote names the first overlay that needs this one and counts the rest.
func forNote(dependents []string) string {
	if len(dependents) == 1 {
		return "for " + dependents[0]
	}
	return fmt.Sprintf("for %s +%d more", dependents[0], len(dependents)-1)
}

// planPath is path relative to the working directory when it lies under it, else absolute.
// A file directly in the working directory starts with ./ so it reads as a path, not a name.
func planPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	if !strings.Contains(rel, string(filepath.Separator)) {
		return "." + string(filepath.Separator) + rel
	}
	return rel
}

// printBuilt ends a real run that built, pulled or rebuilt overlays.
func printBuilt(built int) {
	switch {
	case built == 1:
		utils.PrintSuccess("1 overlay installed")
	case built > 1:
		utils.PrintSuccess("%d overlays installed", built)
	}
	if newerPending {
		printNewerHint()
	}
}

// builtCount is how many of the steps a run will build, pull or rebuild.
func builtCount(steps []build.PlannedStep) int {
	n := 0
	for _, s := range steps {
		if !s.Installed {
			n++
		}
	}
	return n
}

// buildObject plans one build that runs by itself, outside a graph, and builds it unless this is a dry run.
func buildObject(ctx context.Context, bo *build.BuildObject) {
	steps := []build.PlannedStep{bo.PlannedStep(ctx)}
	if createDryRun {
		reportCreatePlan(steps)
		return
	}
	showPlan(steps)
	usage.Worked()
	if err := bo.Build(ctx, false); err != nil {
		ExitWithError("Build failed: %v", err)
	}
	printBuilt(builtCount(steps))
}

// jobsOf is how many scheduler jobs a graph submitted.
func jobsOf(graph *build.BuildGraph) int {
	if graph == nil || !scheduler.Enabled() {
		return 0
	}
	return len(graph.GetJobIDs())
}

// finishBuild ends a run that built through a graph: the closing line when every build ran here, else the exit code of submitted jobs.
func finishBuild(graph *build.BuildGraph, built int) {
	if jobsOf(graph) == 0 {
		printBuilt(built)
	}
	ExitIfJobsSubmitted(graph)
}

// installedStep is an overlay create found already installed under name.
func installedStep(name, path string) build.PlannedStep {
	step := build.PlannedStep{Name: name, Installed: true, Path: path}
	if rt, err := meta.ReadRuntime(path); err == nil {
		step.Type, step.Arch = rt.Type, rt.Platform.Arch
	}
	return step
}

// reportStep is a planned step as the steps document writes it.
func reportStep(s build.PlannedStep) report.Step {
	step := report.Step{
		Overlay:       report.Overlay{Name: s.Name, Type: s.Type, Arch: s.Arch},
		Equiv:         s.Equiv,
		DependsOn:     s.DependsOn,
		RequiresInput: s.RequiresInput,
		Location:      plannedLocation(s.Path, s.Installed),
	}
	for _, d := range s.Deps {
		step.Deps = append(step.Deps, report.Dep{
			Overlay: report.Overlay{Name: d.Name, Type: d.Type}, Role: d.Role, Installed: d.Installed,
			Identity: d.Identity, Equiv: d.Equiv, Location: plannedLocation(d.Path, d.Installed),
		})
	}
	switch {
	case s.Installed:
		step.Action = report.ActionAdopt
	case s.Pull:
		step.Action = report.ActionFetch
		step.Endpoint, step.Digest, step.Identity = s.Endpoint, s.Digest, s.Identity
	default:
		step.Action = report.ActionBuild
		step.Runs = report.RunsLocal
		if s.InJob {
			step.Runs = report.RunsJob
		}
	}
	return step
}

// plannedLocation is where a step's overlay is or will be. Only an installed
// file has a size.
func plannedLocation(path string, installed bool) report.Location {
	if path == "" {
		return report.Location{}
	}
	loc := report.Location{Format: report.FormatOf(path), Path: path, Dir: filepath.Dir(path)}
	if layer := config.LayerOfPath(path); layer != config.LayerUnknown {
		loc.Layer = string(layer)
	}
	if info, err := os.Stat(path); installed && err == nil {
		loc.Size = info.Size()
	}
	return loc
}
