package registry

import (
	"context"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TagMove points a tag at another build before the build it leaves is removed.
type TagMove struct {
	Tag string
	// FromBuild and ToBuild are the build tags of the manifests the tag leaves and joins.
	FromBuild, ToBuild string
	To                 ocispec.Descriptor
}

// RemovePlan is what a removal is about to do.
type RemovePlan struct {
	Moves   []TagMove
	Removes []Held
	// Kept are manifests that matched but hold a tag this package did not write, and stay.
	Kept []Held
}

// ApplyResult is what Apply did.
type ApplyResult struct {
	Moved   []TagMove
	Removed []Held
	// Skipped are planned manifests that changed after the plan was made.
	Skipped []Held
}

// Apply carries out a plan and stops at the first failure.
//  1. Every tag move is made and checked.
//  2. The repository is read again, and a planned manifest that gained a tag, or took back a moved one, is skipped.
//  3. The rest are removed.
func Apply(ctx context.Context, base, repo string, b Backend, p RemovePlan) (ApplyResult, error) {
	ctx = withPush(ctx)
	var res ApplyResult
	if len(p.Moves) > 0 {
		repository, err := newRepository(base, repo)
		if err != nil {
			return res, err
		}
		for _, m := range p.Moves {
			err := retryPolicyFrom(ctx).run(ctx, mutation{
				verb:  verbPublish,
				attrs: []any{"tag", m.Tag},
				do:    func(ctx context.Context) error { return repository.Tag(ctx, m.To, m.Tag) },
			})
			if err != nil {
				return res, fmt.Errorf("cannot move %s: %w", FullRef(base, repo, m.Tag), classify(err))
			}
			got, err := repository.Resolve(ctx, m.Tag)
			if err != nil {
				return res, fmt.Errorf("cannot check %s: %w", FullRef(base, repo, m.Tag), classify(err))
			}
			if got.Digest != m.To.Digest {
				return res, fmt.Errorf("%s points at %s after the move, not at %s", m.Tag, got.Digest, m.To.Digest)
			}
			res.Moved = append(res.Moved, m)
		}
	}

	now, err := b.Manifests(ctx)
	if err != nil {
		return res, err
	}
	current := map[digest.Digest]Held{}
	for _, h := range now {
		current[h.Desc.Digest] = h
	}
	movedTags := map[string]bool{}
	for _, m := range p.Moves {
		movedTags[m.Tag] = true
	}
	for _, planned := range p.Removes {
		held, ok := current[planned.Desc.Digest]
		if !ok {
			continue
		}
		if changed(planned, held, movedTags) {
			res.Skipped = append(res.Skipped, held)
			continue
		}
		err := retryPolicyFrom(ctx).run(ctx, mutation{
			verb:  verbRemove,
			attrs: []any{"manifest", held.Desc.Digest.String()},
			do:    func(ctx context.Context) error { return b.Remove(ctx, held) },
		})
		if err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, held)
	}
	return res, nil
}

// changed reports whether a manifest holds a tag it did not hold when planned,
// or holds one the plan moved away.
func changed(planned, now Held, moved map[string]bool) bool {
	for _, tag := range now.Tags {
		if !slices.Contains(planned.Tags, tag) || moved[tag] {
			return true
		}
	}
	return false
}
