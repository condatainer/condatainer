package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
)

type registryOptions struct {
	repository    string
	source        string
	audience      string
	arch          string
	digest        string
	prefix        string
	username      string
	password      string
	passwordStdin bool
	layer         string
	noRecipe      bool
}

// registryRemoveJSON is --json on registry remove, registry gc and project
// registry gc, which are never run together.
var registryRemoveJSON bool

var registryCmd = newRegistryCommand()

// registryEndpoint is the repository a command works on.
type registryEndpoint struct {
	// repository is the repository coordinate, host included.
	repository string
	// ctx carries the source's registry token when --source named one.
	ctx context.Context
	// source is the configured source behind --source, or nil.
	source *catalog.Source
}

type resolvedRegistryArtifact struct {
	base, repo  string
	desc        ocispec.Descriptor
	annotations map[string]string
	// name is the artifact name the address gave, or empty for a bare digest.
	name string
}

type warningsOnly struct{ slog.Handler }

func init() { rootCmd.AddCommand(registryCmd) }

func newRegistryCommand() *cobra.Command {
	opts := &registryOptions{}
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Publish overlays to a registry and install published ones",
		Long: `Publish overlays to a registry such as ghcr.io, and install them from it.

Credentials are used in this order:
1. Your login, saved by ` + "`condatainer registry login`" + `.
2. Registry tokens added with recipe sources (` + "`condatainer config source add`" + `).
3. GITHUB_TOKEN, for ghcr.io.
4. Anonymous access.

A build pulling from a source's registry tries that source's token first.
For reads, a refused credential is skipped, with a warning naming it.`,
	}

	cmd.AddCommand(newRegistryPushCommand(opts), newRegistryPullCommand(opts), newRegistryTagsCommand(opts),
		newRegistryResolveCommand(opts), newRegistryMissingCommand(opts), newRegistryRemoveCommand(opts),
		newRegistryGCCommand(opts), newRegistryLoginCommand(opts), newRegistryLogoutCommand(opts),
		newRegistryListCommand())
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// reportDone announces a finished registry operation through the command's logger,
// not straight to stderr. A transfer leaves its progress line open, and the log
// handler ends it on the first record that is not progress; a direct write would land
// on the end of that line.
func reportDone(cmd *cobra.Command, verb, subject string) {
	logging.FromContext(cmd.Context()).Info(verb+" "+subject, "kind", "success")
}

// addEndpointFlags adds the two ways to name a repository.
func addEndpointFlags(cmd *cobra.Command, opts *registryOptions) {
	cmd.Flags().StringVar(&opts.repository, "registry", "", "Registry repository to use, such as ghcr.io/my-lab/cnt")
	cmd.Flags().StringVarP(&opts.source, "source", "s", "", "Use the registry of this recipe source")
}

// addSourceOnlyFlag registers the one way to name the registry to remove from.
// A project's repository is reached with its own command, so --registry is not offered.
func addSourceOnlyFlag(cmd *cobra.Command, opts *registryOptions) {
	cmd.Flags().StringVarP(&opts.source, "source", "s", "", "Recipe source whose registry to remove from")
	_ = cmd.MarkFlagRequired("source")
}

// pushCollection is the recipe collection a push to this endpoint is matched to.
// A bare repository, and a source that names no collection, match any.
func (e registryEndpoint) pushCollection() string {
	if e.source == nil {
		return ""
	}
	return strings.TrimSpace(e.source.Desc.Source)
}

// resolveRegistryEndpoint turns --registry or --source into a repository.
//   - A source must declare a registry and the collection its artifacts come from.
//   - A project's repository is reached with --registry.
func resolveRegistryEndpoint(cmd *cobra.Command, opts *registryOptions) (registryEndpoint, error) {
	registryFlag, sourceFlag := strings.TrimSpace(opts.repository), strings.TrimSpace(opts.source)
	switch {
	case registryFlag != "" && sourceFlag != "":
		return registryEndpoint{}, fmt.Errorf("use --registry or --source, not both")
	case registryFlag != "":
		repository := registry.TrimBaseScheme(registryFlag)
		if _, _, err := registry.SplitCoordinate(repository); err != nil {
			return registryEndpoint{}, fmt.Errorf("--registry: %w", err)
		}
		return registryEndpoint{repository: repository, ctx: cmd.Context()}, nil
	case sourceFlag != "":
		cat, err := config.OpenCatalog(cmd.Context())
		if err != nil {
			return registryEndpoint{}, fmt.Errorf("cannot read the configured sources: %w", err)
		}
		return endpointOfSource(cmd.Context(), cat, sourceFlag)
	}
	return registryEndpoint{}, fmt.Errorf("--registry or --source is required (for example, --registry ghcr.io/your-lab/cnt)")
}

// endpointOfSource finds a configured source by name and reads its registry.
func endpointOfSource(ctx context.Context, cat catalog.Catalog, name string) (registryEndpoint, error) {
	for _, source := range cat {
		if source.Name != name {
			continue
		}
		return endpointFromSource(ctx, source)
	}
	return registryEndpoint{}, fmt.Errorf("no configured source is named %q", name)
}

func endpointFromSource(ctx context.Context, source *catalog.Source) (registryEndpoint, error) {
	switch {
	case source.DescriptorErr != nil:
		return registryEndpoint{}, fmt.Errorf("source %q has an invalid descriptor: %w", source.Name, source.DescriptorErr)
	case source.Err != nil || source.Stale:
		return registryEndpoint{}, fmt.Errorf("source %q is unavailable or stale", source.Name)
	case source.Desc.OCI.Registry == "":
		return registryEndpoint{}, fmt.Errorf("source %q declares no OCI registry", source.Name)
	}
	return registryEndpoint{
		repository: registry.TrimBaseScheme(source.Desc.OCI.Registry),
		ctx:        registry.WithSource(ctx, source.Base),
		source:     source,
	}, nil
}

// sourceDeclaring names the configured source whose OCI registry is repository, or "".
func sourceDeclaring(cat catalog.Catalog, repository string) string {
	for _, source := range cat {
		if strings.EqualFold(registry.TrimBaseScheme(source.Desc.OCI.Registry), repository) {
			return source.Name
		}
	}
	return ""
}

// parseRegistryAddress reads <name>, <name>@<identity> or <name>:eq-<key>.
// An identity is the same spelling the store commands take.
func parseRegistryAddress(spec, arch string) (registry.Address, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return registry.Address{}, fmt.Errorf("name is empty")
	}
	if strings.Contains(spec, "@") {
		name, query, err := splitStoreAddress(spec, "")
		if err != nil {
			return registry.Address{}, err
		}
		return registry.Address{Name: utils.NormalizeModule(name), Identity: query.SHA256, Scheme: query.Scheme, Arch: arch}, nil
	}
	if name, key, ok := strings.Cut(spec, ":eq-"); ok {
		return registry.Address{Name: utils.NormalizeModule(name), Equiv: key, Arch: arch}, nil
	}
	if strings.Contains(spec, ":") {
		return registry.Address{}, fmt.Errorf("%q is not an address; give a name, name@identity or name:eq-key", spec)
	}
	return registry.Address{Name: utils.NormalizeModule(spec), Arch: arch}, nil
}

// resolveRegistryArtifact locates the manifest an address or --digest names.
//   - A miss on this machine's architecture says which architectures are published instead.
func resolveRegistryArtifact(cmd *cobra.Command, opts *registryOptions, spec string) (resolvedRegistryArtifact, registryEndpoint, error) {
	ep, err := resolveRegistryEndpoint(cmd, opts)
	if err != nil {
		return resolvedRegistryArtifact{}, ep, err
	}
	base, repo, err := registry.SplitCoordinate(ep.repository)
	if err != nil {
		return resolvedRegistryArtifact{}, ep, err
	}
	want := registry.Want{Arch: opts.arch}
	out := resolvedRegistryArtifact{base: base, repo: repo}

	if opts.digest != "" {
		if _, err := digest.Parse(opts.digest); err != nil || !strings.HasPrefix(opts.digest, "sha256:") {
			return out, ep, fmt.Errorf("--digest %q is not a sha256:<hex> digest", opts.digest)
		}
		if strings.TrimSpace(spec) != "" {
			out.name = utils.NormalizeModule(spec)
			want.Name = out.name
		}
		out.desc, out.annotations, err = registry.ResolveArtifact(ep.ctx, base, repo, opts.digest)
		if err != nil {
			return out, ep, err
		}
	} else {
		addr, err := parseRegistryAddress(spec, opts.arch)
		if err != nil {
			return out, ep, err
		}
		found, err := registry.Lookup(ep.ctx, base, repo, addr)
		if err = registry.ExplainMiss(ep.ctx, base, repo, addr, err); err != nil {
			return out, ep, err
		}
		out.name, out.desc, out.annotations = addr.Name, found.Desc, found.Annotations
		want.Name = addr.Name
	}
	if err := registry.Check(out.annotations, want); err != nil {
		return out, ep, fmt.Errorf("cannot use %s: %w", registry.FullRef(base, repo, out.desc.Digest.String()), err)
	}
	return out, ep, nil
}

// runRemoval plans, shows, asks and applies a removal against a source's registry.
func runRemoval(cmd *cobra.Command, opts *registryOptions, plan func(registryEndpoint, string, string, registry.Backend) (registry.RemovePlan, error)) error {
	opts.repository = ""
	ep, err := resolveRegistryEndpoint(cmd, opts)
	if err != nil {
		return err
	}
	base, repo, err := registry.SplitCoordinate(ep.repository)
	if err != nil {
		return err
	}
	backend, err := registry.BackendFor(base, repo)
	if err != nil {
		return err
	}
	p, err := plan(ep, base, repo, backend)
	if err != nil {
		return err
	}
	return finishRemoval(cmd, ep.ctx, ep.repository, backend, p)
}

// finishRemoval shows a plan, asks, and carries it out.
func finishRemoval(cmd *cobra.Command, ctx context.Context, repository string, backend registry.Backend, p registry.RemovePlan) error {
	base, repo, err := registry.SplitCoordinate(repository)
	if err != nil {
		return err
	}
	if registryRemoveJSON {
		return finishRemovalJSON(cmd, ctx, repository, backend, p)
	}
	if len(p.Removes) == 0 {
		reportKept(p)
		utils.PrintMessage("Nothing to remove from %s", repository)
		return nil
	}
	ok, err := confirmRemoval(cmd, repository, p)
	if err != nil || !ok {
		return err
	}
	res, err := registry.Apply(ctx, base, repo, backend, p)
	if len(res.Removed) > 0 {
		reportDone(cmd, "removed", fmt.Sprintf("%d manifest(s) from %s", len(res.Removed), repository))
	}
	if len(res.Skipped) > 0 {
		utils.PrintWarning("Kept %d manifest(s) that changed after the list was made.", len(res.Skipped))
	}
	return err
}

func printFindings(findings []credential.Finding) {
	for _, finding := range findings {
		if finding.Warn {
			utils.PrintWarning("%s", finding.Text)
		} else {
			utils.PrintNote("%s", finding.Text)
		}
	}
}

// confirmRemoval lists what a removal does and asks. -y answers yes; without a terminal there is nobody to ask.
func confirmRemoval(cmd *cobra.Command, repository string, p registry.RemovePlan) (bool, error) {
	if !utils.ShouldAnswerYes() && !utils.IsInteractiveShell() {
		return false, fmt.Errorf("removing asks first, and there is no terminal here; run it in a terminal or pass -y")
	}
	utils.PrintMessage("Removing from %s", repository)
	if len(p.Moves) > 0 {
		utils.PrintMessage("Move %d tag(s)", len(p.Moves))
		for _, m := range p.Moves {
			utils.PrintMessage("  %s", m.Tag)
			utils.PrintMessage("    from %s", m.FromBuild)
			utils.PrintMessage("    to   %s", m.ToBuild)
		}
	}
	moved := map[string]bool{}
	for _, m := range p.Moves {
		moved[m.Tag] = true
	}
	utils.PrintMessage("Remove %d manifest(s)", len(p.Removes))
	for _, h := range p.Removes {
		if len(h.Tags) == 0 {
			utils.PrintMessage("  untagged %s", h.Desc.Digest)
			continue
		}
		var tags []string
		for _, tag := range h.Tags {
			if !moved[tag] {
				tags = append(tags, tag)
			}
		}
		utils.PrintMessage("  %s", strings.Join(tags, ", "))
	}
	reportKept(p)
	if utils.ShouldAnswerYes() {
		return true, nil
	}
	return utils.Confirm(cmd.Context(), cmd.ErrOrStderr(), "Remove? [y/N]: "), nil
}

func reportKept(p registry.RemovePlan) {
	if len(p.Kept) > 0 {
		utils.PrintWarning("Kept %d manifest(s) that hold tags this tool did not write.", len(p.Kept))
	}
}

// registryCredentials lists the logins and the registry tokens stored with
// sources, by registry, logins first. A source token's Source is its name.
func registryCredentials() ([]credential.Stored, error) {
	files := config.CredentialFiles()
	logins, err := credential.List(files, credential.Registry)
	if err != nil {
		return nil, err
	}
	tokens, err := credential.ListSourceRegistries(files)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, s := range config.ResolvedSources() {
		names[credential.TrimScheme(s.Base)] = s.Name
	}
	for i := range tokens {
		if name := names[tokens[i].Source]; name != "" {
			tokens[i].Source = name
		}
	}
	out := append(logins, tokens...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (h warningsOnly) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn && h.Handler.Enabled(ctx, l)
}

func (h warningsOnly) WithAttrs(attrs []slog.Attr) slog.Handler {
	return warningsOnly{h.Handler.WithAttrs(attrs)}
}

func (h warningsOnly) WithGroup(name string) slog.Handler {
	return warningsOnly{h.Handler.WithGroup(name)}
}

// registryStopped reports an error from the registry itself, which stops a
// push, as opposed to a check that refuses it.
func registryStopped(err error) bool {
	return errors.Is(err, registry.ErrUnavailable) || errors.Is(err, registry.ErrRateLimited) ||
		errors.Is(err, registry.ErrUnauthorized) || errors.Is(err, registry.ErrIncompatibleRegistry)
}

// finishRemovalJSON prints the removal document. Without -y it reports the plan
// and removes nothing; with -y it removes, and reports what it did.
func finishRemovalJSON(cmd *cobra.Command, ctx context.Context, repository string, backend registry.Backend, p registry.RemovePlan) error {
	base, repo, err := registry.SplitCoordinate(repository)
	if err != nil {
		return err
	}
	var problems []report.Problem
	manifests := func(held []registry.Held) report.List[report.RegistryManifest] {
		out := report.List[report.RegistryManifest]{}
		for _, h := range held {
			out = append(out, manifestRecord(ctx, base, repo, h, &problems))
		}
		return out
	}
	moves := func(moved []registry.TagMove) report.List[report.Move] {
		out := report.List[report.Move]{}
		for _, m := range moved {
			out = append(out, report.Move{Tag: m.Tag, From: m.FromBuild, To: m.ToBuild})
		}
		return out
	}
	kept := report.List[report.RegistryKept]{}
	for _, h := range p.Kept {
		kept = append(kept, report.RegistryKept{Digest: h.Desc.Digest.String(), Tags: h.Tags, Reason: report.KeptForeignTag})
	}

	if previewOnly(true) {
		return report.Write(cmd.OutOrStdout(), &report.RegistryRemovalPlan{Repository: repository,
			WouldRemove: manifests(p.Removes), WouldMove: moves(p.Moves), Kept: kept, Problems: problems})
	}
	if len(p.Removes) == 0 {
		return report.Write(cmd.OutOrStdout(), &report.RegistryRemoval{Repository: repository,
			Removed: report.List[report.RegistryManifest]{}, Moved: report.List[report.Move]{}, Kept: kept, Problems: problems})
	}
	res, applyErr := registry.Apply(ctx, base, repo, backend, p)
	for _, h := range res.Skipped {
		kept = append(kept, report.RegistryKept{Digest: h.Desc.Digest.String(), Tags: h.Tags, Reason: report.KeptChanged})
	}
	doc := &report.RegistryRemoval{Repository: repository, Removed: manifests(res.Removed), Moved: moves(res.Moved),
		Kept: kept, Problems: problems}
	if err := report.Write(cmd.OutOrStdout(), doc); err != nil {
		return err
	}
	return applyErr
}

// manifestRecord describes a manifest by its annotations, one read. When they
// cannot be read the record has the digest and tags, and a problem says why.
func manifestRecord(ctx context.Context, base, repo string, h registry.Held, problems *[]report.Problem) report.RegistryManifest {
	rec := report.RegistryManifest{Digest: h.Desc.Digest.String(), Tags: h.Tags}
	_, ann, err := registry.ResolveArtifact(ctx, base, repo, rec.Digest)
	if err != nil {
		*problems = append(*problems, report.Problem{Name: rec.Digest, Message: err.Error()})
		return rec
	}
	rec.Name, rec.Arch = ann[registry.AnnTitle], ann[registry.AnnArch]
	rec.Identity, rec.Equiv = registry.Identity(ann), registry.Equiv(ann)
	return rec
}
