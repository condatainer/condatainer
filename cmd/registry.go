package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
)

type registryOptions struct {
	base          string
	audience      string
	force         bool
	prefix        string
	username      string
	password      string
	passwordStdin bool
	layer         string
}

var registryCmd = newRegistryCommand()

func init() { rootCmd.AddCommand(registryCmd) }

func newRegistryCommand() *cobra.Command {
	opts := &registryOptions{}
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Publish and fetch artifacts through an OCI registry",
		Long: `Publish and fetch read-only .sqf artifacts through an OCI registry such as ghcr.io.

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
		Short: "Publish a local artifact",
		Long: `Publish one local overlay or base image, given by path or by installed name/version.

- --registry says where it goes.
- An installed name/version can find it from the recipe source it was built from.
- A push the endpoint's audience or the recipe's redistribution terms do not allow is refused.
- It shows the destination and asks before uploading. -y skips the question.`,
		Example: `  condatainer registry push star/2.7.11b
  condatainer registry push ./overlays/star.sqf --registry ghcr.io/my-lab/cnt`,
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: registryPushCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			artifact, err := findRegistryArtifact(args[0])
			if err != nil {
				return err
			}
			base, audience, err := registryPushDestination(cmd, opts, artifact)
			if err != nil {
				return err
			}
			_, err = registry.Publish(cmd.Context(), registry.PublishRequest{
				Path: artifact.path, Base: base, Audience: audience, Force: opts.force,
				Confirm: func(plan registry.PublishPlan) bool { return confirmPush(cmd, plan) },
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
	push.Flags().StringVar(&opts.base, "registry", "", "Registry base, including owner/prefix (inferred from the recipe source when omitted)")
	push.Flags().StringVar(&opts.audience, "audience", string(registry.Public), "What a push may publish here: public or restricted")
	push.Flags().BoolVarP(&opts.force, "force", "f", false, "Replace the existing published version for this platform")

	pull := &cobra.Command{
		Use:   "pull [flags] <name/version|repository:tag|repository@digest>",
		Short: "Install an exact published artifact",
		Long: `Download one published artifact and install it.

- The address must be exact. The copy for this machine's platform is taken.
- It is installed under the name it was published as, unless --prefix says otherwise.
- To pick a version rather than name one, use 'condatainer create'.`,
		Example: `  condatainer registry pull star/2.7.11b --registry ghcr.io/my-lab/cnt
  condatainer registry pull star/2.7.11b --registry ghcr.io/my-lab/cnt -p ./overlays/star.sqf`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryPull(cmd, opts, args[0])
		},
	}
	pull.Flags().StringVar(&opts.base, "registry", "", "Registry base, including owner/prefix (required)")
	pull.Flags().StringVarP(&opts.prefix, "prefix", "p", "", "Install at this path (the image extension is optional)")

	resolve := &cobra.Command{
		Use:          "resolve [flags] <name/version|repository:tag|repository@digest>",
		Short:        "Print the digest an address resolves to",
		Long:         `Print the digest this machine would pull for an address. Nothing is downloaded.`,
		Example:      `  condatainer registry resolve star/2.7.11b --registry ghcr.io/my-lab/cnt`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := requireRegistryBase(opts.base)
			if err != nil {
				return err
			}
			resolved, err := resolveRegistryArtifact(cmd, base, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), resolved.desc.Digest)
			return nil
		},
	}
	resolve.Flags().StringVar(&opts.base, "registry", "", "Registry base, including owner/prefix (required)")

	tags := &cobra.Command{
		Use:          "tags [flags] <repository>",
		Short:        "List the tags published for a repository",
		Long:         `List every tag in a repository, to see which versions are published.`,
		Example:      `  condatainer registry tags star --registry ghcr.io/my-lab/cnt`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := requireRegistryBase(opts.base)
			if err != nil {
				return err
			}
			repo := strings.Trim(catalog.Normalize(args[0]), "/")
			if repo == "" || strings.ContainsAny(repo, ":@") {
				return fmt.Errorf("tags needs an OCI repository path, got %q", args[0])
			}
			listed, err := registry.ListTags(cmd.Context(), base, repo)
			if err != nil {
				return err
			}
			for _, tag := range listed {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
			}
			return nil
		},
	}
	tags.Flags().StringVar(&opts.base, "registry", "", "Registry base, including owner/prefix (required)")

	login := &cobra.Command{
		Use:   "login [flags] <host>[/<owner>/<repo>]",
		Short: "Verify and store registry credentials",
		Long: `Check credentials against the registry, and save them once they work.

- Give a host or a repository (host/owner/repo).
- A repository's credential is used before its host's.
- -l chooses which config layer keeps the credential. The default is the user layer.
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
	login.Flags().StringVarP(&opts.layer, "layer", "l", "user", "Config layer to save in: u/user, e/extra-root, r/app-root")

	logout := &cobra.Command{
		Use:   "logout [flags] <host>[/<owner>/<repo>]",
		Short: "Remove stored registry credentials",
		Long: `Remove the credential saved under exactly this host or repository.

- -l chooses the config layer to remove it from.
- GITHUB_TOKEN is not affected.`,
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
	logout.Flags().StringVarP(&opts.layer, "layer", "l", "user", "Config layer to remove from: u/user, e/extra-root, r/app-root")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored registry credentials",
		Long: `List the saved registry credentials with their layer and username.

- FROM is login for one saved by ` + "`condatainer registry login`" + `, or the source a registry token was added with.
- For a registry, logins are tried before source tokens. A build from a source tries that source's token first.
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

	cmd.AddCommand(push, pull, tags, resolve, login, logout, list)
	return cmd
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

// registryPushDestination resolves the publishing endpoint and its policy.
//
//   - Explicit flags win.
//   - Otherwise the artifact's recorded build.source must match exactly one configured source, whose registry it is.
//   - Only an installed name/version infers. A file is where the destination is least likely to be the recipe collection's, so inferring would publish somewhere nobody named.
func registryPushDestination(cmd *cobra.Command, opts *registryOptions, artifact registryArtifact) (string, registry.Audience, error) {
	if strings.TrimSpace(opts.base) != "" {
		base, err := requireRegistryBase(opts.base)
		if err != nil {
			return "", "", err
		}
		audience, err := parseAudience(opts.audience)
		return base, audience, err
	}
	if !artifact.managed {
		return "", "", fmt.Errorf("cannot infer registry for %s: only an installed name/version infers its destination; use --registry", artifact.path)
	}

	manifest, err := meta.ReadManifest(artifact.path)
	if err != nil {
		return "", "", fmt.Errorf("cannot infer registry from %s: cannot read artifact provenance: %w; use --registry", artifact.path, err)
	}
	repository := strings.TrimRight(strings.TrimSpace(manifest.Build.Source), "/")
	if repository == "" {
		return "", "", fmt.Errorf("cannot infer registry: artifact records no build source; use --registry")
	}
	cat, err := config.OpenCatalog(cmd.Context())
	if err != nil {
		return "", "", fmt.Errorf("cannot infer registry from build source %q: %w; use --registry", repository, err)
	}

	source, err := inferPushSource(repository, cat)
	if err != nil {
		return "", "", err
	}

	audience := source.Desc.OCI.Audience
	if cmd.Flags().Changed("audience") {
		audience = opts.audience
	}
	parsed, err := parseAudience(audience)
	if err != nil {
		return "", "", err
	}
	return source.Desc.OCI.Registry, parsed, nil
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
		return nil, fmt.Errorf("cannot infer registry: build source %q matches %d configured sources; use --registry", repository, len(matches))
	}
	source := matches[0]
	if source.DescriptorErr != nil {
		return nil, fmt.Errorf("cannot infer registry from source %q: invalid source descriptor: %w; use --registry", source.Name, source.DescriptorErr)
	}
	if source.Err != nil || source.Stale {
		return nil, fmt.Errorf("cannot infer registry from source %q: source metadata is unavailable or stale; use --registry", source.Name)
	}
	if source.Desc.OCI.Registry == "" {
		return nil, fmt.Errorf("cannot infer registry: source %q declares no OCI registry; use --registry", source.Name)
	}
	return source, nil
}

func requireRegistryBase(base string) (string, error) {
	if base = registry.TrimBaseScheme(base); base == "" {
		return "", fmt.Errorf("--registry is required (for example, ghcr.io/your-lab/condatainer)")
	}
	return base, nil
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
	return registryArtifact{}, fmt.Errorf("no local .sqf artifact found for %q; give its full name/version, such as ubuntu24/base, or a path", arg)
}

type resolvedRegistryArtifact struct {
	repo, reference string
	desc            ocispec.Descriptor
	annotations     map[string]string
}

func resolveRegistryArtifact(cmd *cobra.Command, base, spec string) (resolvedRegistryArtifact, error) {
	name, selector, err := registry.SplitPullSpec(spec)
	if err != nil {
		return resolvedRegistryArtifact{}, err
	}
	if name == "" {
		return resolvedRegistryArtifact{}, fmt.Errorf("artifact address is empty")
	}

	type candidate struct{ repo, reference string }
	var candidates []candidate
	if selector != "" {
		candidates = append(candidates, candidate{name, selector})
	} else {
		if !strings.Contains(name, "/") {
			return resolvedRegistryArtifact{}, fmt.Errorf("%q is a bare name; use create for version selection or give registry pull an exact address", spec)
		}
		if i := strings.LastIndex(name, "/"); i > 0 {
			candidates = append(candidates, candidate{name[:i], name[i+1:]})
		}
		candidates = append(candidates, candidate{name, registry.RollingTag})
	}

	var lastErr error
	for _, candidate := range candidates {
		desc, ann, err := registry.ResolveArtifact(cmd.Context(), base, candidate.repo, candidate.reference)
		if err != nil {
			lastErr = err
			continue
		}
		return resolvedRegistryArtifact{candidate.repo, candidate.reference, desc, ann}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no registry address could be derived from %q", spec)
	}
	return resolvedRegistryArtifact{}, lastErr
}

func runRegistryPull(cmd *cobra.Command, opts *registryOptions, spec string) error {
	base, err := requireRegistryBase(opts.base)
	if err != nil {
		return err
	}
	resolved, err := resolveRegistryArtifact(cmd, base, spec)
	if err != nil {
		return err
	}
	if err := registry.Check(resolved.annotations, registry.Want{}); err != nil {
		return fmt.Errorf("cannot pull %s: %w", registry.FullRef(base, resolved.repo, resolved.reference), err)
	}
	name, selector, _ := registry.SplitPullSpec(spec)
	dest, err := registryPullDestination(opts.prefix, name, selector, resolved.annotations[registry.AnnTitle])
	if err != nil {
		return err
	}
	if err := registry.Pull(cmd.Context(), base, resolved.repo, resolved.desc, resolved.annotations, dest); err != nil {
		return err
	}
	reportDone(cmd, "pulled", dest)
	return nil
}

// pullExt is what every pull lands as: an overlay is SquashFS, so the
// destination never varies by what was published.
const pullExt = ".sqf"

// registryPullDestination picks where a pull installs. --prefix names the path
// and gains the extension when it has none; otherwise the name the artifact was
// published under goes into the writable images directory.
func registryPullDestination(prefix, addressName, selector, title string) (string, error) {
	if prefix != "" {
		if !utils.IsSqf(filepath.Base(prefix)) {
			prefix += pullExt
		}
		return prefix, nil
	}

	name := addressPlacementName(addressName, selector, title)
	if name == "" {
		return "", fmt.Errorf("cannot choose an install name from this address; use --prefix")
	}
	dir, err := config.GetWritableImagesDir()
	if err != nil {
		return "", fmt.Errorf("no writable images directory: %w", err)
	}
	return filepath.Join(dir, strings.ReplaceAll(name, "/", "--")+pullExt), nil
}

func addressPlacementName(name, selector, title string) string {
	name = catalog.Normalize(name)
	title = catalog.Normalize(title)
	if selector == "" {
		return name
	}
	// A project publishes every artifact into one repository with the name in
	// the tag, so composing repository and tag the catalog way would install
	// star/2.7.11b as cnt/star--2.7.11b. Decoding is more specific than that
	// composition rather than a fallback to the payload, and it cannot be
	// confused with a catalog tag, which is a single version segment and never
	// contains `--`.
	if decoded, _, ok := registry.ParseProjectTag(selector); ok {
		return decoded
	}
	if strings.HasPrefix(selector, "sha256:") {
		if title == name {
			return name
		}
		return title
	}
	if title == name {
		return name
	}
	return name + "/" + selector
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
	utils.PrintMessage("  %d layer(s) of up to %s", plan.Layers, utils.FormatSize(plan.LayerSize))
	if len(plan.Tags) > 1 {
		utils.PrintMessage("  tags %s", strings.Join(plan.Tags, ", "))
	}
	if plan.Replace {
		utils.PrintWarning("--force replaces this platform at an existing versioned tag.")
	}
	if utils.ShouldAnswerYes() {
		return true
	}
	return utils.Confirm(cmd.Context(), cmd.ErrOrStderr(), "Push? [y/N]: ")
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
