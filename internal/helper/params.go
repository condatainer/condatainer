package helper

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// ApplyParamFlags scans raw CLI args for flags defined by params.
//   - Standard resource flags (-c/-m/-t/-g/-b/-e/-o/-w) are already parsed by cobra and must NOT be in args — this handles only helper-specific #PARAM: flags.
//   - Returns (values map, unrecognised args, error).
func ApplyParamFlags(params []HelperParam, args []string) (map[string]string, []string, error) {
	values := make(map[string]string)
	var extra []string

	// Build lookup maps: long flag → param, short flag → param
	byLong := make(map[string]*HelperParam, len(params))
	byShort := make(map[string]*HelperParam, len(params))
	for i := range params {
		p := &params[i]
		if p.LongFlag != "" {
			byLong[p.LongFlag] = p
		}
		if p.ShortFlag != "" {
			byShort[p.ShortFlag] = p
		}
	}

	i := 0
	for i < len(args) {
		arg := args[i]
		if p := byLong[arg]; p != nil {
			i++
			if i >= len(args) {
				return nil, nil, fmt.Errorf("flag %s requires a value", arg)
			}
			values[p.Key] = args[i]
			i++
			continue
		}
		if p := byShort[arg]; p != nil {
			i++
			if i >= len(args) {
				return nil, nil, fmt.Errorf("flag %s requires a value", arg)
			}
			values[p.Key] = args[i]
			i++
			continue
		}
		// Handle --flag=value form
		if strings.HasPrefix(arg, "--") {
			flagName, val, hasVal := strings.Cut(arg, "=")
			if p := byLong[flagName]; p != nil {
				if !hasVal {
					return nil, nil, fmt.Errorf("flag %s requires a value", flagName)
				}
				values[p.Key] = val
				i++
				continue
			}
		}
		extra = append(extra, arg)
		i++
	}
	return values, extra, nil
}

// ReservedShortFlags is the set of short flags consumed by parsePostScriptHelperFlags.
// -l/-u are cobra-level only and never reach script arg parsing, so they are excluded.
var ReservedShortFlags = map[string]string{
	"-c": "--cpu", "-m": "--mem", "-t": "--time", "-g": "--gpu",
	"-e": "--env", "-o": "--overlay", "-w": "--cwd",
	"-A": "--account", "-p": "--partition",
}

// ValidateHelperParamConflicts returns an error if any #PARAM: short flag clashes
// with a short flag reserved by condatainer's script-level flag parser.
func ValidateHelperParamConflicts(params []HelperParam) error {
	for _, p := range params {
		if p.ShortFlag != "" {
			if builtin, conflict := ReservedShortFlags[p.ShortFlag]; conflict {
				return fmt.Errorf(
					"helper param %q uses short flag %s which conflicts with built-in %s\n"+
						"  Fix: change the short flag in the #PARAM: header to a different letter",
					p.Key, p.ShortFlag, builtin)
			}
		}
	}
	return nil
}

// PrintRunFlags prints the flags `helper run` accepts after the helper name, in one list for every help screen.
//   - spec fills in the defaults of a helper. Nil prints the flags without them.
func PrintRunFlags(w io.Writer, spec *scheduler.ResourceSpec) {
	def := func(value string) string {
		if spec == nil {
			return ""
		}
		return fmt.Sprintf("   (default: %s)", value)
	}
	cpus, mem, timeStr := "0", "", ""
	if spec != nil {
		cpus = fmt.Sprint(spec.CpusPerTask)
		if mb := spec.GetMemPerNodeMB(); mb > 0 {
			mem = utils.FormatMemoryMB(mb)
		}
		if spec.Time > 0 {
			timeStr = utils.FormatDuration(spec.Time)
		}
	}
	fmt.Fprintln(w, "Run Flags:")
	fmt.Fprintf(w, "  -c, --cpu         CPUs per job%s\n", def(cpus))
	fmt.Fprintf(w, "  -m, --mem         Memory%s\n", def(mem))
	fmt.Fprintf(w, "  -t, --time        Walltime%s\n", def(timeStr))
	fmt.Fprintln(w, "  -g, --gpu         GPU spec (e.g. a100:1)")
	fmt.Fprintln(w, "  -e, --env         Writable overlay (.img)")
	fmt.Fprintln(w, "  -o, --overlay     Additional read-only overlay (repeatable)")
	fmt.Fprintln(w, "  -w, --cwd <path>  Change working directory (e.g. -w .)")
	fmt.Fprintln(w, "  -A, --account     Billing/allocation account")
	fmt.Fprintln(w, "  -p, --partition   Partition/queue")
	fmt.Fprintln(w, "      --exclude-nodes <a,b>  Nodes the job must not run on (empty for none)")
	fmt.Fprintln(w, "      --new         Skip reuse prompt, force new instance")
	fmt.Fprintln(w, "      --no-project  Ignore the project lock when resolving the helper's overlays")
	fmt.Fprintln(w, "      --wait        Stay attached until the app is ready")
}

// PrintHelperUsage prints the combined --help text for a helper.
// versions is the map from ParseHelperScriptMeta().ParamValues; pass nil if unavailable.
func PrintHelperUsage(scriptName string, params []HelperParam, spec *scheduler.ResourceSpec, versions map[string][]string) {
	fmt.Printf("Usage: condatainer helper run %s [flags]\n\n", scriptName)
	if spec == nil {
		spec = &scheduler.ResourceSpec{}
	}
	PrintRunFlags(os.Stdout, spec)
	if len(params) > 0 {
		fmt.Println("\nHelper Flags:")
		for _, p := range params {
			flags := ""
			if p.ShortFlag != "" && p.LongFlag != "" {
				flags = fmt.Sprintf("%s, %s", p.ShortFlag, p.LongFlag)
			} else if p.LongFlag != "" {
				flags = "    " + p.LongFlag // align --long under the --long part of "-x, --long"
			} else if p.ShortFlag != "" {
				flags = p.ShortFlag
			}
			defStr := ""
			if p.Default != "" {
				defStr = fmt.Sprintf(" (default: %s)", p.Default)
			}
			choicesStr := ""
			if versions != nil {
				if vlist := versions[p.Key]; len(vlist) > 0 {
					choicesStr = "  " + utils.StyleDim(utils.FormatChoicesInline(vlist))
				}
			}
			if flags != "" {
				fmt.Printf("  %-18s%s%s%s\n", flags, p.Description, defStr, choicesStr)
			} else {
				fmt.Printf("  %-18s%s%s%s  [prompt-only]\n", p.Key, p.Description, defStr, choicesStr)
			}
		}
	}
	fmt.Printf("\nSaved defaults: condatainer helper config %s [list | set KEY VALUE]\n", scriptName)
}
