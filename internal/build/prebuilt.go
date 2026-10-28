package build

import (
	"context"
	"errors"
	"fmt"
	"maps"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/registry"
)

// prebuiltResult says whether build should stop because an artifact was
// installed. A false result means the caller may continue with the local build.
type prebuiltResult bool

var (
	resolvePrebuilt = registry.Lookup
	probePrebuilt   = registry.Probe
	pullPrebuilt    = registry.PullLocked
)

// prebuiltChoice is what planning decided about a node's prebuilt artifact.
type prebuiltChoice int

const (
	prebuiltUndecided prebuiltChoice = iota // settled when the build runs
	prebuiltNone                            // build from the recipe
	prebuiltPull                            // pull the planned candidate
)

// prebuiltCandidate is a published artifact that matches the local recipe.
type prebuiltCandidate struct {
	endpoint    string
	base, repo  string
	equiv       meta.KeyRef
	desc        ocispec.Descriptor
	annotations map[string]string
}

// prebuiltPlan is a node's prebuilt decision, made before anything runs. miss
// says why a lookup that found nothing did.
type prebuiltPlan struct {
	choice    prebuiltChoice
	candidate prebuiltCandidate
	miss      prebuiltMiss
}

// plannedDep is what a dependency that is not installed yet will contribute to
// a dependent's equivalence: its name and type, and for data its equivalence key.
type plannedDep struct {
	Name  string
	Type  artifact.Type
	Equiv meta.KeyRef
}

// plannedAs reports what this node will contribute as a dependency. A data
// dependency contributes its equivalence key, so it is unknown until derived.
func (b *BuildObject) plannedAs() (plannedDep, bool) {
	if b.spec.Image.Type == artifact.TypeData && b.plannedEquiv.Empty() {
		return plannedDep{}, false
	}
	return plannedDep{Name: b.spec.Image.Name, Type: b.spec.Image.Type, Equiv: b.plannedEquiv}, true
}

// prebuiltMiss says why no candidate was found, most serious first.
type prebuiltMiss int

const (
	missNone        prebuiltMiss = iota // nothing published for this recipe and platform
	missUnavailable                     // the registry could not be reached
)

// noPrebuiltReason says why no prebuilt can apply at all, or "" when one can. A
// Conda build has none to pull.
func (b *BuildObject) noPrebuiltReason() string {
	switch {
	case config.Global.SkipPrebuilt:
		return "prebuilt lookup is turned off"
	case b.buildType == BuildTypeConda:
		return "a Conda build has nothing published"
	case b.catalogSource == nil:
		return "it has no recipe source"
	case b.catalogSource.DescriptorErr != nil:
		return "its recipe source has an invalid descriptor"
	case b.catalogSource.Desc.OCI.Registry == "":
		return "its recipe source declares no registry"
	}
	return ""
}

// prebuiltEligible reports whether a prebuilt can apply at all.
func (b *BuildObject) prebuiltEligible() bool { return b.noPrebuiltReason() == "" }

// findPrebuilt reads the source's registry by metadata alone, and returns the
// artifact published there for the planned architecture if it was made from the
// local recipe. Every other outcome is a miss.
//   - The equivalence tag is asked for directly, so it does not matter where the name tag points.
func (b *BuildObject) findPrebuilt(ctx context.Context) (prebuiltCandidate, bool, prebuiltMiss, error) {
	want, err := b.prebuiltEquivalence(ctx)
	if err != nil {
		return prebuiltCandidate{}, false, missNone, fmt.Errorf("cannot derive expected equivalence for %s: %w", b.spec.Image.Name, err)
	}
	endpoint := b.catalogSource.Desc.OCI.Registry
	base, repo, err := registry.SplitCoordinate(endpoint)
	if err != nil {
		return prebuiltCandidate{}, false, missNone, fmt.Errorf("cannot use prebuilt %s: source registry: %w", b.spec.Image.Name, err)
	}
	arch := planArch(ctx)
	if b.spec.Image.Noarch {
		arch = registry.ArchNoarch
	}

	log := logging.FromContext(ctx)
	ctx = registry.WithSource(ctx, b.catalogSource.Base)
	lookup := resolvePrebuilt
	if tagOnly(ctx) {
		lookup = probePrebuilt
	}
	found, err := lookup(ctx, base, repo, registry.Address{Name: b.spec.Image.Name, Equiv: want.SHA256, Arch: arch})
	if err != nil {
		switch {
		case errors.Is(err, registry.ErrNotFound), errors.Is(err, registry.ErrUnsupportedPlatform),
			closedDoor(ctx, err, endpoint) && !tagOnly(ctx):
			return prebuiltCandidate{}, false, missNone, nil
		case errors.Is(err, registry.ErrUnavailable):
			log.Debug("prebuilt unavailable", "name", b.spec.Image.Name, "err", err)
			return prebuiltCandidate{}, false, missUnavailable, nil
		}
		return prebuiltCandidate{}, false, missNone, fmt.Errorf("cannot use prebuilt %s from %s: %w", b.spec.Image.Name, endpoint, err)
	}
	if !tagOnly(ctx) {
		if err := registry.Check(found.Annotations, registry.Want{Name: b.spec.Image.Name}); err != nil {
			return prebuiltCandidate{}, false, missNone, fmt.Errorf("cannot use prebuilt %s from %s: %w", b.spec.Image.Name, endpoint, err)
		}
		if registry.Equiv(found.Annotations) != want {
			return prebuiltCandidate{}, false, missNone, nil
		}
	}
	return prebuiltCandidate{endpoint: endpoint, base: base, repo: repo, equiv: want,
		desc: found.Desc, annotations: found.Annotations}, true, missNone, nil
}

// planPrebuilt settles, before anything runs, whether this node pulls a
// prebuilt artifact or builds from the recipe, so the plan can say which. known
// says every dependency's contribution is available, installed or planned; a
// node that cannot derive its equivalence yet stays undecided until it builds.
func (b *BuildObject) planPrebuilt(ctx context.Context, known bool) error {
	if known {
		if equiv, edges, err := b.recipeEquivalence(ctx); err == nil {
			b.plannedEquiv = equiv
			b.plannedEdges = edges
			b.plannedDepPaths = maps.Clone(b.depImagePaths)
		}
	}
	switch {
	case !b.prebuiltEligible():
		b.prebuilt = prebuiltPlan{choice: prebuiltNone}
	case known:
		cand, found, miss, err := b.findPrebuilt(ctx)
		if err != nil {
			return err
		}
		if !found {
			if miss == missUnavailable {
				logging.FromContext(ctx).Warn(fmt.Sprintf("Cannot fetch prebuilt %s, building from the recipe", b.spec.Image.Name))
			}
			b.prebuilt = prebuiltPlan{choice: prebuiltNone, miss: miss}
			return nil
		}
		b.prebuilt = prebuiltPlan{choice: prebuiltPull, candidate: cand}
	}
	return nil
}

// tryPrebuilt installs the planned prebuilt, or looks for one in the selected recipe source's registry when planning left the node undecided.
//   - The caller already holds the target's producer lock, so pull uses the locked transport entry point and installs atomically into the final pathname.
func (b *BuildObject) tryPrebuilt(ctx context.Context) (prebuiltResult, error) {
	if !b.prebuiltEligible() || b.prebuilt.choice == prebuiltNone {
		return false, nil
	}

	ctx = registry.WithSource(ctx, b.catalogSource.Base)
	log := logging.FromContext(ctx)
	cand, found := b.prebuilt.candidate, b.prebuilt.choice == prebuiltPull
	miss := missNone
	if !found {
		var err error
		if cand, found, miss, err = b.findPrebuilt(ctx); err != nil {
			return false, err
		}
	}
	if found {
		// Said before the download rather than after it: everything above is
		// metadata, and the gigabytes start here. Without this the operator
		// watches a long transfer with nothing saying what is being fetched or
		// that it has already been checked against the local recipe.
		log.Info("Prebuilt found and verified", "name", b.spec.Image.Name,
			"endpoint", cand.endpoint, "equivalence", describePrebuiltKey(cand.equiv))
		err := pullPrebuilt(ctx, cand.base, cand.repo, cand.desc, cand.annotations, b.tgt.Path)
		switch {
		case err == nil:
			invalidateInstalledOverlays()
			log.Info("Prebuilt image ready", "kind", "success", "path", b.tgt.Path, "endpoint", cand.endpoint)
			return true, nil
		case errors.Is(err, registry.ErrNotFound), errors.Is(err, registry.ErrUnsupportedPlatform),
			closedDoor(ctx, err, cand.endpoint):
		case errors.Is(err, registry.ErrUnavailable):
			log.Debug("prebuilt unavailable", "name", b.spec.Image.Name, "err", err)
			miss = missUnavailable
		default:
			return false, fmt.Errorf("cannot pull prebuilt %s from %s: %w", b.spec.Image.Name, cand.endpoint, err)
		}
	}
	switch miss {
	case missUnavailable:
		log.Warn(fmt.Sprintf("Cannot fetch prebuilt %s, building from the recipe", b.spec.Image.Name))
	default:
		log.Info(fmt.Sprintf("No prebuilt %s available, building from the recipe", b.spec.Image.Name))
	}
	return false, nil
}

// closedDoor reports a refusal that leaves nothing to fix: the endpoint turned away a request carrying no credential at all.
//   - ErrUnauthorized is otherwise fatal, since an expired token must not silently become a long rebuild, but that needs a credential to have expired.
//   - With none, the status only means this user cannot pull from that endpoint, the outcome ErrNotFound already falls back on.
func closedDoor(ctx context.Context, err error, endpoint string) bool {
	return errors.Is(err, registry.ErrUnauthorized) && !registry.HasCredential(ctx, endpoint)
}

// prebuiltEquivalence derives the key from the selected local recipe and the
// dependencies currently installed. Identity is intentionally ignored: a data
// artifact built against equivalent dependency instances is substitutable even
// when their exact identities differ.
func (b *BuildObject) prebuiltEquivalence(ctx context.Context) (meta.KeyRef, error) {
	equiv, _, err := b.recipeEquivalence(ctx)
	return equiv, err
}

// recipeEquivalence is prebuiltEquivalence with the dependency edges the key was
// derived from, each with the role it plays.
func (b *BuildObject) recipeEquivalence(ctx context.Context) (meta.KeyRef, []meta.Dependency, error) {
	recipe, ok := b.spec.Source.RecipeFile()
	if !ok {
		return meta.KeyRef{}, nil, errors.New("selected source has no recipe")
	}
	manifest := b.Manifest()
	manifest.Dependencies, _ = key.Manifest(key.Artifact{
		Name:         b.spec.Image.Name,
		Type:         b.spec.Image.Type,
		Env:          b.spec.Image.Env,
		Recipe:       recipe.Data,
		Placeholders: b.spec.Source.Placeholders,
		Deps:         b.dependencyKeys(ctx),
		From:         b.spec.Source.UpstreamDigest(),
	})
	derived, err := key.Generate(manifest, b.keySources())
	if err != nil {
		return meta.KeyRef{}, nil, err
	}
	return derived.Equiv.Ref, manifest.Dependencies, nil
}

func describePrebuiltKey(k meta.KeyRef) string {
	if k.Empty() {
		return "absent"
	}
	return k.Scheme + " " + k.Digest()
}
