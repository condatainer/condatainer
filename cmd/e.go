package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/cmd/internal/ui"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	eReadOnly    bool
	eNoAutoload  bool
	eFakeroot    bool
	eLoadGPU     bool
	eActivation  string
	eEnvSettings []string
	eBindPaths   []string
	eProjectDir  string
)

// eCmd is a quick shortcut for executing commands with overlays
var eCmd = &cobra.Command{
	Use:   "e [flags] [overlays...] [-- command...]",
	Short: "Shortcut for exec with overlays, writable by default",
	Long: `Shortcut for ` + "`exec`" + ` with overlays given as arguments, writable by default.

- Overlays and flags go before --, the command goes after it.
- Loads environment overlay from the current directory unless -n is set.
- Extra Apptainer flags must use --flag=value format (no space).

The environment overlay is the first of these in the current directory:
  1. env-$USER.img, else env.img, with its env.sqf.
  2. env.sqf alone, when there is no .img.`,
	Example: `  condatainer e                               # Load env.img if present
  condatainer e testing.img
  condatainer e samtools/1.22 bcftools/1.20
  condatainer e samtools/1.22 -- samtools     # Run a command
  condatainer e --home=/custom samtools/1.22  # Pass Apptainer flags`,
	SilenceUsage: true,
	RunE:         runE,
}

func init() {
	rootCmd.AddCommand(eCmd)

	eCmd.Flags().BoolVarP(&eReadOnly, "read-only", "r", false, "Mount .img overlays as read-only (default: writable)")
	eCmd.Flags().BoolVarP(&eNoAutoload, "no-autoload", "n", false, "Disable auto-loading env.img from current directory")
	eCmd.Flags().BoolVarP(&eFakeroot, "fakeroot", "f", false, "Run with fakeroot privileges")
	eCmd.Flags().BoolVar(&eLoadGPU, "load-gpu", false, "Make the host GPU available even if autoload_gpu is off")
	eCmd.Flags().StringVar(&eActivation, "activation", "all", "Which activate.d scripts to source before the command: all, env, or none")
	eCmd.Flags().StringArrayVar(&eEnvSettings, "env", nil, "Set environment variable KEY=VALUE (repeatable)")
	eCmd.Flags().StringSliceVar(&eBindPaths, "bind", nil, "Bind mount HOST:CONTAINER (repeatable)")
	RegisterProjectFlags(eCmd, &eProjectDir)

	// Allow flags to be interspersed with overlays
	eCmd.Flags().SetInterspersed(true)

	// Allow unknown flags to pass through (for apptainer)
	eCmd.FParseErrWhitelist.UnknownFlags = true

	// Completion
	eCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// During completion, check if -- appears in os.Args
		// This is more reliable than checking the processed args array
		if slices.Contains(os.Args, "--") {
			// After --, use default file completion
			return nil, cobra.ShellCompDirectiveDefault
		}
		// Before --, complete overlays
		return overlaySuggestions(true, toComplete)
	}
}

func runE(cmd *cobra.Command, args []string) error {
	if err := applyProjectFlag(eProjectDir); err != nil {
		return err
	}

	// STRICT parsing: everything before -- is overlay/flag, after is command
	overlays, commands, apptainerFlags, err := parseEArgs(cmd, args)
	if err != nil {
		return err
	}

	// Auto-load env.img if not disabled and no .img in overlays.
	// Reported after Project detection
	var announceAutoload func()
	if !eNoAutoload && !container.NamesEnvironment(overlays) {
		if pwd, err := os.Getwd(); err == nil {
			var autoloaded []string
			autoloaded, announceAutoload = autoloadEnv(pwd, eReadOnly)
			overlays = append(overlays, autoloaded...)
		}
	}

	// Prepare command and determine if prompt should be hidden
	commands, hidePrompt := PrepareCommandAndHidePrompt(commands)

	// Resolve overlays
	overlays, err = projectOverlays(cmd.Context(), overlays)
	if err != nil {
		return err
	}
	resolvedOverlays, err := container.ResolveOverlayPaths(overlays)
	if err != nil {
		return err
	}

	baseImageResolved, err := ensureRootBaseImage(cmd.Context(), resolvedOverlays)
	if err != nil {
		return err
	}
	if announceAutoload != nil {
		announceAutoload()
	}

	activation, err := parseActivation(eActivation)
	if err != nil {
		return err
	}

	// Build options
	options := exec.Options{
		Overlays:       resolvedOverlays,
		Command:        commands,
		WritableImg:    !eReadOnly,                                            // Default writable unless -r specified
		EnvSettings:    append(liveJobResourceEnvSettings(), eEnvSettings...), // Inject live job resources, then user env vars
		BindPaths:      append(configBinds(), eBindPaths...),
		ApptainerFlags: apptainerFlags,
		Fakeroot:       eFakeroot,
		GpuRequested:   eLoadGPU,
		BaseImage:      baseImageResolved,
		HidePrompt:     hidePrompt,
		Activation:     activation,
		HostRun:        hostrun.KindExec,
	}

	plan, err := exec.Prepare(cmd.Context(), options)
	if err != nil {
		return err
	}
	ui.RenderExecPlan(plan)
	if err := exec.RunPrepared(cmd.Context(), plan, exec.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(cmd.Context().Err(), context.Canceled) {
			return nil
		}
		// Propagate exit code from container command
		if appErr, ok := err.(*apptainer.ApptainerError); ok {
			if code := appErr.ExitCode(); code >= 0 {
				os.Exit(code)
			}
		}
		return err
	}
	return nil
}

// autoloadEnv picks the environment overlays in dir for a run that named no .img, and the message to print once the run is set up.
//   - An env.img that can be locked is loaded with its paired env.sqf.
//   - An env.img that cannot be used loads nothing, and neither does its env.sqf. The snapshot alone is an older environment, not this one.
//   - With no env.img, an env.sqf in dir is loaded read-only.
func autoloadEnv(dir string, readOnly bool) ([]string, func()) {
	candidate := utils.FindEnvOverlay("", dir)
	if candidate == "" {
		snapshot := helper.FindEnvSnapshot(dir)
		if snapshot == "" {
			return nil, nil
		}
		return []string{snapshot}, func() {
			utils.PrintNote("Autoload snapshot %s", filepath.Base(snapshot))
		}
	}

	imgName := filepath.Base(candidate)
	snapshot := container.LookupSnapshot(candidate).Path
	err := image.CheckAvailable(candidate, !readOnly)
	switch {
	case err == nil && snapshot != "":
		return []string{candidate, snapshot}, func() {
			utils.PrintNote("Autoload environment overlay %s with %s", imgName, filepath.Base(snapshot))
		}
	case err == nil:
		return []string{candidate}, func() {
			utils.PrintNote("Autoload environment overlay %s", imgName)
		}
	}

	reason := err.Error()
	if errors.Is(err, image.ErrInUse) {
		reason = imgName + " is in use"
	}
	hint := unavailableHint(candidate, snapshot, readOnly)
	return nil, func() {
		utils.PrintWarning("%s; running without it%s", reason, hint)
	}
}

// unavailableHint names what the user can run when the env.img cannot be opened, or "" when nothing would load.
//   - -r opens an env.img that is write-protected or held only by readers.
//   - Naming the env.sqf opens the snapshot alone, read-only.
func unavailableHint(img, snapshot string, readOnly bool) string {
	switch {
	case !readOnly && image.CheckAvailable(img, false) == nil:
		return " (-r mounts it read-only)"
	case snapshot != "":
		return fmt.Sprintf(" (e %s loads the snapshot read-only)", filepath.Base(snapshot))
	}
	return ""
}

func parseEArgs(cmd *cobra.Command, args []string) (overlays, commands, apptainerFlags []string, err error) {
	// Parse os.Args directly to catch unknown flags that Cobra filtered out
	eIdx := -1
	for i, arg := range os.Args {
		if arg == "e" {
			eIdx = i
			break
		}
	}

	tokens := flagTokens(cmd)

	commandStarted := false

	if eIdx == -1 {
		// Fallback: no os.Args parsing, just return cobra's positional args as overlays
		for _, arg := range args {
			if arg == "--" {
				commandStarted = true
				continue
			}
			if commandStarted {
				commands = append(commands, arg)
				continue
			}
			overlays = append(overlays, arg)
		}
		return overlays, commands, apptainerFlags, nil
	}

	for i := eIdx + 1; i < len(os.Args); i++ {
		arg := os.Args[i]

		if arg == "--" {
			commandStarted = true
			continue
		}

		if commandStarted {
			// Everything after -- is command
			commands = append(commands, arg)
			continue
		}

		// Before -- (overlay/flag section)

		// Skip the command's own flags (already handled by cobra)
		if own, valueFollows := ownFlag(tokens, arg); own {
			if valueFollows && i+1 < len(os.Args) {
				i++ // Skip value
			}
			continue
		}

		// Unknown flag → must use --flag=value format
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") {
				// Has value: --home=/path
				apptainerFlags = append(apptainerFlags, arg)
			} else {
				// No value: boolean flag like --nv
				apptainerFlags = append(apptainerFlags, arg)
			}
			continue
		}

		// Not a flag, before -- → overlay
		overlays = append(overlays, arg)
	}

	return overlays, commands, apptainerFlags, nil
}
