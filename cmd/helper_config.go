package cmd

import (
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var helperConfigCmd = &cobra.Command{
	Use:   "config [flags] <name> [path|list|get <key>|set <key> <value>]",
	Short: "List or save a helper's defaults",
	Example: `  condatainer helper config code-server
  condatainer helper config code-server set cpu 4
  condatainer helper config code-server get cpu`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		refuseInsideJob(cmd)
		scriptPath, _ := config.FindHelperScript(args[0])
		return runHelperConfig(args[0], scriptPath, args[1:])
	},
	ValidArgsFunction: completeHelperScripts,
}

func init() {
	helperConfigCmd.Flags().SetInterspersed(false)
}

// runHelperConfig handles: condatainer helper config <name> [list|get <key>|set <key> <val>|path|-h]
func runHelperConfig(name, scriptPath string, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	// Load params and spec once (needed for list and -h).
	var params []helper.HelperParam
	var spec *scheduler.ResourceSpec
	if scriptPath != "" {
		params, _ = helper.ParseHelperParams(scriptPath)
		spec = helper.ResolveHelperSpec(scriptPath)
	}

	switch sub {
	case "-h", "--help", "":
		keys := []string{"cpu", "mem", "time", "gpu", "account", "partition"}
		for _, p := range params {
			keys = append(keys, p.Key)
		}
		// Laid out to match cobra's help format; this cannot be a real cobra
		// command because the helper name is only known at runtime.
		fmt.Printf("Show or save default settings for the %s helper.\n\n", name)
		fmt.Printf("Usage:\n  condatainer helper config %s <command>\n\n", name)
		fmt.Println("Available Commands:")
		fmt.Println("  get         Print a single saved value")
		fmt.Println("  path        Print the config file path")
		fmt.Println("  set         Save a default value")
		fmt.Println("  list        List saved defaults")
		fmt.Printf("\nExamples:\n")
		fmt.Printf("  condatainer helper config %s list\n", name)
		fmt.Printf("  condatainer helper config %s set cpu 4\n", name)
		fmt.Printf("  condatainer helper config %s get cpu\n", name)
		fmt.Printf("\nSaveable keys: %s\n", strings.Join(keys, ", "))

	case "path":
		p := helper.HelperConfigPath(name)
		if p == "" {
			return fmt.Errorf("cannot determine config path")
		}
		fmt.Println(p)

	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: `condatainer helper config %s get <key>`", name)
		}
		v, ok := helper.GetHelperConfigKey(name, args[1])
		if !ok {
			return fmt.Errorf("key %q not found in %s config", args[1], name)
		}
		fmt.Println(v)

	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: `condatainer helper config %s set <key> <value>`", name)
		}
		if err := helper.SetHelperConfigKey(name, args[1], args[2]); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

	case "list", "ls", "show":
		cfg, err := helper.LoadHelperConfig(name)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		p := helper.HelperConfigPath(name)
		fmt.Printf("# %s\n", p)

		type configRow struct {
			key, def string
		}
		var rows []configRow
		// Resource rows with spec defaults.
		resDefault := func(v string) string {
			if v == "" {
				return "(none)"
			}
			return v
		}
		cpuDef, memDef, timeDef := "", "", ""
		if spec != nil {
			if spec.CpusPerTask > 0 {
				cpuDef = fmt.Sprintf("%d", spec.CpusPerTask)
			}
			if mb := spec.GetMemPerNodeMB(); mb > 0 {
				memDef = utils.FormatMemoryMB(mb)
			}
			if spec.Time > 0 {
				timeDef = utils.FormatDuration(spec.Time)
			}
		}
		rows = append(rows,
			configRow{"cpu", resDefault(cpuDef)},
			configRow{"mem", resDefault(memDef)},
			configRow{"time", resDefault(timeDef)},
			configRow{"gpu", "(none)"},
			configRow{"account", resDefault(scheduler.Account())},
			configRow{"partition", resDefault(scheduler.Partition())},
		)
		for _, pp := range params {
			rows = append(rows, configRow{strings.ToLower(pp.Key), pp.Default})
		}

		maxKey := 4 // len("time")
		for _, r := range rows {
			if len(r.key) > maxKey {
				maxKey = len(r.key)
			}
		}
		for _, r := range rows {
			val, ok := cfg[strings.ToLower(r.key)]
			valDisp := "(not set)"
			if ok && val != "" {
				valDisp = val
			}
			defDisp := ""
			if r.def != "" {
				defDisp = utils.StyleDim(fmt.Sprintf("(default: %s)", r.def))
			}
			fmt.Printf("  %-*s  %-20s  %s\n", maxKey, r.key, valDisp, defDisp)
		}

	default:
		return fmt.Errorf("unknown config command %q — try `condatainer helper config %s -h`", sub, name)
	}
	return nil
}
