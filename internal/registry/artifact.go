package registry

import (
	"context"
	"errors"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ResolveArtifact locates "<base>/<repo>:<tag>" and returns the manifest
// descriptor this machine can use, plus its annotations, without the payload.
//
//   - tag is a tag or a "sha256:…" digest.
//   - A multi-arch index resolves to this platform's child.
//   - A noarch artifact is a bare manifest and comes back as it is.
func ResolveArtifact(ctx context.Context, base, repo, tag string) (ocispec.Descriptor, map[string]string, error) {
	repository, err := newRepository(base, repo)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	desc, annotations, err := resolvePlatformManifest(ctx, repository, tag)
	if err != nil {
		return desc, nil, fmt.Errorf("cannot resolve %s: %w", FullRef(base, repo, tag), classify(err))
	}
	return desc, annotations, nil
}

// ListTags returns every tag published under "<base>/<repo>", in whatever order
// the registry pages them.
//
// A repository with nothing in it answers 404, which is reported as no tags
// rather than as an error: a name nobody has pushed yet is the ordinary state of
// a name, not a failure.
func ListTags(ctx context.Context, base, repo string) ([]string, error) {
	repository, err := newRepository(base, repo)
	if err != nil {
		return nil, err
	}
	var tags []string
	err = repository.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	})
	if err != nil {
		if err = classify(err); errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list tags for %s/%s: %w", TrimBaseScheme(base), repo, err)
	}
	return tags, nil
}
