package registry

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/registry/remote"
)

// RemoveTarget says which builds a removal covers.
type RemoveTarget struct {
	Name string
	// Identity is a hex prefix of one build's identity. Empty covers every build of the name.
	Identity string
	// Arch limits the removal to one architecture. Empty covers every architecture.
	Arch string
}

// tagged is a held manifest with its tags read.
type tagged struct {
	held Held
	tags []parsedTag
	// buildTag is the manifest's build tag for this name, if it has one.
	buildTag string
}

// build is another build of a name, read for the tag it may take over.
type build struct {
	tag     string
	desc    ocispec.Descriptor
	created time.Time
	equiv   string
}

// PlanRemove plans the removal of one build of a name, or of every build of it.
//   - A build holding the name tag or an equivalence tag hands each to the newest other build that qualifies; with none, the tag goes with it.
//   - Removing every build of a name moves nothing.
//   - A manifest holding a tag this package did not write is never removed.
func PlanRemove(ctx context.Context, base, repo string, b Backend, t RemoveTarget) (RemovePlan, error) {
	ctx = withPush(ctx)
	if t.Arch != "" && !ValidArch(t.Arch) {
		return RemovePlan{}, fmt.Errorf("%q is not an architecture a manifest is published for", t.Arch)
	}
	if t.Identity != "" && !isHex(t.Identity) {
		return RemovePlan{}, fmt.Errorf("identity %q is not a hexadecimal digest", t.Identity)
	}
	encoded, err := EncodeName(t.Name)
	if err != nil {
		return RemovePlan{}, err
	}
	repository, err := newRepository(base, repo)
	if err != nil {
		return RemovePlan{}, err
	}
	held, err := b.Manifests(ctx)
	if err != nil {
		return RemovePlan{}, err
	}
	all := readTags(held, encoded)

	var plan RemovePlan
	var targets []tagged
	for _, m := range all {
		if !m.of(encoded, t.Arch) {
			continue
		}
		if t.Identity != "" && !strings.HasPrefix(m.qualifier(kindBuild), t.Identity) {
			continue
		}
		if m.foreign() {
			if t.Identity != "" {
				return plan, fmt.Errorf("%s also holds a tag this tool did not write, so it is not removed", m.held.Tags[0])
			}
			plan.Kept = append(plan.Kept, m.held)
			continue
		}
		targets = append(targets, m)
	}
	if len(targets) == 0 {
		if len(plan.Kept) == 0 {
			return plan, fmt.Errorf("%w: no %s of %s in %s/%s", ErrNotFound, what(t), t.Name, registryHost(base), repo)
		}
		return plan, nil
	}
	if t.Identity != "" {
		if err := refuseAmbiguous(targets, t); err != nil {
			return plan, err
		}
	}
	for _, m := range targets {
		plan.Removes = append(plan.Removes, m.held)
	}
	if t.Identity == "" {
		return plan, nil
	}
	moves, err := planMoves(ctx, repository, all, targets)
	if err != nil {
		return plan, err
	}
	plan.Moves = moves
	return plan, nil
}

func what(t RemoveTarget) string {
	if t.Identity == "" {
		return "build"
	}
	return "build starting " + t.Identity
}

// readTags parses every tag of the held manifests that belong to encoded.
func readTags(held []Held, encoded string) []tagged {
	var out []tagged
	for _, h := range held {
		m := tagged{held: h}
		for _, tag := range h.Tags {
			p := parseTag(tag)
			m.tags = append(m.tags, p)
			if p.Kind == kindBuild && p.Encoded == encoded {
				m.buildTag = tag
			}
		}
		out = append(out, m)
	}
	return out
}

// of reports whether any tag is of the name and, when arch is set, that architecture.
func (m tagged) of(encoded, arch string) bool {
	return slices.ContainsFunc(m.tags, func(p parsedTag) bool {
		return p.Kind != kindOther && p.Encoded == encoded && (arch == "" || p.Arch == arch)
	})
}

// foreign reports whether the manifest holds a tag this package did not write.
func (m tagged) foreign() bool {
	return slices.ContainsFunc(m.tags, func(p parsedTag) bool { return p.Kind == kindOther })
}

// qualifier is the hex key of the manifest's first tag of kind, or "".
func (m tagged) qualifier(kind tagKind) string {
	for _, p := range m.tags {
		if p.Kind == kind {
			return p.Qualifier
		}
	}
	return ""
}

func (m tagged) arch() string {
	for _, p := range m.tags {
		if p.Kind != kindOther {
			return p.Arch
		}
	}
	return ""
}

// refuseAmbiguous refuses an identity prefix that matches two builds of one architecture.
func refuseAmbiguous(targets []tagged, t RemoveTarget) error {
	seen := map[string][]string{}
	for _, m := range targets {
		seen[m.arch()] = append(seen[m.arch()], m.buildTag)
	}
	for _, tags := range seen {
		if len(tags) > 1 {
			slices.Sort(tags)
			return fmt.Errorf("%w: %s matches %d builds of %s: %s",
				ErrMismatch, t.Identity, len(tags), t.Name, strings.Join(tags, ", "))
		}
	}
	return nil
}

// planMoves hands each name and equivalence tag a removed build holds to the newest other build that qualifies.
func planMoves(ctx context.Context, repository *remote.Repository, all, targets []tagged) ([]TagMove, error) {
	removed := map[string]bool{}
	for _, m := range targets {
		removed[m.buildTag] = true
	}
	var moves []TagMove
	for _, m := range targets {
		var carried []parsedTag
		for _, p := range m.tags {
			if p.Kind == kindName || p.Kind == kindEquiv {
				carried = append(carried, p)
			}
		}
		if len(carried) == 0 {
			continue
		}
		var others []string
		for _, o := range all {
			if o.buildTag != "" && !removed[o.buildTag] && o.arch() == m.arch() && o.buildTagName() == m.buildTagName() {
				others = append(others, o.buildTag)
			}
		}
		builds, err := readBuilds(ctx, repository, others)
		if err != nil {
			return nil, err
		}
		for _, p := range carried {
			tag := tagFor(p, m)
			var pool []build
			for _, c := range builds {
				if p.Kind == kindName || strings.HasPrefix(c.equiv, p.Qualifier) {
					pool = append(pool, c)
				}
			}
			if len(pool) == 0 {
				continue
			}
			to := newest(pool)
			moves = append(moves, TagMove{Tag: tag, FromBuild: m.buildTag, ToBuild: to.tag, To: to.desc})
		}
	}
	return moves, nil
}

// buildTagName is the encoded name of the build tag.
func (m tagged) buildTagName() string {
	return parseTag(m.buildTag).Encoded
}

// tagFor finds the full spelling of a name or equivalence tag of the manifest.
func tagFor(p parsedTag, m tagged) string {
	for i, q := range m.tags {
		if q == p {
			return m.held.Tags[i]
		}
	}
	return ""
}

// readBuilds reads each build tag's manifest for the time and equivalence key it records.
func readBuilds(ctx context.Context, repository *remote.Repository, tags []string) ([]build, error) {
	out := make([]build, len(tags))
	var group errgroup.Group
	group.SetLimit(workers)
	for i, tag := range tags {
		group.Go(func() error {
			desc, ann, err := resolveManifest(ctx, repository, tag)
			if err != nil {
				return fmt.Errorf("cannot read %s: %w", tag, classify(err))
			}
			created, _ := time.Parse(time.RFC3339, ann[AnnCreated])
			out[i] = build{
				tag: tag, desc: desc, created: created,
				equiv: strings.TrimPrefix(Equiv(ann).SHA256, digestPrefix),
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// newest is the most recently built. A build with no recorded time is the oldest, and a tie goes to the larger tag.
func newest(builds []build) build {
	return slices.MaxFunc(builds, func(a, b build) int {
		return cmp.Or(a.created.Compare(b.created), strings.Compare(a.tag, b.tag))
	})
}
