package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/condatainer/condatainer/internal/utils/units"
	"github.com/spf13/cobra"
)

// pushDestination is where a push goes and under what policy.
type pushDestination struct {
	repository string
	audience   registry.Audience
	collection string
	fromSource bool
	ctx        context.Context
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

func newRegistryPushCommand(opts *registryOptions) *cobra.Command {
	var pushJSON bool
	push := &cobra.Command{
		Use:   "push [flags] <path|name/version>",
		Short: "Publish an overlay to a registry",
		Long: `Publish a local overlay to a registry, so other machines can download it.

- Give a path, or the name and version of an installed overlay.
- By default an installed overlay goes to its recipe source's registry.
- A new build of a name becomes the one it installs. The older build stays.
- A snapshot is never published to a source's registry.

Audience:
- A public registry refuses overlays that may not be redistributed.
- A source's audience is "oci.audience" in its source.json.
- --audience is for --registry.
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
			if pushJSON {
				return runPushJSON(cmd, opts, artifact)
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
	jsonFlag(push.Flags(), &pushJSON)
	addEndpointFlags(push, opts)
	push.Flags().StringVar(&opts.audience, "audience", string(registry.Public), "Declare who can download from the registry: public or restricted")
	return push
}

func registryPushCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return overlayChoices(false, false, toComplete)
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

func findRegistryArtifact(arg string) (registryArtifact, error) {
	if info, err := os.Stat(arg); err == nil && !info.IsDir() {
		return registryArtifact{path: arg}, nil
	}
	filename := strings.ReplaceAll(utils.NormalizeModule(arg), "/", "--")
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

// confirmPush shows what a push is about to do and asks to go on. -y answers yes.
func confirmPush(cmd *cobra.Command, plan registry.PublishPlan) bool {
	utils.PrintMessage("Publish %s (%s)", plan.Name, units.FormatSize(plan.Size))
	utils.PrintMessage("  to %s (%s)", plan.Reference, plan.Audience)
	if plan.Existing {
		utils.PrintMessage("  already published: nothing is uploaded")
	} else {
		utils.PrintMessage("  %d layer(s) of up to %s", plan.Layers, units.FormatSize(plan.LayerSize))
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

// runPushJSON prints the pushes document. Without -y it only reports what the
// push would do, and with -y it pushes. A refusal before anything moved is
// outcome refused, and a push that started and stopped is failed; both print
// the record and end the command with an error.
func runPushJSON(cmd *cobra.Command, opts *registryOptions, artifact registryArtifact) error {
	rec := report.Push{Path: artifact.path}
	var problems []report.Problem
	finish := func(failure error) error {
		doc := &report.Pushes{Pushes: report.List[report.Push]{rec}, Problems: problems}
		if err := report.Write(cmd.OutOrStdout(), doc); err != nil {
			return err
		}
		return failure
	}
	refuse := func(err error) error {
		rec.Outcome, rec.Error = report.OutcomeRefused, err.Error()
		return finish(err)
	}

	dest, err := registryPushDestination(cmd, opts, artifact)
	if err != nil {
		return refuse(err)
	}
	if err := refuseFrozenForSource(artifact.path, dest); err != nil {
		return refuse(err)
	}
	prepared, err := registry.Prepare(dest.ctx, registry.PublishRequest{
		Path: artifact.path, Repository: dest.repository, Audience: dest.audience,
		Tags:       registry.TagRequest{Name: true, Equiv: true},
		Collection: dest.collection,
	})
	if err != nil {
		if registryStopped(err) {
			rec.Outcome, rec.Error = report.OutcomeFailed, err.Error()
			return finish(err)
		}
		return refuse(err)
	}
	rec = pushRecord(artifact.path, prepared.Plan)
	if m := prepared.Plan.Mismatch; m != nil {
		recorded := "no recipe collection"
		if m.Recorded != "" {
			recorded = "the collection " + m.Recorded
		}
		problems = append(problems, report.Problem{Name: rec.Name,
			Message: fmt.Sprintf("this overlay records %s, but the repository belongs to %s", recorded, m.Expected)})
	}

	if previewOnly(true) {
		return finish(nil)
	}
	desc, err := prepared.Publish(dest.ctx)
	if err != nil {
		rec.Outcome, rec.Error = report.OutcomeFailed, err.Error()
		return finish(err)
	}
	rec.Outcome, rec.Digest = report.OutcomeDone, desc.Digest.String()
	return finish(nil)
}

// pushRecord is the record of a prepared push.
func pushRecord(path string, plan registry.PublishPlan) report.Push {
	rec := report.Push{
		Overlay:    report.Overlay{Name: plan.Name, Type: plan.Type, Arch: plan.Arch},
		Path:       path,
		Repository: plan.Repository,
		Identity:   plan.Identity,
		Equiv:      plan.Equiv,
		Digest:     plan.Digest,
		Reference:  plan.Reference,
		Tags:       plan.Tags,
		Size:       plan.Size,
		Audience:   string(plan.Audience),
		Existing:   plan.Existing,
	}
	if plan.MovedTag != "" {
		rec.Moved = &report.Move{Tag: plan.MovedTag, From: plan.MovedFrom, To: shortKey(plan.Identity)}
	}
	return rec
}

// shortKey is the leading hex of a key, as a tag spells it.
func shortKey(k meta.KeyRef) string {
	if len(k.SHA256) > 12 {
		return k.SHA256[:12]
	}
	return k.SHA256
}
