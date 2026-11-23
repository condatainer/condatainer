package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

// tagIndexMinLookups is how many lookups make a listing of the registry's tags
// cheaper than the lookups, which cost a token and a request each.
const tagIndexMinLookups = 50

// missingWorkers is how many names checkCopies looks up at once.
const missingWorkers = 8

func newRegistryMissingCommand(opts *registryOptions) *cobra.Command {
	var arch, types string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "missing [flags] [name|pattern]...",
		Short: "List the overlays a source's registry does not hold yet",
		Long: `List the overlays whose recipe has no published copy in a source's registry.

- A copy is published when the registry has the tag of its equivalence key.
- The tag counts whatever its date. The copy itself is not read.
- A recipe that does not build on an architecture is skipped for it.
- A missing copy prints as its name and architecture.
- A repository nobody has pushed to has no copies.

What it checks:
- A name is exact, or a quoted pattern with * and ?, such as 'ubuntu24/r/*'.
- --type alone checks every recipe of those types.
- With no name, pattern or --type, this help is shown.

An unreadable registry is an error: unreachable, or refusing the read.`,
		Example: `  condatainer registry missing --source cnt --type os
  condatainer registry missing --source cnt --arch amd64,arm64 ubuntu24/base 'ubuntu24/r/*'
  condatainer registry missing --source lab 'tools/*' --type app`,
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if bareMissing(cmd, args) {
				return nil
			}
			if strings.TrimSpace(opts.source) == "" {
				return errors.New("--source is required, for example --source cnt")
			}
			return config.SelectSources([]string{opts.source})
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if bareMissing(cmd, args) {
				return cmd.Help()
			}
			archs, err := parseMissingArchs(arch)
			if err != nil {
				return err
			}
			kinds, err := parseMissingTypes(types, args, cmd.Flags().Changed("type"))
			if err != nil {
				return err
			}
			ep, err := resolveRegistryEndpoint(cmd, &registryOptions{source: opts.source})
			if err != nil {
				return err
			}
			ctx := quietly(cmd.Context())
			names, problems, err := selectRecipes(ctx, args, kinds)
			if err != nil {
				return err
			}
			if len(names)*len(archs) >= tagIndexMinLookups {
				if ctx, err = withTagIndex(ep, ctx); err != nil {
					return err
				}
			}
			copies, more, err := checkCopies(ctx, names, archs)
			if err != nil {
				return err
			}
			return writeCopies(cmd.OutOrStdout(), ep.repository, copies, append(problems, more...), asJSON)
		},
	}
	cmd.Flags().StringVarP(&opts.source, "source", "s", "", "Recipe source to read recipes and the registry from (required)")
	cmd.Flags().StringVar(&arch, "arch", "", "Architectures to check, comma separated: amd64, arm64 (default: this machine's)")
	cmd.Flags().StringVar(&types, "type", "", "Types to check, comma separated: os, app")
	jsonFlag(cmd.Flags(), &asJSON)
	_ = cmd.RegisterFlagCompletionFunc("source", sourceHandleCompletion)
	_ = cmd.RegisterFlagCompletionFunc("type", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"os", "app"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// parseMissingArchs reads --arch: a comma list of architectures, this
// machine's when empty.
func parseMissingArchs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{meta.NativeArch()}, nil
	}
	var archs []string
	for _, a := range strings.Split(raw, ",") {
		a = strings.TrimSpace(a)
		if a != registry.ArchAMD64 && a != registry.ArchARM64 {
			return nil, fmt.Errorf("--arch: %q is not amd64 or arm64", a)
		}
		if !slices.Contains(archs, a) {
			archs = append(archs, a)
		}
	}
	return archs, nil
}

// bareMissing reports a call that says what to check in no way: no name, no
// pattern and no --type. It shows the help.
func bareMissing(cmd *cobra.Command, args []string) bool {
	return len(args) == 0 && !cmd.Flags().Changed("type")
}

// isPattern reports whether an argument is a pattern, not an exact name.
func isPattern(arg string) bool { return strings.ContainsAny(arg, "*?") }

// parseMissingTypes reads --type: the types a pattern or no name selects, all
// of them when unset. --type beside an exact name is refused, since it does not
// narrow one.
func parseMissingTypes(raw string, args []string, given bool) ([]artifact.Type, error) {
	if !given {
		return nil, nil
	}
	if slices.ContainsFunc(args, func(a string) bool { return !isPattern(a) }) {
		return nil, errors.New("--type applies to patterns and to a run with no name, not to an exact name")
	}
	var kinds []artifact.Type
	for _, t := range strings.Split(raw, ",") {
		kind := artifact.Type(strings.TrimSpace(t))
		if kind == artifact.TypeData {
			return nil, errors.New("--type does not take data: a template of data overlays has too many variants to list; give a name or a pattern")
		}
		if kind != artifact.TypeOS && kind != artifact.TypeApp {
			return nil, fmt.Errorf("--type: %q is not os or app", t)
		}
		if !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds, nil
}

// patternMatcher matches names against a pattern in which * is any run of
// characters and ? is one.
func patternMatcher(pattern string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*`, ".*")
	quoted = strings.ReplaceAll(quoted, `\?`, ".")
	return regexp.MustCompile("^" + quoted + "$")
}

// selectRecipes names what to check: each exact name as given, then the recipes
// of the selected source that a pattern matches, or all of them with no name.
// Only recipes of the wanted types are listed, and a template is expanded into
// its variants. A pattern that matches nothing is a problem.
func selectRecipes(ctx context.Context, args []string, kinds []artifact.Type) ([]string, []report.Problem, error) {
	var names []string
	var patterns []string
	for _, arg := range args {
		if isPattern(arg) {
			patterns = append(patterns, arg)
		} else if name := utils.NormalizeModule(arg); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(args) > 0 && len(patterns) == 0 {
		return names, nil, nil
	}

	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return nil, nil, err
	}
	var listed []string
	for _, source := range cat {
		entries, err := source.Entries(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read the recipes of source %q: %w", source.Name, err)
		}
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			e := entries[key]
			if kinds != nil && !slices.Contains(kinds, e.Type) {
				continue
			}
			if !e.IsTemplate {
				listed = append(listed, key)
				continue
			}
			for _, v := range catalog.NewTemplate(e.TargetTemplate).Enumerate(e.PH) {
				listed = append(listed, v.Name)
			}
		}
	}

	var problems []report.Problem
	if len(patterns) == 0 {
		return appendNew(names, listed), nil, nil
	}
	for _, pattern := range patterns {
		match := patternMatcher(utils.NormalizeModule(pattern))
		before := len(names)
		for _, name := range listed {
			if match.MatchString(name) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		if len(names) == before {
			problems = append(problems, report.Problem{Name: pattern, Message: "the pattern matches no recipe"})
		}
	}
	return names, problems, nil
}

// appendNew adds the names of more that list does not have yet.
func appendNew(list, more []string) []string {
	for _, name := range more {
		if !slices.Contains(list, name) {
			list = append(list, name)
		}
	}
	return list
}

// withTagIndex reads the registry's tags once, so a tag is found or missing without
// a request. A registry that stops the listing is an error; any other failure
// leaves each name to its own lookup.
func withTagIndex(ep registryEndpoint, ctx context.Context) (context.Context, error) {
	base, repo, err := registry.SplitCoordinate(ep.repository)
	if err != nil {
		return ctx, nil
	}
	index, err := registry.ReadTagIndex(ep.ctx, base, repo)
	switch {
	case registryStopped(err):
		return ctx, err
	case err != nil:
		logging.FromContext(ctx).Debug("tags were not listed, so each copy is looked up", "err", err)
		return ctx, nil
	}
	if index.Empty() {
		utils.PrintNote("%s holds no tags yet", ep.repository)
	}
	return registry.WithTagIndex(ctx, index), nil
}

// checkCopies asks, for each name and architecture, whether the registry holds
// a copy built from the local recipe. An unreachable registry is an error; a
// name that cannot be checked is a problem and leaves the others.
//   - Names are checked at once, and the result keeps their order.
//   - The error returned is the first name's.
func checkCopies(ctx context.Context, names, archs []string) ([]report.Copy, []report.Problem, error) {
	// Only names a copy would be filed under; nothing is built here.
	dir := filepath.Join(os.TempDir(), "cnt-registry-missing")
	type checked struct {
		copies   []report.Copy
		problems []report.Problem
		err      error
	}
	results := make([]checked, len(names))
	slots := make(chan struct{}, missingWorkers)
	var wg sync.WaitGroup
	for i, name := range names {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			results[i].copies, results[i].problems, results[i].err = checkName(ctx, name, archs, dir)
		}()
	}
	wg.Wait()

	var copies []report.Copy
	var problems []report.Problem
	for _, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		copies = append(copies, r.copies...)
		problems = append(problems, r.problems...)
	}
	return copies, problems, nil
}

// checkName is checkCopies for one name.
func checkName(ctx context.Context, raw string, archs []string, dir string) ([]report.Copy, []report.Problem, error) {
	name := utils.NormalizeModule(raw)
	known, err := recipeKnown(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if !known {
		return nil, []report.Problem{{Name: name, Message: "the source has no recipe with this name"}}, nil
	}
	var copies []report.Copy
	var problems []report.Problem
	found := false
	for _, arch := range archs {
		step, err := planCopy(build.TagOnly(build.PlanOnly(build.ForArch(ctx, arch))), name, dir)
		if errors.Is(err, catalog.ErrUnsupportedArch) {
			continue
		}
		if registryStopped(err) {
			return nil, nil, err
		}
		if err != nil {
			problems = append(problems, report.Problem{Name: name, Message: err.Error()})
			found = true
			break
		}
		found = true
		c, problem, err := copyOf(step)
		if err != nil {
			return nil, nil, err
		}
		if problem != "" {
			problems = append(problems, report.Problem{Name: name, Message: problem})
			break
		}
		copies = append(copies, c)
		if step.Arch == meta.ArchNone {
			break
		}
	}
	if !found {
		problems = append(problems, report.Problem{Name: name,
			Message: fmt.Sprintf("its recipe does not build on %s", strings.Join(archs, ", "))})
	}
	return copies, problems, nil
}

// recipeKnown reports whether the selected source has a recipe called name. A
// template's own name is refused, since it names no one overlay.
func recipeKnown(ctx context.Context, name string) (bool, error) {
	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return false, err
	}
	m, found, err := cat.Lookup(ctx, name)
	if err != nil || !found {
		return false, err
	}
	if m.Entry.IsTemplate && len(m.Vars) == 0 {
		return false, fmt.Errorf("%s is a template; give the full name of one variant, as `condatainer avail -e %s` lists them", name, name)
	}
	return true, nil
}

// planCopy plans name alone, as installed nowhere, and returns its step.
func planCopy(ctx context.Context, name, dir string) (build.PlannedStep, error) {
	bo, err := build.NewBuildObject(ctx, name, false, dir, true)
	if err != nil {
		return build.PlannedStep{}, err
	}
	graph, err := build.NewBuildGraph(ctx, []*build.BuildObject{bo}, dir, false, true)
	if err != nil {
		return build.PlannedStep{}, err
	}
	for _, step := range graph.Plan() {
		if step.Name == bo.NameVersion() {
			return step, nil
		}
	}
	return build.PlannedStep{}, fmt.Errorf("%s is not in its own plan", name)
}

// copyOf reads a planned step as a published or missing copy. A step that has
// none is a problem, and an unreachable registry is an error.
func copyOf(step build.PlannedStep) (report.Copy, string, error) {
	switch {
	case step.NoRegistry != "":
		return report.Copy{}, "nothing to check: " + step.NoRegistry, nil
	case step.Unreachable:
		return report.Copy{}, "", fmt.Errorf("cannot reach %s to look for %s", step.Registry, step.Name)
	case step.Equiv.Empty():
		return report.Copy{}, "its equivalence is not known: a dependency has no key yet", nil
	}
	tag, err := registry.EquivTag(step.Name, step.Equiv, step.Arch)
	if err != nil {
		return report.Copy{}, err.Error(), nil
	}
	return report.Copy{
		Overlay:   report.Overlay{Name: step.Name, Type: step.Type, Arch: step.Arch},
		Published: step.Pull,
		Equiv:     step.Equiv,
		Tag:       tag,
		Digest:    step.Digest,
		Identity:  step.Identity,
	}, "", nil
}

// writeCopies prints the missing copies one per line, or all of them as the
// copies document. Problems go to stderr in text.
func writeCopies(w io.Writer, repository string, copies []report.Copy, problems []report.Problem, asJSON bool) error {
	if asJSON {
		return report.Write(w, &report.Copies{Repository: repository, Overlays: copies, Problems: problems})
	}
	for _, c := range copies {
		if !c.Published {
			fmt.Fprintf(w, "%s %s\n", c.Name, c.Arch)
		}
	}
	for _, p := range problems {
		utils.PrintWarning("%s: %s", p.Name, p.Message)
	}
	return nil
}

// quietly returns ctx with a logger that passes warnings only, so notes logged
// while planning do not read as this command's result.
func quietly(ctx context.Context) context.Context {
	return logging.WithLogger(ctx, slog.New(warningsOnly{logging.FromContext(ctx).Handler()}))
}
