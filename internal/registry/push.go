package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/condatainer/condatainer/internal/artifact/compare"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/image/squashfs"
	"github.com/condatainer/condatainer/internal/logging"
)

// Audience declares who can pull from an endpoint, and so decides what may be
// pushed to it.
//
// It states a fact about a registry, and CondaTainer derives the permitted set
// from it. Config must never enumerate types directly: `types: [app]` beside a
// public endpoint would erase the rule with no error, whereas a wrong audience
// is a claim someone has to write down and defend. Nothing here verifies the
// registry really is restricted — it is a declaration, not enforcement.
//
// It is deliberately not called visibility. A GitHub package's visibility is a
// separate per-package setting that this neither reads nor changes, and using
// its word for an unrelated declaration invited exactly that reading.
type Audience string

const (
	// Public is the default. Anyone can pull, so only what is ours to
	// redistribute may go here.
	Public Audience = "public"
	// Restricted is a registry only a known set of people can pull from.
	Restricted Audience = "restricted"
)

// maxIndexReconcileAttempts bounds the retry when another architecture is
// publishing at the same time. Registries offer no compare-and-swap on tags, so
// this converges rather than locks; CI should still publish sequentially.
const maxIndexReconcileAttempts = 3

// Accepts reports why an artifact may not be published to an endpoint of this
// audience, or nil. Restricted takes everything. Public answers in order:
//
//   - #REDISTRIBUTE: decides when declared: `no` refuses, `yes` publishes, whatever the type.
//   - Undeclared, a Conda build and a frozen environment publish: neither embeds a recipe to declare in.
//   - Undeclared otherwise, os and data publish and an app is refused.
//
// Nothing overrides a refusal: no flag, no force.
func (v Audience) Accepts(m meta.Manifest) error {
	if v == Restricted {
		return nil
	}
	if m.Redistribute != nil {
		if !*m.Redistribute {
			return fmt.Errorf("%s declares #REDISTRIBUTE: no, so it is not published to a %s registry", m.Name, v)
		}
		return nil
	}
	if m.BuildType == meta.BuildTypeConda || m.BuildType == meta.BuildTypeSnapshot {
		return nil
	}
	switch m.Type {
	case catalog.TypeOS, catalog.TypeData:
		return nil
	}
	return fmt.Errorf("%s artifacts are not published to a %s registry: %s installs software that is not ours to redistribute; declare #REDISTRIBUTE: yes in its recipe if its licence permits it, or push to an endpoint declared restricted", m.Type, v, m.Name)
}

// Placement overrides where an artifact is published, for a caller that owns a
// naming scheme of its own.
//
// A project keeps every artifact in one repository with the name in the tag,
// which PushReference cannot express — it derives both from the name, for the
// catalog's nested layout. Everything else about publishing is unchanged, which
// is the point of overriding only this: audience, key regeneration, and the
// annotations all still come from the artifact's own bytes.
type Placement struct {
	// Repo is the repository path beneath the base.
	Repo string
	// Tags are the tags to write, canonical first.
	Tags []string
}

// PublishRequest is one artifact and where it is going.
type PublishRequest struct {
	// Path is the local artifact. Its embedded manifest decides everything
	// about the destination, so renaming the file cannot smuggle it anywhere.
	Path string
	// Base is the registry base, owner and prefix included.
	Base string
	// Audience is what the endpoint declared.
	Audience Audience
	// Force allows replacing an existing versioned tag, which is otherwise
	// immutable.
	Force bool
	// Placement overrides the catalog repository and tags. Nil derives them from
	// the artifact's name, which is what an ordinary `registry push` does.
	Placement *Placement
	// Source overrides org.opencontainers.image.source, which otherwise names the
	// recipe collection the artifact recorded at build time. Empty keeps that.
	//
	// It is the publisher's fact, not the artifact's: one repository holding a
	// whole project's artifacts comes from several collections, so the collection
	// is not what that package's source is. The artifact keeps its own
	// Build.Source in the embedded manifest either way.
	Source string
	// Confirm, when set, is asked once the destination is settled and before
	// any bytes move. Returning false stops the push with ErrDeclined.
	Confirm func(PublishPlan) bool
}

// PublishPlan is what a push is about to do, for a caller to show and confirm.
type PublishPlan struct {
	Name      string
	Reference string   // the canonical tag's full reference
	Tags      []string // every tag written
	Size      int64
	Layers    int
	LayerSize int64
	Audience  Audience
	Replace   bool // Force will replace an existing versioned tag
}

// ErrDeclined reports a push its caller's Confirm turned down.
var ErrDeclined = errors.New("push declined")

// Publish validates an artifact against its destination, pushes it, and returns
// the platform manifest descriptor it published.
//
//   - Everything published is derived from the artifact's own bytes.
//   - The caller supplies only where it goes, through Placement.
//   - The returned digest is this architecture's index child, the address a lock records.
func Publish(ctx context.Context, req PublishRequest) (ocispec.Descriptor, error) {
	ctx = withPush(ctx)
	log := logging.FromContext(ctx)
	if req.Audience == "" {
		req.Audience = Public
	}
	if err := checkDistributable(req.Path); err != nil {
		return ocispec.Descriptor{}, err
	}

	m, err := meta.ReadManifest(req.Path)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := req.Audience.Accepts(m); err != nil {
		return ocispec.Descriptor{}, err
	}
	if m.Keys.Identity.Empty() || m.Keys.Equiv.Empty() {
		return ocispec.Descriptor{}, fmt.Errorf("%s records no scheme-backed identity, so nothing could pin what was pulled", m.Name)
	}
	// Held to its own claim before anyone else has to trust it: a published
	// artifact whose files do not reproduce its recorded keys is one that pull
	// would refuse on arrival, and finding that out here is cheaper for everyone.
	got, err := compare.Read(req.Path)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if err := checkRegeneratedKeys(got, m.Keys.Identity, m.Keys.Equiv, "recorded"); err != nil {
		return ocispec.Descriptor{}, err
	}

	repo, tags := "", []string(nil)
	if req.Placement != nil {
		repo, tags = req.Placement.Repo, req.Placement.Tags
		if repo == "" || len(tags) == 0 {
			return ocispec.Descriptor{}, fmt.Errorf("placement for %s names no repository or tags", m.Name)
		}
		// No immutability check: a placement's canonical tag carries the content
		// key, so it can only ever be re-pushed with the same content, and any
		// unqualified tag beside it is a moving pointer by design. Whether this
		// artifact is already there is the caller's question, asked before it
		// spends the bytes.
	} else {
		if repo, tags, err = PushReference(m); err != nil {
			return ocispec.Descriptor{}, err
		}
		if err := checkTagIsFree(ctx, req, m, repo, tags[0]); err != nil {
			return ocispec.Descriptor{}, err
		}
	}

	annotations := Annotations(m, compressionOf(req.Path))
	// Applied here rather than inside Annotations, which derives everything from
	// the artifact alone: this is the one value the publisher supplies. A caller
	// that names a source and gets it wrong is told, rather than publishing under
	// a URL that links nowhere.
	if source := strings.TrimSpace(req.Source); source != "" {
		if err := catalog.ValidSourceURL(source); err != nil {
			return ocispec.Descriptor{}, err
		}
		annotations[AnnSource] = source
	}
	// The artifact's own answer was recorded by whatever descriptor built it,
	// possibly before that field was checked, so it is dropped rather than
	// refused: a malformed provenance URL links nothing either way, and it is not
	// a reason to block distribution.
	if err := catalog.ValidSourceURL(annotations[AnnSource]); err != nil {
		log.Warn("Dropping the source annotation", "artifact", m.Name, "err", err)
		delete(annotations, AnnSource)
	}
	if req.Confirm != nil {
		var size int64
		if info, err := os.Stat(req.Path); err == nil {
			size = info.Size()
		}
		layerSize, _ := planLayerSize(size, profileFor(registryHost(req.Base)).MaxLayerSize)
		plan := PublishPlan{
			Name: m.Name, Reference: FullRef(req.Base, repo, tags[0]), Tags: tags,
			Size: size, Layers: layerCount(size, layerSize), LayerSize: layerSize,
			Audience: req.Audience, Replace: req.Force,
		}
		if !req.Confirm(plan) {
			return ocispec.Descriptor{}, ErrDeclined
		}
	}
	log.Info("Publishing", "artifact", m.Name, "reference", FullRef(req.Base, repo, tags[0]))
	return Push(ctx, req.Path, req.Base, repo, tags, annotations, m.Platform.Arch)
}

// checkTagIsFree refuses to displace published content without Force.
//
//   - A version-less artifact is addressed by date and a rolling tag, so re-publishing is allowed.
//   - A tag whose index lacks this architecture is an addition, so it is allowed.
//   - A child for this platform, or any content under a tag with no index, is refused.
func checkTagIsFree(ctx context.Context, req PublishRequest, m meta.Manifest, repo, tag string) error {
	if req.Force || isVersionLess(m.Type, m.Name) {
		return nil
	}
	repository, err := newRepository(req.Base, repo)
	if err != nil {
		return err
	}
	desc, err := repository.Resolve(ctx, tag)
	if err != nil {
		if err = classify(err); errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	occupant := "it"
	if desc.MediaType == ocispec.MediaTypeImageIndex && m.Platform.Arch != meta.ArchNone {
		plat, ok := platformFor(m.Platform.Arch)
		if !ok {
			return unpublishableArch(m.Name, m.Platform.Arch)
		}
		if _, taken := readIndexEntries(ctx, repository, tag)[platformKey(&plat)]; !taken {
			return nil
		}
		occupant = platformKey(&plat)
	}
	return fmt.Errorf("%s already publishes %s; a versioned tag is immutable, so replacing it needs --force",
		FullRef(req.Base, repo, tag), occupant)
}

// unpublishableArch refuses an artifact built for an architecture no index
// child is published for.
func unpublishableArch(what, arch string) error {
	return fmt.Errorf("%w: %s is built for %q; only amd64, arm64 and noarch are published",
		ErrUnsupportedPlatform, what, arch)
}

// Push uploads artifactPath, built for arch, to "<base>/<repo>" under every tag,
// the first canonical.
//
//   - annotations are carried, never read: policy lives in [Publish].
//   - amd64 or arm64: the manifest joins the tag's OCI image index as that platform, merged with what is published.
//   - noarch: tagged directly, since an index over one child would imply otherwise.
func Push(ctx context.Context, artifactPath, base, repo string, tags []string, annotations map[string]string, arch string) (ocispec.Descriptor, error) {
	if len(tags) == 0 {
		return ocispec.Descriptor{}, fmt.Errorf("no tags to push %s under", artifactPath)
	}
	if err := checkDistributable(artifactPath); err != nil {
		return ocispec.Descriptor{}, err
	}
	if _, err := meta.ReadManifest(artifactPath); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", artifactPath, err)
	}
	// One profile decides both how much is asked for at once and how fast: it
	// clamps the layer plan, and it spaces the writes that follow.
	profile := profileFor(registryHost(base))
	plan, err := preflightUpload(ctx, artifactPath, base, tags, profile)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = withPacer(ctx, newPacer(profile.MinMutationGap))
	ctx = withThroughputGuard(ctx, newThroughputGuard(profile))
	archIndependent := arch == meta.ArchNone
	var plat ocispec.Platform
	if !archIndependent {
		var ok bool
		if plat, ok = platformFor(arch); !ok {
			return ocispec.Descriptor{}, unpublishableArch(artifactPath, arch)
		}
	}

	repository, err := newRepository(base, repo)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	// After the local checks, not before: a missing or empty artifact is free to
	// detect and should not be gated behind a network round trip. Still before
	// any hashing, which is the property that matters.
	if err := probePushAccess(ctx, repository); err != nil {
		return ocispec.Descriptor{}, err
	}

	// Blobs first: a manifest may not reference what the registry does not yet
	// hold. Nothing is staged on the way — see pushArtifactLayers.
	layers, err := pushArtifactLayers(ctx, repository.Blobs(), artifactPath, MediaTypeOverlayBlob, plan.LayerSize)
	if err != nil {
		return ocispec.Descriptor{}, classify(err)
	}

	// Packed straight against the repository. There is no local store to copy
	// from, because the layers were never written to one.
	//
	// Retried like the layers, and for a sharper reason: this lands after every
	// blob, when a provider's request counter is at its hottest, so it is the
	// most likely single mutation in a large push to be refused.
	var manifestDesc ocispec.Descriptor
	err = retryPolicyFrom(ctx).run(ctx, mutation{verb: verbPublish, attrs: []any{"target", "manifest"}, do: func(ctx context.Context) error {
		var packErr error
		manifestDesc, packErr = oras.PackManifest(ctx, repository, oras.PackManifestVersion1_1, ArtifactTypeOverlay, oras.PackManifestOptions{
			Layers:              layers,
			ManifestAnnotations: annotations,
		})
		return packErr
	}})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to publish manifest: %w", err)
	}
	manifestDesc.ArtifactType = ArtifactTypeOverlay

	if archIndependent {
		for _, tag := range tags {
			if err := retryPolicyFrom(ctx).run(ctx, mutation{
				verb:  verbPublish,
				attrs: []any{"tag", tag},
				do:    func(ctx context.Context) error { return repository.Tag(ctx, manifestDesc, tag) },
			}); err != nil {
				return ocispec.Descriptor{}, fmt.Errorf("failed to tag %s: %w", FullRef(base, repo, tag), err)
			}
		}
		return manifestDesc, nil
	}
	manifestDesc.Platform = &plat
	if err := reconcileIndex(ctx, repository, tags, manifestDesc, annotations, ArtifactTypeOverlay, base, repo); err != nil {
		return ocispec.Descriptor{}, err
	}
	return manifestDesc, nil
}

// reconcileIndex points every tag at an index containing this architecture's
// child, keeping the architectures others published. Tagging is not atomic and
// tags have no compare-and-swap, so it re-reads and retries, folding in what it
// observed, until concurrent publishers converge.
func reconcileIndex(ctx context.Context, repository *remote.Repository, tags []string, child ocispec.Descriptor, annotations map[string]string, artifactType, base, repo string) error {
	entries := collectIndexEntries(ctx, repository, tags)
	// Inserted before the merge, so the child being pushed wins over the one
	// already published for this platform.
	entries[platformKey(child.Platform)] = child

	for range maxIndexReconcileAttempts {
		if err := publishIndex(ctx, repository, tags, entries, annotations, artifactType); err != nil {
			return classify(err)
		}
		observed, complete := verifyTaggedIndexes(ctx, repository, tags, entries)
		if complete {
			return nil
		}
		mergeIndexEntries(entries, observed)
	}
	return fmt.Errorf("%s kept changing under a concurrent push; publish architectures sequentially and retry",
		FullRef(base, repo, tags[0]))
}

// compressionOf reports the SquashFS compressor of an image, or "" when it
// cannot be read. Best-effort by design: the annotation tells a puller whether
// its kernel can mount the payload, and not knowing is a reason to omit the
// claim rather than to refuse the push.
func compressionOf(path string) string {
	stats, err := squashfs.GetSquashFSStats(path)
	if err != nil || stats == nil {
		return ""
	}
	return stats.Compression
}
