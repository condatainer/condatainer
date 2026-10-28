package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/condatainer/condatainer/internal/artifact"
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
	case artifact.TypeOS, artifact.TypeData:
		return nil
	}
	return fmt.Errorf("%s overlays are not published to a %s registry: %s installs software that is not ours to redistribute; declare #REDISTRIBUTE: yes in its recipe if its licence permits it, or push to an endpoint declared restricted", m.Type, v, m.Name)
}

// PublishRequest is one artifact and where it is going.
type PublishRequest struct {
	// Path is the local artifact. Its embedded manifest decides everything
	// about the destination, so renaming the file cannot smuggle it anywhere.
	Path string
	// Repository is the destination repository coordinate, host included.
	Repository string
	// Audience is what the endpoint declared.
	Audience Audience
	// Tags says which tags are written besides the build tag.
	Tags TagRequest
	// Source overrides org.opencontainers.image.source, which otherwise names the
	// recipe collection the artifact recorded at build time. Empty keeps that.
	//
	// It is the publisher's fact, not the artifact's: one repository holding a
	// whole project's artifacts comes from several collections, so the collection
	// is not what that package's source is. The artifact keeps its own
	// Build.Source in the embedded manifest either way.
	Source string
	// Collection, when set, is the recipe collection a source's repository
	// belongs to. An artifact recording another one, or none, is reported in the
	// plan, since the package links to one code repository.
	Collection string
	// Confirm, when set, is asked once the destination is settled and before
	// any bytes move. Returning false stops the push with ErrDeclined.
	Confirm func(PublishPlan) bool
}

// PublishPlan is what a push is about to do, for a caller to show and confirm.
type PublishPlan struct {
	Name string
	// Type and Arch are the artifact's, and Identity and Equiv its keys.
	Type     artifact.Type
	Arch     string
	Identity meta.KeyRef
	Equiv    meta.KeyRef
	// Repository is the destination coordinate. Digest is the published
	// manifest, known only when the build is already there.
	Repository string
	Digest     string
	Reference  string   // the build tag's full reference
	Tags       []string // every tag written
	Size       int64
	Layers     int
	LayerSize  int64
	Audience   Audience
	// Existing reports that the build is already published, so only tags are
	// written and nothing is uploaded.
	Existing bool
	// MovedTag is the tag that moves off another build, and MovedFrom is the
	// leading hex of that build's identity.
	MovedTag, MovedFrom string
	// Mismatch is set when the artifact does not record the repository's collection.
	Mismatch *CollectionMismatch
}

// CollectionMismatch says which collection an artifact records, which may be
// none, against the one its repository belongs to.
type CollectionMismatch struct {
	Recorded, Expected string
}

// ErrDeclined reports a push its caller's Confirm turned down.
var ErrDeclined = errors.New("push declined")

// Prepared is a push that has been checked and surveyed, and not yet made. Plan
// says what it would do.
type Prepared struct {
	Plan PublishPlan

	req         PublishRequest
	m           meta.Manifest
	base, repo  string
	repository  *remote.Repository
	tags        Tags
	annotations map[string]string
	held        surveyed
	mismatch    *CollectionMismatch
}

// Complete reports that the build and all its tags are already published.
func (p *Prepared) Complete() bool { return p.held.built && len(p.held.pending) == 0 }

// Prepare validates an artifact against its destination and reads what the
// repository holds, and moves no bytes.
//
//   - Everything published is derived from the artifact's own bytes.
//   - The caller supplies only where it goes and which tags it wants.
func Prepare(ctx context.Context, req PublishRequest) (*Prepared, error) {
	ctx = withPush(ctx)
	log := logging.FromContext(ctx)
	if req.Audience == "" {
		req.Audience = Public
	}
	if err := checkDistributable(req.Path); err != nil {
		return nil, err
	}

	m, err := meta.ReadManifest(req.Path)
	if err != nil {
		return nil, err
	}
	if err := req.Audience.Accepts(m); err != nil {
		return nil, err
	}
	if m.Keys.Identity.Empty() || m.Keys.Equiv.Empty() {
		return nil, fmt.Errorf("%s records no scheme-backed identity, so nothing could pin what was pulled", m.Name)
	}
	if !ValidArch(m.Platform.Arch) {
		return nil, fmt.Errorf("%w: %s is built for %q; only amd64, arm64 and noarch are published",
			ErrUnsupportedPlatform, m.Name, m.Platform.Arch)
	}
	// Held to its own claim before anyone else has to trust it: a published
	// artifact whose files do not reproduce its recorded keys is one that pull
	// would refuse on arrival, and finding that out here is cheaper for everyone.
	got, err := compare.Read(req.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if err := checkRegeneratedKeys(got, m.Keys.Identity, m.Keys.Equiv, "recorded"); err != nil {
		return nil, err
	}

	base, repo, err := SplitCoordinate(req.Repository)
	if err != nil {
		return nil, err
	}
	tags, err := TagsFor(m, req.Tags)
	if err != nil {
		return nil, err
	}

	annotations := Annotations(m, compressionOf(req.Path))
	// Applied here rather than inside Annotations, which derives everything from
	// the artifact alone: this is the one value the publisher supplies. A caller
	// that names a source and gets it wrong is told, rather than publishing under
	// a URL that links nowhere.
	if source := strings.TrimSpace(req.Source); source != "" {
		if err := catalog.ValidSourceURL(source); err != nil {
			return nil, err
		}
		annotations[AnnSource] = source
	}
	// The artifact's own answer was recorded by whatever descriptor built it,
	// possibly before that field was checked, so it is dropped rather than
	// refused: a malformed provenance URL links nothing either way, and it is not
	// a reason to block distribution.
	if err := catalog.ValidSourceURL(annotations[AnnSource]); err != nil {
		log.Warn("Dropping the source annotation", "name", m.Name, "err", err)
		delete(annotations, AnnSource)
	}
	var mismatch *CollectionMismatch
	if recorded := annotations[AnnSource]; req.Collection != "" && !sameCollection(recorded, req.Collection) {
		mismatch = &CollectionMismatch{Recorded: recorded, Expected: req.Collection}
	}

	repository, err := newRepository(base, repo)
	if err != nil {
		return nil, err
	}
	held, err := survey(ctx, repository, tags, m.Keys.Identity)
	if err != nil {
		return nil, err
	}

	var size int64
	if info, err := os.Stat(req.Path); err == nil {
		size = info.Size()
	}
	layerSize, _ := planLayerSize(size, profileFor(registryHost(base)).MaxLayerSize)
	plan := PublishPlan{
		Name: m.Name, Type: m.Type, Arch: m.Platform.Arch, Identity: m.Keys.Identity, Equiv: m.Keys.Equiv,
		Repository: TrimBaseScheme(req.Repository), Reference: FullRef(base, repo, tags.Build), Tags: held.pending,
		Size: size, Layers: layerCount(size, layerSize), LayerSize: layerSize,
		Audience: req.Audience, Existing: held.built,
		MovedTag: held.movedTag, MovedFrom: held.movedFrom, Mismatch: mismatch,
	}
	if held.built {
		plan.Digest = held.build.Digest.String()
	}
	return &Prepared{Plan: plan, req: req, m: m, base: base, repo: repo, repository: repository,
		tags: tags, annotations: annotations, held: held, mismatch: mismatch}, nil
}

// Publish makes a prepared push, and returns the manifest descriptor it
// published, the address a lock records. A build already published uploads
// nothing: the other tags are pointed at it.
func (p *Prepared) Publish(ctx context.Context) (ocispec.Descriptor, error) {
	ctx = withPush(ctx)
	log := logging.FromContext(ctx)
	if p.Complete() {
		log.Info("Already published", "name", p.m.Name, "reference", p.Plan.Reference)
		return p.held.build, nil
	}
	if p.held.built {
		log.Info("Tagging published build", "name", p.m.Name, "tags", strings.Join(p.held.pending, ", "))
		return p.held.build, tagAll(ctx, p.repository, p.base, p.repo, p.held.build, p.held.pending)
	}
	log.Info("Publishing", "name", p.m.Name, "reference", p.Plan.Reference)
	return Push(ctx, p.req.Path, p.base, p.repo, p.tags.List(), p.annotations)
}

// Publish prepares a push, asks req.Confirm when it has something to do, and
// makes it.
func Publish(ctx context.Context, req PublishRequest) (ocispec.Descriptor, error) {
	p, err := Prepare(ctx, req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if !p.Complete() {
		if req.Confirm != nil {
			if !req.Confirm(p.Plan) {
				return ocispec.Descriptor{}, ErrDeclined
			}
		} else if p.mismatch != nil {
			logging.FromContext(ctx).Warn("Overlay was not built from this repository's recipe collection",
				"name", p.m.Name, "recorded", p.mismatch.Recorded, "repository", p.mismatch.Expected)
		}
	}
	return p.Publish(ctx)
}

// surveyed is what a repository already holds for one push.
type surveyed struct {
	// built reports that the build tag exists, and build is its manifest.
	built bool
	build ocispec.Descriptor
	// pending are the tags that do not yet point at the build, in write order.
	pending []string
	// movedTag and movedFrom name the name tag's current build when it differs.
	movedTag, movedFrom string
}

// survey reads the tags a push would write.
//   - A build tag that holds another identity is refused, since a tag carries only a key prefix.
//   - The name tag's occupant is read for the build it would leave.
func survey(ctx context.Context, repository *remote.Repository, tags Tags, identity meta.KeyRef) (surveyed, error) {
	var s surveyed
	desc, ann, err := resolveManifest(ctx, repository, tags.Build)
	switch {
	case err == nil:
		if got := Identity(ann); got != identity {
			return s, fmt.Errorf("%w: %s holds %s, not this build", ErrMismatch, tags.Build, describeKey(got))
		}
		s.built, s.build = true, desc
	case errors.Is(classify(err), ErrNotFound):
		s.pending = append(s.pending, tags.Build)
	default:
		return s, classify(err)
	}
	for _, tag := range tags.List()[1:] {
		current, err := repository.Resolve(ctx, tag)
		if err != nil {
			if err = classify(err); !errors.Is(err, ErrNotFound) {
				return s, err
			}
		} else if s.built && current.Digest == s.build.Digest {
			continue
		}
		s.pending = append(s.pending, tag)
		if tag == tags.Name && err == nil {
			if _, occupant, oerr := resolveManifest(ctx, repository, tag); oerr == nil {
				if from := Identity(occupant); from != identity && !from.Empty() {
					s.movedTag, s.movedFrom = tag, from.SHA256[:min(keyChars, len(from.SHA256))]
				}
			}
		}
	}
	return s, nil
}

// Push uploads artifactPath to "<base>/<repo>" and tags its manifest with every
// tag, in order.
//
//   - annotations are carried, never read: policy lives in [Publish].
//   - Each architecture is its own manifest, so nothing of another architecture is read or merged.
func Push(ctx context.Context, artifactPath, base, repo string, tags []string, annotations map[string]string) (ocispec.Descriptor, error) {
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
	plan, err := preflightUpload(ctx, artifactPath, TrimBaseScheme(base)+"/"+repo, tags, profile)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	ctx = withPacer(ctx, newPacer(profile.MinMutationGap))
	ctx = withThroughputGuard(ctx, newThroughputGuard(profile))

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
		capture := &manifestCapture{Repository: repository}
		var packErr error
		manifestDesc, packErr = oras.PackManifest(ctx, capture, oras.PackManifestVersion1_1, ArtifactTypeOverlay, oras.PackManifestOptions{
			Layers:              layers,
			ManifestAnnotations: annotations,
		})
		if packErr == nil {
			_, packErr = rememberManifest(repository, manifestDesc, capture.raw)
		}
		return packErr
	}})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to publish manifest: %w", err)
	}
	manifestDesc.ArtifactType = ArtifactTypeOverlay
	return manifestDesc, tagAll(ctx, repository, base, repo, manifestDesc, tags)
}

// manifestCapture keeps the bytes of the manifest packed through it, so tagging it later reads nothing back.
type manifestCapture struct {
	*remote.Repository
	raw []byte
}

func (c *manifestCapture) Push(ctx context.Context, expected ocispec.Descriptor, body io.Reader) error {
	if expected.MediaType != ocispec.MediaTypeImageManifest {
		return c.Repository.Push(ctx, expected, body)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	c.raw = raw
	return c.Repository.Push(ctx, expected, bytes.NewReader(raw))
}

// tagAll points every tag at desc, in order.
func tagAll(ctx context.Context, repository *remote.Repository, base, repo string, desc ocispec.Descriptor, tags []string) error {
	for _, tag := range tags {
		if err := retryPolicyFrom(ctx).run(ctx, mutation{
			verb:  verbPublish,
			attrs: []any{"tag", tag},
			do:    func(ctx context.Context) error { return tagManifest(ctx, repository, desc, tag) },
		}); err != nil {
			return fmt.Errorf("failed to tag %s: %w", FullRef(base, repo, tag), err)
		}
	}
	return nil
}

// tagManifest writes tag against the manifest's own bytes.
//   - The bytes come from what this process already read or wrote, so a tag costs one write and no read.
//   - A read that counts against a pull limit is spent only for a manifest this process has not seen.
func tagManifest(ctx context.Context, repository *remote.Repository, desc ocispec.Descriptor, tag string) error {
	raw, err := manifestBytes(ctx, repository, desc)
	if err != nil {
		return err
	}
	return repository.Manifests().PushReference(ctx, desc, bytes.NewReader(raw), tag)
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
