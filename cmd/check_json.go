package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// checkedScript is one argument of check: a script, or a recipe given by name,
// with the dependencies it declares.
type checkedScript struct {
	label string
	deps  []string
}

// runCheckJSON prints the checked document. Each script lists its own
// dependencies; missing is the union. With -a it is a dry run, and the steps
// are what installing the missing would do.
func runCheckJSON(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	var scripts []checkedScript
	var allPaths, allMeta []string
	for _, arg := range args {
		paths, remotes, metaDeps, err := resolveAllScriptPaths(ctx, []string{arg})
		for _, rp := range remotes {
			defer os.Remove(rp)
		}
		if err != nil {
			return err
		}
		allPaths, allMeta = append(allPaths, paths...), append(allMeta, metaDeps...)
		if len(metaDeps) > 0 {
			deps, _ := collectDeps(nil, metaDeps)
			scripts = append(scripts, checkedScript{label: arg, deps: deps})
		}
		for _, p := range paths {
			deps, err := collectDeps([]string{p}, nil)
			if err != nil {
				return err
			}
			label := p
			if len(remotes) > 0 && p == remotes[0] {
				label = arg
			}
			scripts = append(scripts, checkedScript{label: label, deps: deps})
		}
	}
	if handled, err := projectCheck(ctx, allPaths, allMeta); handled {
		return err
	}

	doc := &report.Checked{Scripts: report.List[report.Script]{}, Missing: report.List[string]{}}
	if len(scripts) == 0 {
		doc.Problems = append(doc.Problems, report.Problem{Message: "no scripts found to check"})
		return report.Write(cmd.OutOrStdout(), doc)
	}

	var unique []string
	for _, s := range scripts {
		for _, dep := range s.deps {
			if !slices.Contains(unique, dep) {
				unique = append(unique, dep)
			}
		}
	}
	solved := append([]string(nil), unique...)
	if len(unique) > 0 {
		var err error
		if solved, err = resolveOverlayValues(ctx, unique, nil, true); err != nil {
			return err
		}
	}
	records := make(map[string]report.Dependency, len(unique))
	var missing []string
	for i, dep := range unique {
		rec := dependencyRecord(dep, solved[i])
		records[dep] = rec
		if !rec.Installed {
			missing = append(missing, dep)
		}
	}
	for _, s := range scripts {
		entry := report.Script{Script: s.label, Deps: report.List[report.Dependency]{}}
		for _, dep := range s.deps {
			entry.Deps = append(entry.Deps, records[dep])
		}
		doc.Scripts = append(doc.Scripts, entry)
	}
	doc.Missing = append(doc.Missing, missing...)

	var refused error
	if checkAutoInstall && len(missing) > 0 {
		steps, err := planMissing(ctx, missing)
		if err != nil {
			return err
		}
		for _, step := range steps {
			if step.Action == report.ActionUnavailable {
				refused = errors.New("some missing overlays cannot be installed")
			}
		}
		doc.Steps = steps
	}
	if err := report.Write(cmd.OutOrStdout(), doc); err != nil {
		return err
	}
	return refused
}

// dependencyRecord is the record of one request. solved is the request with an
// installed name replaced by its overlay path, so a value left unchanged is not
// installed; an overlay given by path is installed when the file is there.
func dependencyRecord(request, solved string) report.Dependency {
	rec := report.Dependency{Request: request}
	path := solved
	if utils.IsOverlay(request) || utils.IsSif(request) {
		rec.Installed = utils.FileExists(request)
		path = request
	} else {
		rec.Installed = solved != request
	}
	if !rec.Installed {
		return rec
	}
	rec.Location = plannedLocation(path, true)
	if rt, err := meta.ReadRuntime(path); err == nil {
		rec.Name = rt.Name
	}
	return rec
}

// planMissing plans installing the missing dependencies without installing
// them: package names through the build planner, overlays given by path from
// the source file beside them. An overlay that cannot be made is an
// unavailable step, and says why.
func planMissing(ctx context.Context, missing []string) ([]report.Step, error) {
	var packages []string
	var steps []report.Step
	for _, dep := range missing {
		if utils.IsOverlay(dep) || utils.IsSif(dep) {
			steps = append(steps, externalStep(dep))
		} else {
			packages = append(packages, dep)
		}
	}
	if len(packages) > 0 {
		planned, err := planPackages(ctx, packages)
		if err != nil {
			return nil, err
		}
		for _, step := range planned {
			steps = append(steps, reportStep(step))
		}
	}
	return steps, nil
}

// planPackages plans the named packages as create would build them.
func planPackages(ctx context.Context, packages []string) ([]build.PlannedStep, error) {
	ctx = build.PlanOnly(ctx)
	imagesDir, err := config.GetWritableImagesDir()
	if err != nil {
		return nil, fmt.Errorf("no writable images directory found: %w", err)
	}
	objects := make([]*build.BuildObject, 0, len(packages))
	for _, pkg := range packages {
		bo, err := build.NewBuildObject(ctx, pkg, false, imagesDir, false)
		if err != nil {
			return nil, fmt.Errorf("failed to create build object for %s: %w", pkg, err)
		}
		objects = append(objects, bo)
	}
	graph, err := build.NewBuildGraph(ctx, objects, imagesDir, scheduler.Enabled(), false)
	if err != nil {
		return nil, fmt.Errorf("failed to create build graph: %w", err)
	}
	return graph.Plan(), nil
}

// externalStep is the step -a takes for a missing overlay given by path: a
// build from the source file beside it, or unavailable when there is none or
// the file declares dependencies of its own.
func externalStep(dep string) report.Step {
	step := report.Step{Location: plannedLocation(dep, false)}
	sibling := findSiblingSourceFile(dep)
	if sibling == "" {
		step.Action, step.Reason = report.ActionUnavailable, "no source file (.yml, .yaml, .def or .sh) beside it"
		return step
	}
	if strings.HasSuffix(sibling, ".sh") {
		if shDeps, _ := utils.ScriptDependencies(sibling); len(shDeps) > 0 {
			step.Action = report.ActionUnavailable
			step.Reason = fmt.Sprintf("%s declares #DEP:; create it with `condatainer create -f %s`", filepath.Base(sibling), sibling)
			return step
		}
	}
	step.Action, step.Runs = report.ActionBuild, report.RunsLocal
	return step
}

// dryRunInstall reports what -a would install, and installs nothing. It fails
// when -a would, for an overlay it cannot make.
func dryRunInstall(ctx context.Context, missing []string) error {
	steps, err := planMissing(ctx, missing)
	if err != nil {
		return err
	}
	var refused error
	for _, step := range steps {
		if step.Action == report.ActionUnavailable {
			utils.PrintError("%s cannot be installed: %s", step.Path, step.Reason)
			refused = errors.New("some missing overlays cannot be installed")
		}
	}
	utils.PrintMessage("Dry run: nothing was installed.")
	return refused
}
