package registry

import (
	"context"
	"fmt"

	"github.com/condatainer/condatainer/internal/image"
)

// PlanGC plans the removal of manifests nothing serves.
//   - A manifest qualifies when it holds a build tag and neither a name tag nor an equivalence tag, so no name and no prebuilt check can reach it.
//   - A version with no tag at all qualifies too, unless a name limits the run, since an untagged version carries no name.
//   - A manifest holding a tag this package did not write is kept.
//   - With hasRecipe, every build of a name it rejects qualifies too, served or not.
func PlanGC(ctx context.Context, b Backend, name string, hasRecipe func(name string) bool) (RemovePlan, error) {
	var encoded string
	if name != "" {
		var err error
		if encoded, err = EncodeName(name); err != nil {
			return RemovePlan{}, err
		}
	}
	held, err := b.Manifests(withPush(ctx))
	if err != nil {
		return RemovePlan{}, err
	}
	var plan RemovePlan
	for _, h := range held {
		if len(h.Tags) == 0 {
			if name == "" {
				plan.Removes = append(plan.Removes, h)
			}
			continue
		}
		var built, served, foreign, named, orphan bool
		for _, tag := range h.Tags {
			switch p := parseTag(tag); p.Kind {
			case kindBuild:
				built = true
				named = named || encoded == "" || p.Encoded == encoded
				orphan = orphan || hasRecipe != nil && !hasRecipe(image.DecodeArtifactName(p.Encoded))
			case kindName, kindEquiv:
				served = true
			default:
				foreign = true
			}
		}
		switch {
		case !built || !named || served && !orphan:
		case foreign:
			plan.Kept = append(plan.Kept, h)
		default:
			plan.Removes = append(plan.Removes, h)
		}
	}
	return plan, nil
}

// PlanUnpinned plans the removal of every manifest whose digest is not in pinned.
//   - An empty pinned set is refused, since it would remove the whole repository.
//   - A manifest keeps all its tags if pinned, and loses them all if not; no tag moves.
func PlanUnpinned(ctx context.Context, b Backend, pinned map[string]bool) (RemovePlan, error) {
	if len(pinned) == 0 {
		return RemovePlan{}, fmt.Errorf("nothing is pinned in this repository, so every manifest would be removed")
	}
	held, err := b.Manifests(withPush(ctx))
	if err != nil {
		return RemovePlan{}, err
	}
	var plan RemovePlan
	for _, h := range held {
		if !pinned[h.Desc.Digest.String()] {
			plan.Removes = append(plan.Removes, h)
		}
	}
	return plan, nil
}
