package registry

import "context"

// TagIndex is every tag of one repository, read once. A lookup that carries it
// answers "not found" for a tag it lacks without asking the registry.
type TagIndex struct {
	repository string
	tags       map[string]struct{}
}

type tagIndexKey struct{}

// ReadTagIndex lists the tags of "<base>/<repo>".
//   - A repository nobody has pushed to has no tags.
//   - Any failure is an error, a refusal included.
func ReadTagIndex(ctx context.Context, base, repo string) (*TagIndex, error) {
	repository := TrimBaseScheme(base) + "/" + repo
	tags, err := ListTags(ctx, base, repo)
	if err != nil {
		return nil, err
	}
	index := &TagIndex{repository: repository, tags: make(map[string]struct{}, len(tags))}
	for _, tag := range tags {
		index.tags[tag] = struct{}{}
	}
	return index, nil
}

// Empty reports whether the repository has no tags.
func (i *TagIndex) Empty() bool { return len(i.tags) == 0 }

// WithTagIndex returns a context whose lookups in the index's repository consult it first.
//   - A tag the index lacks is not found, and no request is made.
//   - A probe of a tag it holds is found by the tag alone, with no descriptor.
func WithTagIndex(ctx context.Context, index *TagIndex) context.Context {
	return context.WithValue(ctx, tagIndexKey{}, index)
}

// indexed says whether the tag is in the context's index of this repository.
// known is false when the context carries no index of it.
func indexed(ctx context.Context, base, repo, tag string) (held, known bool) {
	index, _ := ctx.Value(tagIndexKey{}).(*TagIndex)
	if index == nil || index.repository != TrimBaseScheme(base)+"/"+repo {
		return false, false
	}
	_, held = index.tags[tag]
	return held, true
}
