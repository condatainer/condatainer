// Package ui holds the terminal prompts and displays the CLI commands share.
package ui

import (
	"fmt"
	"sort"

	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/utils"
)

// RenderExecPlan prints CLI-only diagnostics and environment notes for a prepared exec plan.
func RenderExecPlan(plan *execpkg.Plan) {
	if plan == nil {
		return
	}
	for _, d := range plan.Diagnostics {
		switch d.Level {
		case "warn", "warning":
			utils.PrintWarning("%s", d.Message)
		case "note":
			utils.PrintNote("%s", d.Message)
		case "error":
			utils.PrintError("%s", d.Message)
		default:
			utils.PrintMessage("%s", d.Message)
		}
	}

	if !utils.IsInteractiveShell() || plan.Options.HidePrompt {
		return
	}
	if len(plan.Setup.EnvNotes) > 0 {
		utils.PrintMessage("Overlay envs:")
		keys := make([]string, 0, len(plan.Setup.EnvNotes))
		for key := range plan.Setup.EnvNotes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Printf("  %s: %s\n", utils.StyleName(key), plan.Setup.EnvNotes[key])
		}
		fmt.Println("")
	}
}
