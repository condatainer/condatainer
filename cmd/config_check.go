package cmd

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	checkLayer   string
	checkVerbose bool
	checkStrict  bool
	checkFix     bool
	checkKeepOld bool
	checkDryRun  bool
)

// configFinding is one thing `config check` reports.
type configFinding struct {
	Where   string // file:line, or the environment variable
	Message string
	Problem bool // a problem fails the command; the rest is information

	// Deprecated marks a name that still works but has a replacement. --strict makes it a problem.
	Deprecated bool
	// Rename is set for an old name that --fix can rewrite.
	Rename *settings.Alias
	Key    string // the key as written in the file
	Layer  string // the layer the key is in
	// Fixed is set for a rename that --fix made or, with --dry-run, would make.
	Fixed bool
}

var configCheckCmd = &cobra.Command{
	Use:   "check",
	Args:  cobra.NoArgs,
	Short: "Check the config files and environment for mistakes",
	Long: `Check the config files and environment for mistakes a command would skip silently.

It reports:
- an unknown key, with its file and line
- a value its key does not accept, or that is not true of this machine
- a renamed, deprecated or removed key, with what to use instead
- a ` + settings.EnvPrefix + `* variable that matches no key
- with -v, a value that a higher layer overrides

It exits 1 on a problem. --strict also fails on a renamed or deprecated name.

--fix rewrites renamed keys to their new names and keeps comments:
- -l is required: one layer's file is rewritten per run.
- --keep-old leaves the old name beside the new one, for older versions.
- --dry-run shows the changes and writes nothing.
` + configLayersHelp,
	Run: func(cmd *cobra.Command, args []string) {
		if checkFix && checkLayer == "" {
			ExitWithError("--fix rewrites one layer at a time: add -l user, -l extra-root or -l app-root")
		}
		layers, err := checkTargetLayers(checkLayer)
		if err != nil {
			ExitWithError("%v", err)
		}
		findings := checkConfig(layers, os.Environ())
		if checkFix {
			findings = fixRenames(findings, fixOptions{keepOld: checkKeepOld, dryRun: checkDryRun})
		}
		problems := 0
		for _, f := range findings {
			switch {
			case f.Problem || (checkStrict && f.Deprecated):
				problems++
			case !checkVerbose && !f.Deprecated && !f.Fixed:
				continue
			}
			fmt.Printf("%s: %s\n", f.Where, f.Message)
		}
		if problems > 0 {
			utils.PrintError("%d problem(s) found", problems)
			os.Exit(ExitCodeError)
		}
		utils.PrintMessage("Nothing wrong found in %d config file(s) and the environment", len(layers))
	},
}

func init() {
	configCheckCmd.Flags().StringVarP(&checkLayer, "layer", "l", "", "Check only this config layer: u/user, e/extra-root, r/app-root")
	configCheckCmd.Flags().BoolVar(&checkStrict, "strict", false, "Also fail on a renamed or deprecated name")
	configCheckCmd.Flags().BoolVar(&checkFix, "fix", false, "Rewrite renamed keys to their new names (needs -l)")
	configCheckCmd.Flags().BoolVar(&checkKeepOld, "keep-old", false, "With --fix, keep the old name beside the new one")
	configCheckCmd.Flags().BoolVar(&checkDryRun, "dry-run", false, "With --fix, show the changes and write nothing")
	configCheckCmd.Flags().BoolVarP(&checkVerbose, "verbose", "v", false, "Also show values that a higher layer overrides")
}

// checkTargetLayers returns the layers to check: the loaded ones, or the file of one named layer.
func checkTargetLayers(layer string) ([]*config.Layer, error) {
	if layer == "" {
		return config.GetConfigLayers(), nil
	}
	path, typ, err := config.ResolveReadableConfigPath(layer)
	if err != nil {
		return nil, err
	}
	if !utils.FileExists(path) {
		return nil, fmt.Errorf("%s config has no file: %s", typ, path)
	}
	l, err := config.ReadLayer(path, typ)
	if err != nil {
		return nil, err
	}
	return []*config.Layer{l}, nil
}

// checkConfig inspects layers and environment, in file order and then by variable name.
func checkConfig(layers []*config.Layer, environ []string) []configFinding {
	var out []configFinding
	for _, l := range layers {
		out = append(out, checkLayerFile(l)...)
	}
	return append(out, checkEnvironment(environ)...)
}

func checkLayerFile(l *config.Layer) []configFinding {
	var out []configFinding
	at := func(key string) string { return fmt.Sprintf("%s:%d", l.Path, l.Line(key)) }
	for _, key := range l.Keys() {
		if key == "sources" || strings.HasPrefix(key, "sources.") {
			continue
		}
		k, ok := settings.Lookup(key)
		if !ok {
			out = append(out, unregisteredKey(l, key, at(key)))
			continue
		}
		stored, err := checkValue(k, l)
		if err != nil {
			out = append(out, configFinding{Where: at(key), Message: err.Error(), Problem: true})
			continue
		}
		if stored != "" {
			if err := k.Check(stored); err != nil {
				out = append(out, configFinding{Where: at(key), Message: fmt.Sprintf("%s %s: %v", key, stored, err), Problem: true})
			}
		}
		if res, _ := settings.Resolve(key); !k.IsList() && res.Source != settings.SourceDefault && (res.Source != settings.SourceLayer || res.Layer != l.Type) {
			out = append(out, configFinding{Where: at(key), Message: fmt.Sprintf("%s in %s is overridden by %s", key, l.Type, overrider(res))})
		}
	}
	return out
}

// checkValue parses the value a layer holds for k with the key's own rules, and returns the form that would be stored.
func checkValue(k *settings.Key, l *config.Layer) (string, error) {
	if k.IsList() {
		list, _ := l.List(k.Name)
		var stored []string
		for _, e := range list {
			s, err := k.Parse(e)
			if err != nil {
				return "", err
			}
			stored = append(stored, s)
		}
		return strings.Join(stored, ","), nil
	}
	text, ok := l.Text(k.Name)
	if !ok {
		return "", fmt.Errorf("%s holds a list, not a single value", k.Name)
	}
	if text == "" {
		return "", nil
	}
	return k.Parse(text)
}

// unregisteredKey classifies a key in a file that no live key has: a renamed key, a removed one, or an unknown one.
func unregisteredKey(l *config.Layer, key, where string) configFinding {
	if k, a, ok := settings.LookupAlias(key); ok {
		f := configFinding{Where: where, Message: a.RenameMessage(), Deprecated: true, Rename: &a, Key: key, Layer: l.Type}
		if l.InConfig(k.Name) {
			if keptForOlderVersions(l, key, k, a) {
				f.Message = fmt.Sprintf("%s is kept beside %s for older versions", key, k.Name)
				f.Deprecated = false
			} else {
				f.Message += "; " + k.Name + " is also set to a different value, so this one is ignored"
			}
		}
		return f
	}
	if r, ok := settings.LookupRemoved(key); ok {
		return configFinding{Where: where, Message: r.Text() + "; its value is ignored", Problem: true}
	}
	return configFinding{Where: where, Message: fmt.Sprintf("unknown key %q", key), Problem: true}
}

// keptForOlderVersions reports whether a layer holds an old name beside the new one with the same value.
func keptForOlderVersions(l *config.Layer, old string, k *settings.Key, a settings.Alias) bool {
	if k.IsList() {
		oldList, _ := l.List(old)
		newList, _ := l.List(k.Name)
		return slices.Equal(oldList, newList)
	}
	oldText, _ := l.Text(old)
	migrated, err := a.Migrate(oldText)
	newText, _ := l.Text(k.Name)
	return err == nil && migrated == newText
}

// fixOptions says how --fix may rewrite files.
type fixOptions struct {
	keepOld bool
	dryRun  bool
}

// fixRenames rewrites each renamed key a finding names, and returns the findings with those marked fixed.
func fixRenames(findings []configFinding, opts fixOptions) []configFinding {
	for i, f := range findings {
		if f.Rename == nil {
			continue
		}
		alreadyKept := !f.Deprecated
		if alreadyKept && opts.keepOld {
			continue
		}
		keep := opts.keepOld
		if !opts.dryRun {
			path, _, _ := strings.Cut(f.Where, ":")
			if err := config.RenameConfigKey(path, f.Key, f.Rename.New, f.Rename.Migrate, keep); err != nil {
				findings[i].Message += fmt.Sprintf(" (not fixed: %v)", err)
				continue
			}
		}
		findings[i].Message = fixMessage(f.Key, f.Rename.New, keep, opts.dryRun)
		findings[i].Deprecated = false
		findings[i].Fixed = true
	}
	return findings
}

// checkEnvironment reports every CNT_CONFIG_ variable that matches no key.
func checkEnvironment(environ []string) []configFinding {
	known := map[string]bool{}
	for _, k := range settings.Keys() {
		known[settings.EnvName(k.Name)] = true
	}
	renamed := map[string]settings.Alias{}
	for _, a := range settings.AllAliases() {
		renamed[settings.EnvName(a.Old)] = a
	}
	gone := map[string]settings.RemovedKey{}
	for _, r := range settings.RemovedKeys() {
		gone[settings.EnvName(r.Name)] = r
	}
	var names []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, settings.EnvPrefix) && !known[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]configFinding, len(names))
	for i, name := range names {
		switch a, isOld := renamed[name]; {
		case isOld:
			out[i] = configFinding{Where: name, Message: fmt.Sprintf("deprecated%s; use %s", sinceWords(a.Since), settings.EnvName(a.New)), Deprecated: true}
		case gone[name].Name != "":
			out[i] = configFinding{Where: name, Message: gone[name].Text() + "; the variable is ignored", Problem: true}
		default:
			out[i] = configFinding{Where: name, Message: "no such key", Problem: true}
		}
	}
	return out
}

// overrider names what a resolved value comes from, for a message about a value it hides.
func overrider(res settings.Resolution) string {
	switch res.Source {
	case settings.SourceFlag:
		return "--" + res.Flag
	case settings.SourceEnv:
		return res.Env
	case settings.SourceLayer:
		return res.Layer
	}
	return "the default"
}

func sinceWords(v string) string {
	if v == "" {
		return ""
	}
	return " since " + v
}

// fixMessage says what --fix did, or with --dry-run would do, to one renamed key.
func fixMessage(old, new string, keep, dryRun bool) string {
	switch {
	case keep && dryRun:
		return fmt.Sprintf("%s would be copied as %s; the old name would stay", old, new)
	case keep:
		return fmt.Sprintf("copied %s as %s; the old name stays", old, new)
	case dryRun:
		return fmt.Sprintf("%s would be renamed to %s", old, new)
	}
	return fmt.Sprintf("renamed %s to %s", old, new)
}
