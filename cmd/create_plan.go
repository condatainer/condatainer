package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
)

// reportCreatePlan prints what create would do and builds, pulls and submits
// nothing. The text is the notes planning already printed and one closing line;
// --json prints the steps document instead.
func reportCreatePlan(steps []build.PlannedStep) {
	if !createJSON {
		utils.PrintMessage("Dry run: nothing was built, pulled or submitted.")
		return
	}
	doc := &report.Steps{Steps: make(report.List[report.Step], 0, len(steps))}
	for _, step := range steps {
		doc.Steps = append(doc.Steps, reportStep(step))
	}
	if err := report.Write(os.Stdout, doc); err != nil {
		ExitWithError("%v", err)
	}
}

// reportObjectPlan reports a build that runs by itself, outside a graph, which
// prints no planning notes of its own.
func reportObjectPlan(ctx context.Context, bo *build.BuildObject) {
	step := bo.PlannedStep(ctx)
	if !createJSON {
		if step.Installed {
			utils.PrintNote("%s is already installed", step.Name)
		} else {
			utils.PrintNote("%s will be built locally", step.Name)
		}
	}
	reportCreatePlan([]build.PlannedStep{step})
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
