package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
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

var registryCmd = newRegistryCommand()

func init() { rootCmd.AddCommand(registryCmd) }

func newRegistryCommand() *cobra.Command {
	opts := &registryOptions{}
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Publish overlays to a registry and install published ones",
		Long: `Publish overlays to a registry such as ghcr.io, and install overlays published there.

Credentials are used in this order:
- Your login, saved by ` + "`condatainer registry login`" + `.
- Registry tokens added with recipe sources (` + "`condatainer config source add`" + `).
- GITHUB_TOKEN, for ghcr.io.
- Anonymous access.

A build pulling from a source's registry tries that source's token first.
For reads, a refused credential is skipped for the next one, with a warning naming it.`,
	}

	push := &cobra.Command{
		Use:   "push [flags] <path|name/version>",
		Short: "Publish an overlay to a registry",
		Long: `Publish a local overlay to a registry, so other machines can download it.

- Give a path, or the name and version of an installed overlay.
- Without --source or --registry, an installed overlay goes to its recipe source's registry.
- A new build of a name becomes the one that name installs. The older build stays.
- A snapshot is never published to a source's registry.

Registry audience, public (default) or restricted, says who can download from the registry.
- A public registry refuses overlays that may not be redistributed.
- A source's audience is "oci.audience" in its source.json. --audience is for --registry.
- It does not change who can access the registry on the host.`,
		Example: `  condatainer registry push star/2.7.11b
  condatainer registry push ./overlays/star.sqf --registry ghcr.io/my-lab/cnt
  condatainer registry push star/2.7.11b --source lab`,
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: registryPushCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			artifact, err := findRegistryArtifact(args[0])
			if err != nil {
				return err
			}
			dest, err := registryPushDestination(cmd, opts, artifact)
			if err != nil {
				return err
			}
			if err := refuseFrozenForSource(artifact.path, dest); err != nil {
				return err
			}
			_, err = registry.Publish(dest.ctx, registry.PublishRequest{
				Path: artifact.path, Repository: dest.repository, Audience: dest.audience,
				Tags:       registry.TagRequest{Name: true, Equiv: true},
				Collection: dest.collection,
				Confirm:    func(plan registry.PublishPlan) bool { return confirmPush(cmd, plan) },
			})
			if errors.Is(err, registry.ErrDeclined) {
				return nil
			}
			if err != nil {
				return err
			}
			reportDone(cmd, "published", artifact.path)
			return nil
		},
	}
	addEndpointFlags(push, opts)
	push.Flags().StringVar(&opts.audience, "audience", string(registry.Public), "Who can download: public or restricted")

	pull := &cobra.Command{
		Use:   "pull [flags] <name|name@identity>",
		Short: "Install a published overlay",
		Long: `Install a published overlay, using the copy for this machine's architecture.

- A name installs the current build of that name, such as star/2.7.11b.
- name@identity installs one particular build.
- A snapshot must be pulled with --prefix.`,
		Example: `  condatainer registry pull star/2.7.11b --registry ghcr.io/my-lab/cnt
  condatainer registry pull star/2.7.11b@9f2c1ab --source lab
  condatainer registry pull star/2.7.11b --source lab -p ./overlays/star.sqf`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryPull(cmd, opts, firstArg(args))
		},
	}
	addEndpointFlags(pull, opts)
	pull.Flags().StringVar(&opts.digest, "digest", "", "Install the copy with this digest (sha256:<hex>)")
	pull.Flags().StringVarP(&opts.prefix, "prefix", "p", "", "Install at this path (the image extension is optional)")

	resolve := &cobra.Command{
		Use:   "resolve [flags] <name|name@identity>",
		Short: "Print the digest of a published overlay without downloading it",
		Example: `  condatainer registry resolve star/2.7.11b --registry ghcr.io/my-lab/cnt
  condatainer registry resolve star/2.7.11b --source lab --arch arm64`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, _, err := resolveRegistryArtifact(cmd, opts, firstArg(args))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), resolved.desc.Digest)
			return nil
		},
	}
	addEndpointFlags(resolve, opts)
	resolve.Flags().StringVar(&opts.digest, "digest", "", "Check the copy with this digest (sha256:<hex>)")
	resolve.Flags().StringVar(&opts.arch, "arch", "", "Machine architecture: amd64, arm64 or noarch")

	tags := &cobra.Command{
		Use:   "tags [flags]",
		Short: "List what a registry holds",
		Long: `Print every tag in the registry. Each ends with its architecture.

- name: star--2.7.11b__amd64
- identity: star--2.7.11b__9f2c1ab4d3e8__amd64 (one exact build)
- equivalence: star--2.7.11b__eq-e90d3b7714cf__amd64 (newest build from the same recipe)`,
		Example: `  condatainer registry tags --source lab
  condatainer registry tags --registry ghcr.io/my-lab/cnt | grep star`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ep, err := resolveRegistryEndpoint(cmd, opts)
			if err != nil {
				return err
			}
			base, repo, err := registry.SplitCoordinate(ep.repository)
			if err != nil {
				return err
			}
			listed, err := registry.ListTags(ep.ctx, base, repo)
			if err != nil {
				return err
			}
			for _, tag := range listed {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
			}
			return nil
		},
	}
	addEndpointFlags(tags, opts)

	login := &cobra.Command{
		Use:   "login [flags] <host>[/<owner>/<repo>]",
		Short: "Verify and store registry credentials",
		Long: `Check credentials against the registry, and save them once they work.

- A repository's credential is used before its host's.
- Without --username or --password, they are asked for on the terminal.

` + configLayersHelp,
		Example: `  condatainer registry login ghcr.io -u my-user
  condatainer registry login ghcr.io/my-lab/rnaseq -u my-user --password-stdin
  condatainer registry login ghcr.io -l extra-root -u my-user`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			layer := config.NormalizeConfigLayer(opts.layer)
			if layer != "user" {
				if opts.passwordStdin && !utils.ShouldAnswerYes() {
					return fmt.Errorf("saving to the %s layer needs confirmation; pass -y to confirm when the password comes from stdin", layer)
				}
				if !confirmSharedLogin(cmd, layer) {
					return nil
				}
			}
			username := registryUsername(cmd, opts)
			password, err := registryPassword(cmd, opts)
			if err != nil {
				return err
			}
			if err := registry.Login(cmd.Context(), args[0], layer, username, password); err != nil {
				return err
			}
			reportDone(cmd, "logged in to", registry.TrimBaseScheme(args[0]))
			reportPermissions(layer)
			return nil
		},
	}
	login.Flags().StringVarP(&opts.username, "username", "u", "", "Registry username")
	login.Flags().StringVarP(&opts.password, "password", "p", "", "Registry password or token")
	login.Flags().BoolVar(&opts.passwordStdin, "password-stdin", false, "Read the password/token from stdin")
	login.Flags().StringVarP(&opts.layer, "layer", "l", "user", "Layer to save in: u/user, e/extra-root, r/cnt-root")

	logout := &cobra.Command{
		Use:   "logout [flags] <host>[/<owner>/<repo>]",
		Short: "Remove stored registry credentials",
		Long: `Remove the credential saved under exactly this host or repository.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			layer := config.NormalizeConfigLayer(opts.layer)
			if err := registry.Logout(cmd.Context(), args[0], layer); err != nil {
				if name := sourceHolding(registry.TrimBaseScheme(args[0]), layer); errors.Is(err, credential.ErrNotStored) && name != "" {
					return fmt.Errorf("%s in the %s layer is the registry token of source %s; remove it with `condatainer config source remove %s`",
						registry.TrimBaseScheme(args[0]), utils.StyleName(layer), name, name)
				}
				return err
			}
			reportDone(cmd, "logged out of", registry.TrimBaseScheme(args[0]))
			return nil
		},
	}
	logout.Flags().StringVarP(&opts.layer, "layer", "l", "user", "Layer to remove from: u/user, e/extra-root, r/cnt-root")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored registry credentials",
		Long: `List the saved registry credentials with their layer and username.

- FROM is login for a saved login, or the source a registry token was added with.
- Logins are tried before source tokens. A build from a source tries its own token first.
- Passwords and tokens are never shown.
- GITHUB_TOKEN, when set, is used for ghcr.io when none of these matches.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stored, err := registryCredentials()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(stored) == 0 {
				fmt.Fprintln(out, "No stored registry credentials.")
			} else {
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "REGISTRY\tFROM\tUSER\tLAYER\tPERMISSION")
				for _, c := range stored {
					from := "login"
					if c.Source != "" {
						from = "source " + c.Source
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Key, from, c.Username, c.Layer, c.Perm)
				}
				tw.Flush()
				reported := map[string]bool{}
				for _, c := range stored {
					if !reported[c.Path] {
						reported[c.Path] = true
						printFindings(credential.Findings(c.Layer, c.Path))
					}
				}
			}
			if os.Getenv(registry.EnvGitHubToken) != "" {
				fmt.Fprintf(out, "%s is set and applies to ghcr.io when no saved credential matches.\n", registry.EnvGitHubToken)
			}
			return nil
		},
	}

	cmd.AddCommand(push, pull, tags, resolve, newRegistryRemoveCommand(opts), newRegistryGCCommand(opts), login, logout, list)
	return cmd
}

func newRegistryRemoveCommand(opts *registryOptions) *cobra.Command {
	remove := &cobra.Command{
		Use:   "remove [flags] <name|name@identity>",
		Short: "Remove published overlays from a source's registry",
		Long: `Remove published overlays from a source's registry.

- name@identity removes one build on every architecture. A name alone removes all builds.
- If the build was the one its name installs, the name moves to the newest other build.
- A project that pins a removed build can no longer download it.`,
		Example: `  condatainer registry remove star/2.7.11b@9f2c1ab --source lab
  condatainer registry remove star/2.7.11b --source lab --arch arm64`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := parseRemoveTarget(args[0], opts.arch)
			if err != nil {
				return err
			}
			return runRemoval(cmd, opts, func(ep registryEndpoint, base, repo string, b registry.Backend) (registry.RemovePlan, error) {
				return registry.PlanRemove(ep.ctx, base, repo, b, target)
			})
		},
	}
	addSourceOnlyFlag(remove, opts)
	remove.Flags().StringVar(&opts.arch, "arch", "", "Only this machine architecture: amd64, arm64 or noarch")
	return remove
}

func newRegistryGCCommand(opts *registryOptions) *cobra.Command {
	gc := &cobra.Command{
		Use:   "gc [flags] [name]",
		Short: "Remove builds no name installs from a source's registry",
		Long: `Remove builds that no name installs from a source's registry.

- A build stays while its name installs it, or while it is the newest built from its recipe.
- A name limits the run to that name.
- A project that pins a removed build can no longer download it.`,
		Example: `  condatainer registry gc --source lab
  condatainer registry gc star/2.7.11b --source lab`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := catalog.Normalize(firstArg(args))
			if opts.noRecipe {
				// The recipe index is cached, and a stale one would call a new recipe missing.
				if err := config.RefreshCatalogCache(); err != nil {
					return err
				}
			}
			return runRemoval(cmd, opts, func(ep registryEndpoint, _, _ string, b registry.Backend) (registry.RemovePlan, error) {
				var hasRecipe func(string) bool
				if opts.noRecipe {
					var err error
					if hasRecipe, err = recipeCheck(ep); err != nil {
						return registry.RemovePlan{}, err
					}
				}
				return registry.PlanGC(ep.ctx, b, name, hasRecipe)
			})
		},
	}
	addSourceOnlyFlag(gc, opts)
	gc.Flags().BoolVar(&opts.noRecipe, "no-recipe", false, "Also remove builds of names the source has no recipe for")
	return gc
}

// recipeCheck reports whether the source has a recipe for a name.
// It refuses when the source cannot say, since every name would look missing.
func recipeCheck(ep registryEndpoint) (func(string) bool, error) {
	source := ep.source
	if source == nil {
		return nil, fmt.Errorf("--no-recipe needs a source")
	}
	entries, err := source.Entries(ep.ctx)
	switch {
	case err != nil:
		return nil, fmt.Errorf("cannot read the recipes of source %s, so none can be called missing: %w", source.Name, err)
	case source.Stale:
		return nil, fmt.Errorf("source %s could not be refreshed, so its recipes may be out of date", source.Name)
	case len(entries) == 0:
		return nil, fmt.Errorf("source %s lists no recipes, so every build would be removed", source.Name)
	}
	only := catalog.Catalog{source}
	return func(name string) bool {
		_, found, _ := only.Lookup(ep.ctx, name)
		return found
	}, nil
}

// firstArg is the one optional argument, or empty.
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

func registryPushCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return overlayChoices(true, false, false, toComplete)
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

// registryEndpoint is the repository a command works on.
type registryEndpoint struct {
	// repository is the repository coordinate, host included.
	repository string
	// ctx carries the source's registry token when --source named one.
	ctx context.Context
	// source is the configured source behind --source, or nil.
	source *catalog.Source
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

// pushDestination is where a push goes and under what policy.
type pushDestination struct {
	repository string
	audience   registry.Audience
	collection string
	fromSource bool
	ctx        context.Context
}

// refuseFrozenForSource refuses a frozen environment bound for a source's registry.
// A frozen environment belongs to its project, so a source never holds one.
func refuseFrozenForSource(path string, dest pushDestination) error {
	if !dest.fromSource {
		return nil
	}
	manifest, err := meta.ReadManifest(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	return refuseFrozen(manifest, path)
}

func refuseFrozen(m meta.Manifest, path string) error {
	if m.BuildType == meta.BuildTypeSnapshot {
		return fmt.Errorf("%s is a snapshot: it belongs to its project, so it is never published to a source's registry", path)
	}
	return nil
}

// requirePrefixForFrozen refuses a frozen environment pulled without --prefix.
// Its project addresses it by path, so it is never filed under a name.
func requirePrefixForFrozen(annotations map[string]string, prefix string) error {
	if prefix == "" && annotations[registry.AnnIdentityScheme] == string(key.PayloadTreeV1) {
		return fmt.Errorf("a snapshot must be pulled with --prefix")
	}
	return nil
}

// registryPushDestination resolves the publishing repository and its policy.
//
//   - --registry or --source wins.
//   - Otherwise the artifact's recorded build.source must match exactly one configured source, whose registry it is.
//   - Only an installed name/version infers. A file is where the destination is least likely to be the recipe collection's, so inferring would publish somewhere nobody named.
func registryPushDestination(cmd *cobra.Command, opts *registryOptions, artifact registryArtifact) (pushDestination, error) {
	if strings.TrimSpace(opts.repository) != "" || strings.TrimSpace(opts.source) != "" {
		ep, err := resolveRegistryEndpoint(cmd, opts)
		if err != nil {
			return pushDestination{}, err
		}
		audience := opts.audience
		if ep.source != nil {
			audience = ep.source.Desc.OCI.Audience
		}
		parsed, err := pushAudience(cmd, ep.source, audience)
		if err != nil {
			return pushDestination{}, err
		}
		return pushDestination{repository: ep.repository, audience: parsed, collection: ep.pushCollection(), fromSource: ep.source != nil, ctx: ep.ctx}, nil
	}
	if !artifact.managed {
		return pushDestination{}, fmt.Errorf("cannot infer registry for %s: only an installed name/version infers its destination; use --registry or --source", artifact.path)
	}

	manifest, err := meta.ReadManifest(artifact.path)
	if err != nil {
		return pushDestination{}, fmt.Errorf("cannot infer registry from %s: cannot read the overlay's provenance: %w; use --registry or --source", artifact.path, err)
	}
	repository := strings.TrimRight(strings.TrimSpace(manifest.Build.Source), "/")
	if repository == "" {
		return pushDestination{}, fmt.Errorf("cannot infer registry: the overlay records no build source; use --registry or --source")
	}
	cat, err := config.OpenCatalog(cmd.Context())
	if err != nil {
		return pushDestination{}, fmt.Errorf("cannot infer registry from build source %q: %w; use --registry or --source", repository, err)
	}

	source, err := inferPushSource(repository, cat)
	if err != nil {
		return pushDestination{}, err
	}
	ep, err := endpointFromSource(cmd.Context(), source)
	if err != nil {
		return pushDestination{}, err
	}

	parsed, err := pushAudience(cmd, source, source.Desc.OCI.Audience)
	if err != nil {
		return pushDestination{}, err
	}
	return pushDestination{repository: ep.repository, audience: parsed, collection: ep.pushCollection(), fromSource: ep.source != nil, ctx: ep.ctx}, nil
}

// pushAudience parses the audience a push is checked against.
// A source declares its own, so --audience applies only to a push by --registry.
func pushAudience(cmd *cobra.Command, source *catalog.Source, audience string) (registry.Audience, error) {
	if source != nil && cmd.Flags().Changed("audience") {
		return "", fmt.Errorf("--audience applies to --registry; source %s declares its own", source.Name)
	}
	return parseAudience(audience)
}

func inferPushSource(repository string, cat catalog.Catalog) (*catalog.Source, error) {
	repository = strings.TrimRight(strings.TrimSpace(repository), "/")
	var matches []*catalog.Source
	for _, source := range cat {
		if strings.TrimRight(strings.TrimSpace(source.Desc.Source), "/") == repository {
			matches = append(matches, source)
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("cannot infer registry: build source %q matches %d configured sources; use --registry or --source", repository, len(matches))
	}
	source := matches[0]
	if source.DescriptorErr != nil {
		return nil, fmt.Errorf("cannot infer registry from source %q: invalid source descriptor: %w; use --registry or --source", source.Name, source.DescriptorErr)
	}
	if source.Err != nil || source.Stale {
		return nil, fmt.Errorf("cannot infer registry from source %q: source metadata is unavailable or stale; use --registry or --source", source.Name)
	}
	if source.Desc.OCI.Registry == "" {
		return nil, fmt.Errorf("cannot infer registry: source %q declares no OCI registry; use --registry or --source", source.Name)
	}
	return source, nil
}

func parseAudience(raw string) (registry.Audience, error) {
	v := registry.Audience(strings.ToLower(strings.TrimSpace(raw)))
	if v != registry.Public && v != registry.Restricted {
		return "", fmt.Errorf("invalid audience %q: want public or restricted", raw)
	}
	return v, nil
}

// registryArtifact is a local artifact and how the user addressed it.
type registryArtifact struct {
	path string
	// managed reports that the argument was a name/version found in an images
	// directory, rather than a path pointed at. It decides only whether the
	// destination may be inferred: what is published is read from the artifact
	// either way.
	managed bool
}

func findRegistryArtifact(arg string) (registryArtifact, error) {
	if info, err := os.Stat(arg); err == nil && !info.IsDir() {
		return registryArtifact{path: arg}, nil
	}
	filename := strings.ReplaceAll(catalog.Normalize(arg), "/", "--")
	for _, dir := range config.GetImageSearchPaths() {
		for _, ext := range []string{".sqf"} {
			candidate := filepath.Join(dir, filename+ext)
			if utils.FileExists(candidate) {
				return registryArtifact{path: candidate, managed: true}, nil
			}
		}
	}
	return registryArtifact{}, fmt.Errorf("no local overlay found for %q; give its full name/version, such as ubuntu24/base, or a path", arg)
}

type resolvedRegistryArtifact struct {
	base, repo  string
	desc        ocispec.Descriptor
	annotations map[string]string
	// name is the artifact name the address gave, or empty for a bare digest.
	name string
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
		return registry.Address{Name: catalog.Normalize(name), Identity: query.SHA256, Scheme: query.Scheme, Arch: arch}, nil
	}
	if name, key, ok := strings.Cut(spec, ":eq-"); ok {
		return registry.Address{Name: catalog.Normalize(name), Equiv: key, Arch: arch}, nil
	}
	if strings.Contains(spec, ":") {
		return registry.Address{}, fmt.Errorf("%q is not an address; give a name, name@identity or name:eq-key", spec)
	}
	return registry.Address{Name: catalog.Normalize(spec), Arch: arch}, nil
}

// parseRemoveTarget reads "name" or "name@identity".
func parseRemoveTarget(spec, arch string) (registry.RemoveTarget, error) {
	addr, err := parseRegistryAddress(spec, arch)
	if err != nil {
		return registry.RemoveTarget{}, err
	}
	if addr.Equiv != "" {
		return registry.RemoveTarget{}, fmt.Errorf("%q is not a build; give a name or name@identity", spec)
	}
	return registry.RemoveTarget{Name: addr.Name, Identity: addr.Identity, Arch: arch}, nil
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
			out.name = catalog.Normalize(spec)
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

func runRegistryPull(cmd *cobra.Command, opts *registryOptions, spec string) error {
	resolved, ep, err := resolveRegistryArtifact(cmd, opts, spec)
	if err != nil {
		return err
	}
	if err := requirePrefixForFrozen(resolved.annotations, opts.prefix); err != nil {
		return err
	}
	dest, err := registryPullDestination(opts.prefix, resolved.name, resolved.annotations[registry.AnnTitle])
	if err != nil {
		return err
	}
	if err := registry.Pull(ep.ctx, resolved.base, resolved.repo, resolved.desc, resolved.annotations, dest); err != nil {
		return err
	}
	reportDone(cmd, "pulled", dest)
	return nil
}

// pullExt is what every pull lands as: an overlay is SquashFS, so the
// destination never varies by what was published.
const pullExt = ".sqf"

// registryPullDestination picks where a pull installs. --prefix names the path
// and gains the extension when it has none; otherwise the artifact's name, or the
// one it was published as when only a digest was given, goes into the writable
// images directory.
func registryPullDestination(prefix, name, title string) (string, error) {
	if prefix != "" {
		if !utils.IsSqf(filepath.Base(prefix)) {
			prefix += pullExt
		}
		return prefix, nil
	}

	if name == "" {
		name = catalog.Normalize(title)
	}
	if name == "" {
		return "", fmt.Errorf("cannot choose an install name from this address; use --prefix")
	}
	dir, err := config.GetWritableImagesDir()
	if err != nil {
		return "", fmt.Errorf("no writable images directory: %w", err)
	}
	return filepath.Join(dir, strings.ReplaceAll(name, "/", "--")+pullExt), nil
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

// registryUsername is --username, or one typed on the terminal when it is
// omitted. Anything else is left empty, and the registry refuses it.
func registryUsername(cmd *cobra.Command, opts *registryOptions) string {
	if opts.username != "" {
		return opts.username
	}
	if file, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Username: ")
		name, _ := utils.ReadLineContext(cmd.Context())
		return name
	}
	return ""
}

func registryPassword(cmd *cobra.Command, opts *registryOptions) (string, error) {
	if opts.passwordStdin && opts.password != "" {
		return "", fmt.Errorf("--password and --password-stdin are mutually exclusive")
	}
	if opts.passwordStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("cannot read password from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if opts.password != "" {
		return opts.password, nil
	}
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", fmt.Errorf("no password provided; use --password or --password-stdin")
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Password/Token: ")
	password, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("cannot read password: %w", err)
	}
	return string(password), nil
}

// confirmSharedLogin warns that a credential saved outside the user's own layer
// can be read by anyone who can read that file, and asks to go on. -y answers yes.
func confirmSharedLogin(cmd *cobra.Command, layer string) bool {
	utils.PrintWarning("The credential is saved in the %s layer: anyone who can read that file can use it. Use a read-only token.", layer)
	utils.PrintNote("The file is written 0600, or 0660 when its directory is group-writable.")
	if utils.ShouldAnswerYes() {
		return true
	}
	return utils.Confirm(cmd.Context(), cmd.ErrOrStderr(), "Continue? [y/N]: ")
}

// reportPermissions tells the person who just saved a credential if the file
// can be reached by more people than it should.
func reportPermissions(layer string) {
	if file, err := config.CredentialFile(layer); err == nil {
		printFindings(credential.Findings(layer, file.Path))
	}
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

// confirmPush shows what a push is about to do and asks to go on. -y answers yes.
func confirmPush(cmd *cobra.Command, plan registry.PublishPlan) bool {
	utils.PrintMessage("Publish %s (%s)", plan.Name, utils.FormatSize(plan.Size))
	utils.PrintMessage("  to %s (%s)", plan.Reference, plan.Audience)
	if plan.Existing {
		utils.PrintMessage("  already published: nothing is uploaded")
	} else {
		utils.PrintMessage("  %d layer(s) of up to %s", plan.Layers, utils.FormatSize(plan.LayerSize))
	}
	if len(plan.Tags) > 1 {
		utils.PrintMessage("  tags %s", strings.Join(plan.Tags, ", "))
	}
	if plan.MovedTag != "" {
		utils.PrintWarning("Moves %s off build %s.", plan.MovedTag, plan.MovedFrom)
	}
	if mismatch := plan.Mismatch; mismatch != nil {
		recorded := "no recipe collection"
		if mismatch.Recorded != "" {
			recorded = "the collection " + mismatch.Recorded
		}
		utils.PrintWarning("This overlay records %s, but the repository belongs to %s.", recorded, mismatch.Expected)
		utils.PrintNote("A package links to one code repository.")
	}
	if utils.ShouldAnswerYes() {
		return true
	}
	if plan.Mismatch != nil {
		return utils.Confirm(cmd.Context(), cmd.ErrOrStderr(), "Push anyway? [y/N]: ")
	}
	return utils.ConfirmDefaultYes(cmd.Context(), cmd.ErrOrStderr(), "Push? [Y/n]: ")
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

// sourceHolding names the source whose registry token is stored under key in
// layer, or "".
func sourceHolding(key, layer string) string {
	stored, err := registryCredentials()
	if err != nil {
		return ""
	}
	for _, c := range stored {
		if c.Source != "" && c.Key == key && c.Layer == layer {
			return c.Source
		}
	}
	return ""
}
