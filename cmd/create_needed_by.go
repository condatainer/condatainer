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

	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/conda"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/slot"
	"github.com/condatainer/condatainer/internal/utils"
)

// neededByRefused lists the create flags that --needed-by does not take, with the flag as typed.
func neededByRefused() map[string]bool {
	return map[string]bool{
		"--name": createName != "", "--prefix": createPrefix != "", "--file": createFile != "",
		"--from": createFrom != "", "--desc": createDesc != "", "--store": createStore, "--update": createUpdate,
	}
}

// checkNeededByFlags refuses the flags and arguments --needed-by cannot be used with.
func checkNeededByFlags(args []string) error {
	if len(args) == 0 {
		return errors.New("--needed-by needs a script or a directory")
	}
	for _, flag := range []string{"--name", "--prefix", "--file", "--from", "--desc", "--store", "--update"} {
		if neededByRefused()[flag] {
			return fmt.Errorf("--needed-by cannot be used with %s", flag)
		}
	}
	return nil
}

// neededScripts expands the arguments to the scripts they name, each once, as absolute paths.
//   - A directory stands for the scripts directly in it. A file there that is not a script is skipped, and so is a build recipe, with a note.
//   - A file named as an argument must be a script, and must not be a build recipe.
func neededScripts(args []string) ([]string, error) {
	var scripts []string
	add := func(path string) error {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if !slices.Contains(scripts, abs) {
			scripts = append(scripts, abs)
		}
		return nil
	}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("%s not found", arg)
		}
		if !info.IsDir() {
			if err := requireNeededScript(arg); err != nil {
				return nil, err
			}
			if err := add(arg); err != nil {
				return nil, err
			}
			continue
		}
		found, err := scriptsInDir(arg)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			utils.PrintWarning("No scripts found in directory %s", arg)
		}
		for _, path := range found {
			if err := add(path); err != nil {
				return nil, err
			}
		}
	}
	return scripts, nil
}

// requireNeededScript refuses a named file that is not a script to run.
func requireNeededScript(path string) error {
	if strings.HasSuffix(path, ".sub") {
		return fmt.Errorf("%s is a submit file; give the script it runs", path)
	}
	if err := requireTextScript(path); err != nil {
		return err
	}
	if err := requireInterpreterLine(path); err != nil {
		return err
	}
	return requireNotBuildRecipe(path)
}

// scriptsInDir lists the scripts directly in dir: the files whose first line is an interpreter line.
//   - A build recipe is left out with a note, since a recipe often sits beside the overlay it builds.
func scriptsInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}
	var found []string
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if utils.CheckShebangFile(path) != nil {
			continue
		}
		if err := requireNotBuildRecipe(path); err != nil {
			if strings.Contains(err.Error(), "build recipe") {
				utils.PrintNote("Skipping %s: a build recipe", path)
			}
			continue
		}
		found = append(found, path)
	}
	return found, nil
}

// refuseProjectScripts refuses the command when the standing project covers a script.
// A project pins exact identities, so it is installed by `project lock` and `project restore`, not by name.
func refuseProjectScripts(scripts []string) error {
	for _, script := range scripts {
		if !projectCoversScript(script) {
			continue
		}
		standing, err := standingProjectHere()
		if err != nil || standing == nil {
			return err
		}
		return fmt.Errorf("%s belongs to the project at %s, which pins exact identities\n"+
			"run `condatainer project lock --project %s`, then `condatainer project restore --project %s`, or give --no-project",
			script, standing.Root, standing.Root, standing.Root)
	}
	return nil
}

// neededKey is how a request is told apart from another: an overlay file by its absolute path, a name by its normalized form.
func neededKey(value string) (key, request string) {
	if utils.IsPathDep(value) {
		abs, err := filepath.Abs(value)
		if err != nil {
			return value, value
		}
		return abs, abs
	}
	name := utils.NormalizeModule(value)
	return name, name
}

// neededOverlays is every overlay the scripts declare, once, with where each stands.
//   - A #DEP: is read by the rule a run mounts it by.
//   - A #SLOT: lists its default and every choice. One no #DEP: names is read by the rule a run binds a slot by.
//   - A relative overlay file is read against the current directory, as a run reads it.
func neededOverlays(ctx context.Context, scripts []string) ([]scriptDep, error) {
	var depValues, slotValues []string
	seenDep, seenSlot := map[string]bool{}, map[string]bool{}
	for _, script := range scripts {
		deps, err := utils.ScriptDependencies(script)
		if err != nil {
			return nil, err
		}
		for _, value := range deps {
			if key, request := neededKey(value); !seenDep[key] {
				seenDep[key] = true
				depValues = append(depValues, request)
			}
		}
	}
	for _, script := range scripts {
		decls, err := slot.ParseScript(script)
		if err != nil {
			return nil, err
		}
		for _, decl := range decls {
			for _, value := range decl.Listed() {
				if key, request := neededKey(value); !seenDep[key] && !seenSlot[key] {
					seenSlot[key] = true
					slotValues = append(slotValues, request)
				}
			}
		}
	}

	var wanted []scriptDep
	if len(depValues) > 0 {
		resolved, _, err := scriptDeps(ctx, depValues)
		if err != nil {
			return nil, err
		}
		wanted = append(wanted, resolved...)
	}
	if len(slotValues) > 0 {
		resolved, err := slotOverlayStates(ctx, slotValues)
		if err != nil {
			return nil, err
		}
		wanted = append(wanted, resolved...)
	}
	return wanted, nil
}

// slotOverlayStates says where each overlay a slot lists stands, by the rule a run binds a slot by.
func slotOverlayStates(ctx context.Context, requests []string) ([]scriptDep, error) {
	mounts, _, err := resolveSlotMounts(ctx, requests, false)
	if err != nil {
		return nil, err
	}
	if len(mounts) != len(requests) {
		return nil, fmt.Errorf("resolved %d of %d slot overlays", len(mounts), len(requests))
	}
	out := make([]scriptDep, len(requests))
	for i, mount := range mounts {
		d := scriptDep{Request: requests[i], Path: requests[i]}
		switch {
		case !slotMountMissing(mount):
			d.Path = mount.Path
		case d.IsFile():
			d.State = depMissingFile
		default:
			d.State = depMissingName
		}
		out[i] = d
	}
	return out, nil
}

// neededFile is a missing overlay file and the source file beside it that builds it.
type neededFile struct{ Overlay, Source string }

// neededRefusal is an overlay that cannot be installed, and why.
type neededRefusal struct{ Request, Reason string }

// neededPlan sorts what the scripts need by what is done about it.
type neededPlan struct {
	Installed []scriptDep
	Files     []neededFile
	// Names are the missing names to create, expanded to concrete modules.
	Names []string
	// Resolved maps each missing name as declared to the module that is created for it.
	Resolved map[string]string
	Refused  []neededRefusal
}

// classifyNeeded decides what each missing overlay needs, and refuses the ones that cannot be installed.
func classifyNeeded(ctx context.Context, wanted []scriptDep) neededPlan {
	plan := neededPlan{Resolved: map[string]string{}}
	for _, d := range wanted {
		switch {
		case !d.Missing():
			plan.Installed = append(plan.Installed, d)
		case d.IsFile():
			source, reason := neededFileSource(d.Request)
			if reason != "" {
				plan.Refused = append(plan.Refused, neededRefusal{d.Request, reason})
			} else {
				plan.Files = append(plan.Files, neededFile{d.Request, source})
			}
		default:
			name, reason := neededName(ctx, d.Request)
			switch {
			case reason != "":
				plan.Refused = append(plan.Refused, neededRefusal{d.Request, reason})
			default:
				plan.Resolved[d.Request] = name
				if !slices.Contains(plan.Names, name) {
					plan.Names = append(plan.Names, name)
				}
			}
		}
	}
	return plan
}

// neededFileSource is the source file that builds a missing overlay file, or why it cannot be built.
//   - Only a .sqf is built, since a build writes <prefix>.sqf.
func neededFileSource(overlay string) (source, reason string) {
	ext := strings.ToLower(filepath.Ext(overlay))
	switch {
	case ext == ".sqf":
	case utils.IsImg(overlay):
		return "", "a writable overlay is made, not built; use `condatainer overlay create`"
	case utils.IsSif(overlay):
		return "", "a .sif is pulled or built with Apptainer"
	default:
		return "", "only a .sqf is built; name it " + strings.TrimSuffix(overlay, filepath.Ext(overlay)) + ".sqf"
	}
	if source = findSiblingSourceFile(overlay); source != "" {
		return source, ""
	}
	reason = "no source file (.yml, .yaml, .def, .sh or .txt) beside it"
	if txt := strings.TrimSuffix(overlay, filepath.Ext(overlay)) + ".txt"; utils.FileExists(txt) {
		reason += fmt.Sprintf("; %s has no @EXPLICIT line", filepath.Base(txt))
	}
	return "", reason
}

// findSiblingSourceFile is the first source file with the base name of an overlay, in the order .yml, .yaml, .def, .sh, .txt.
//   - A .txt counts only as an explicit Conda spec, since a plain list cannot be told from notes.
func findSiblingSourceFile(overlayPath string) string {
	base := strings.TrimSuffix(overlayPath, filepath.Ext(overlayPath))
	for _, ext := range []string{".yml", ".yaml", ".def", ".sh", ".txt"} {
		path := base + ext
		if !utils.FileExists(path) {
			continue
		}
		if ext == ".txt" {
			if text, err := os.ReadFile(path); err != nil || !conda.IsExplicit(text) {
				continue
			}
		}
		return path
	}
	return ""
}

// neededName expands a missing name to the module create builds, as `create <name>` does.
//   - A template family names no module, so it is refused and a variant is asked for.
func neededName(ctx context.Context, request string) (name, reason string) {
	name, _, err := solveCreateName(ctx, request)
	if err != nil {
		return "", err.Error()
	}
	if cat, err := config.OpenCatalog(ctx); err == nil {
		if m, found, err := cat.Lookup(ctx, name); err == nil && found && m.Entry.IsTemplate && len(m.Vars) == 0 {
			return "", fmt.Sprintf("%s is a template; name one variant, as `condatainer avail -e %s` lists them", name, name)
		}
	}
	return name, ""
}

// planNeededNames plans the missing names as `create` builds them, and refuses a name that has no build object.
func planNeededNames(ctx context.Context, names []string) (steps []build.PlannedStep, kept []string, refused []neededRefusal, err error) {
	ctx = build.PlanOnly(ctx)
	imagesDir, err := writableImagesDirFor(createLayer)
	if err != nil {
		return nil, nil, nil, err
	}
	createPlanDir = imagesDir
	var objects []*build.BuildObject
	for _, name := range names {
		bo, err := build.NewBuildObject(ctx, name, false, imagesDir, false)
		if err != nil {
			refused = append(refused, neededRefusal{name, err.Error()})
			continue
		}
		objects = append(objects, bo)
		kept = append(kept, name)
	}
	if len(objects) == 0 {
		return nil, nil, refused, nil
	}
	graph, err := build.NewBuildGraph(ctx, objects, imagesDir, scheduler.Enabled(), false)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create build graph: %w", err)
	}
	graph.SetJobFlags(createJobFlags())
	return graph.Plan(), kept, refused, nil
}

// neededFileStep is the step that builds one missing overlay file.
func neededFileStep(f neededFile) report.Step {
	return report.Step{
		Location: plannedLocation(f.Overlay, false),
		Action:   report.ActionBuild,
		Runs:     report.RunsLocal,
	}
}

// neededRefusalStep is the step of an overlay that cannot be installed.
func neededRefusalStep(r neededRefusal) report.Step {
	step := report.Step{Action: report.ActionUnavailable, Reason: r.Reason}
	if utils.IsPathDep(r.Request) {
		step.Location = plannedLocation(r.Request, false)
	} else {
		step.Name = r.Request
	}
	return step
}

// scriptNeed is one overlay a script declares, and the slot that lists it when no #DEP: does.
type scriptNeed struct{ Request, Slot string }

// scriptNeeds is what one script declares, in the order it declares it.
type scriptNeeds struct {
	Script string
	Needs  []scriptNeed
}

// scriptNeedsOf lists what each script declares: its #DEP: values, then the overlays its slots list.
func scriptNeedsOf(scripts []string) ([]scriptNeeds, error) {
	out := make([]scriptNeeds, 0, len(scripts))
	for _, script := range scripts {
		entry := scriptNeeds{Script: script}
		seen := map[string]bool{}
		deps, err := utils.ScriptDependencies(script)
		if err != nil {
			return nil, err
		}
		for _, value := range deps {
			if key, request := neededKey(value); !seen[key] {
				seen[key] = true
				entry.Needs = append(entry.Needs, scriptNeed{Request: request})
			}
		}
		decls, err := slot.ParseScript(script)
		if err != nil {
			return nil, err
		}
		for _, decl := range decls {
			for _, value := range decl.Listed() {
				key, request := neededKey(value)
				if seen[key] {
					continue
				}
				seen[key] = true
				note := "slot " + decl.Name
				if value == decl.Default {
					note += ", default"
				}
				entry.Needs = append(entry.Needs, scriptNeed{Request: request, Slot: note})
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// needLine is how one declared overlay stands: ✓ when it is installed, else ✗ and what is done about it.
type needLine struct {
	Installed bool
	Detail    string
}

// lineOf says how a declared overlay stands in the plan.
func (p neededPlan) lineOf(request string) needLine {
	for _, d := range p.Installed {
		if d.Request == request {
			return needLine{Installed: true}
		}
	}
	for _, f := range p.Files {
		if f.Overlay == request {
			return needLine{Detail: "build from " + filepath.Base(f.Source)}
		}
	}
	name, resolved := p.Resolved[request]
	for _, r := range p.Refused {
		if r.Request == request || (resolved && r.Request == name) {
			return needLine{Detail: r.Reason}
		}
	}
	switch {
	case !resolved:
		return needLine{}
	case name == request:
		return needLine{Detail: "install"}
	}
	return needLine{Detail: "install as " + name}
}

// writeNeeded lists what each script declares and how each overlay stands.
func writeNeeded(w io.Writer, needs []scriptNeeds, plan neededPlan) {
	for _, sn := range needs {
		fmt.Fprintln(w, displayPath(sn.Script))
		if len(sn.Needs) == 0 {
			fmt.Fprintln(w, "  nothing to install")
			continue
		}
		shown := make([]string, len(sn.Needs))
		width := 0
		for i, n := range sn.Needs {
			shown[i] = n.Request
			if utils.IsPathDep(n.Request) {
				shown[i] = planPath(n.Request)
			}
			width = max(width, len(shown[i]))
		}
		for i, n := range sn.Needs {
			l := plan.lineOf(n.Request)
			if l.Installed {
				fmt.Fprintf(w, "  %s %s\n", utils.StyleSuccess("✓"), shown[i])
				continue
			}
			line := fmt.Sprintf("  %s %s", utils.StyleError("✗"), shown[i])
			if l.Detail != "" {
				detail := l.Detail
				if n.Slot != "" {
					detail += " (" + n.Slot + ")"
				}
				line += strings.Repeat(" ", 3+width-len(shown[i])) + detail
			}
			fmt.Fprintln(w, line)
		}
	}
}

// neededSteps are the steps of the plan block: the files to build, then what the graph planned for the names.
//   - A file is built where its source is, so it is shown by its path, with the source it is built from.
func neededSteps(plan neededPlan, planned []build.PlannedStep) ([]build.PlannedStep, planContext) {
	pc := planContext{typed: map[string]string{}, dir: createPlanDir, from: map[string]string{}}
	var steps []build.PlannedStep
	for _, f := range plan.Files {
		name := strings.TrimSuffix(filepath.Base(f.Overlay), filepath.Ext(f.Overlay))
		steps = append(steps, build.PlannedStep{Name: name, Path: f.Overlay})
		pc.typed[name] = name
		pc.from[f.Overlay] = filepath.Base(f.Source)
	}
	for _, name := range plan.Names {
		pc.typed[name] = name
	}
	return append(steps, planned...), pc
}

// writeNeededPlan prints the script blocks and, when something is to be installed, the plan block.
func writeNeededPlan(w io.Writer, needs []scriptNeeds, plan neededPlan, planned []build.PlannedStep, dryRun bool) {
	writeNeeded(w, needs, plan)
	steps, pc := neededSteps(plan, planned)
	if installed, block := planLines(steps, pc); len(installed)+len(block) > 0 {
		fmt.Fprintln(w)
	}
	writePlan(w, steps, pc, dryRun)
}

// buildNeededFile builds one missing overlay file from the source beside it, as `create -f <source> -p <overlay>` does.
//   - It returns the graph when a scheduler may have been given jobs.
func buildNeededFile(ctx context.Context, f neededFile) (*build.BuildGraph, error) {
	absPrefix := strings.TrimSuffix(f.Overlay, filepath.Ext(f.Overlay))
	outputDir := filepath.Dir(absPrefix)

	if utils.IsCondaFile(f.Source) {
		bo, err := build.NewCondaObjectWithSource(filepath.Base(absPrefix), f.Source, outputDir, false)
		if err != nil {
			return nil, err
		}
		return nil, bo.Build(ctx, false)
	}
	bo, err := build.FromExternalSource(ctx, absPrefix, f.Source, strings.HasSuffix(f.Source, ".def"), outputDir, false)
	if err != nil {
		return nil, err
	}
	bo.SetJobArgs(prefixJobArgs(absPrefix, f.Source))
	graph, err := build.NewBuildGraph(ctx, []*build.BuildObject{bo}, outputDir, scheduler.Enabled(), false)
	if err != nil {
		return nil, err
	}
	graph.SetJobFlags(createJobFlagsFor(""))
	return graph, graph.Run(ctx)
}

// runCreateNeededBy installs what the scripts in args declare.
//   - It shows what each script declares and the plan, asks before installing, and builds the files before the names.
//   - It exits 3 when build jobs were submitted, else 1 when an overlay could not be installed.
func runCreateNeededBy(ctx context.Context, args []string) {
	scripts, err := neededScripts(args)
	if err != nil {
		ExitWithError("%v", err)
	}
	if len(scripts) == 0 {
		utils.PrintWarning("No scripts found.")
		return
	}
	if err := refuseProjectScripts(scripts); err != nil {
		ExitWithError("%v", err)
	}
	wanted, err := neededOverlays(ctx, scripts)
	if err != nil {
		ExitWithError("%v", err)
	}
	if len(wanted) == 0 {
		utils.PrintMessage("No dependencies found.")
		return
	}
	needs, err := scriptNeedsOf(scripts)
	if err != nil {
		ExitWithError("%v", err)
	}

	plan := classifyNeeded(ctx, wanted)
	planned, kept, refused, err := planNeededNames(ctx, plan.Names)
	if err != nil {
		ExitWithError("%v", err)
	}
	plan.Names = kept
	plan.Refused = append(plan.Refused, refused...)

	if createDryRun {
		if createJSON {
			reportNeededPlan(needs, plan, planned)
		} else if !utils.QuietMode {
			writeNeededPlan(os.Stdout, needs, plan, planned, true)
		}
		if len(plan.Refused) > 0 {
			os.Exit(ExitCodeError)
		}
		return
	}

	pending := len(plan.Files) + builtCount(planned)
	asking := pending > 0 && !utils.ShouldAnswerYes()
	if !utils.QuietMode || asking {
		writeNeededPlan(os.Stdout, needs, plan, planned, false)
	}
	if pending == 0 {
		if len(plan.Refused) > 0 {
			os.Exit(ExitCodeError)
		}
		utils.PrintSuccess("Everything the scripts need is installed.")
		return
	}
	if asking && !utils.ConfirmDefaultYes(ctx, os.Stderr, fmt.Sprintf("Install %d overlay(s)? [Y/n]: ", pending)) {
		utils.PrintMessage("Nothing was installed.")
		if len(plan.Refused) > 0 {
			os.Exit(ExitCodeError)
		}
		return
	}

	failed, jobs, built := len(plan.Refused) > 0, 0, 0
	for _, f := range plan.Files {
		graph, err := buildNeededFile(ctx, f)
		jobs += jobsOf(graph)
		if err == nil {
			if jobsOf(graph) == 0 {
				built++
			}
			continue
		}
		if ctx.Err() != nil || errors.Is(err, build.ErrBuildCancelled) {
			exitOnBuildError(err)
		}
		utils.PrintError("Build failed for %s: %v", f.Overlay, err)
		failed = true
	}
	if len(plan.Names) > 0 {
		planShown = true
		graph, n := buildPackages(ctx, plan.Names)
		jobs += jobsOf(graph)
		built += n
	}
	switch {
	case jobs > 0:
		utils.PrintNote("%d scheduler job(s) submitted. exiting with code %d", jobs, ExitCodeJobsSubmitted)
		os.Exit(ExitCodeJobsSubmitted)
	case failed:
		os.Exit(ExitCodeError)
	}
	printBuilt(built)
}

// reportNeededPlan ends a dry run with --json: the steps of every overlay as a document.
//   - A step names the scripts that declare it, and the steps that need it.
func reportNeededPlan(needs []scriptNeeds, plan neededPlan, planned []build.PlannedStep) {
	doc := &report.Steps{Steps: report.List[report.Step]{}}
	wantedBy := func(match func(request string) bool) []string {
		var scripts []string
		for _, sn := range needs {
			for _, n := range sn.Needs {
				if match(n.Request) {
					scripts = append(scripts, displayPath(sn.Script))
					break
				}
			}
		}
		return scripts
	}
	for _, d := range plan.Installed {
		step := reportStep(installedStep(d.Request, d.Path))
		step.Scripts = wantedBy(func(r string) bool { return r == d.Request })
		doc.Steps = append(doc.Steps, step)
	}
	for _, f := range plan.Files {
		step := neededFileStep(f)
		step.Scripts = wantedBy(func(r string) bool { return r == f.Overlay })
		doc.Steps = append(doc.Steps, step)
	}
	dependents := dependentsOf(planned)
	for _, p := range planned {
		step := reportStep(p)
		step.Dependents = dependents[p.Name]
		step.Scripts = wantedBy(func(r string) bool { return plan.Resolved[r] == p.Name })
		doc.Steps = append(doc.Steps, step)
	}
	for _, r := range plan.Refused {
		step := neededRefusalStep(r)
		step.Scripts = wantedBy(func(request string) bool { return request == r.Request || plan.Resolved[request] == r.Request })
		doc.Steps = append(doc.Steps, step)
	}
	if err := report.Write(os.Stdout, doc); err != nil {
		ExitWithError("%v", err)
	}
}
