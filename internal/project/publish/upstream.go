// Package publish resolves where a project's artifacts already live in an OCI
// registry, and where it puts the ones that do not.
//
// It knows about locks, collections, and the registry transport; none of them
// knows about it. In particular internal/project/lock gains no registry import,
// which is what keeps `project validate --lock-only` checkout-local by
// construction rather than by discipline.
package publish

import (
	"context"
	"strings"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/registry"
)

// Upstream finds the vendored artifacts a recipe collection already publishes at the exact identity the lock pins, and returns one remote per match.
//   - It is a resolve-and-compare, never a search: name and identity are known, so it asks one coordinate per declared endpoint.
//   - Nothing enumerates tags.
//   - It is best effort.
//   - Any failure records nothing and is not an error: locking works offline, and an absent remote costs a rebuild.
func Upstream(ctx context.Context, root string, artifacts []string, cat catalog.Catalog) map[string][]lock.Remote {
	if len(artifacts) == 0 || len(cat) == 0 {
		return nil
	}
	log := logging.FromContext(ctx)
	out := make(map[string][]lock.Remote)
	for _, artifact := range artifacts {
		entry, err := lock.ReadEntry(root, artifact)
		if err != nil {
			log.Debug("cannot read a vendored artifact while looking for upstream copies", "artifact", artifact, "err", err)
			continue
		}
		remote, ok := upstreamOf(ctx, entry, cat)
		if !ok {
			continue
		}
		out[artifact] = []lock.Remote{remote}
		log.Debug("upstream publishes this exact identity", "name", entry.Manifest.Name,
			"repository", remote.Repository, "digest", remote.ManifestDigest)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// upstreamOf returns the artifact's collection registry as its remote when that
// registry advertises this exact identity.
func upstreamOf(ctx context.Context, entry *lock.Entry, cat catalog.Catalog) (lock.Remote, bool) {
	source := collectionOf(entry.Manifest, cat)
	if source == nil {
		return lock.Remote{}, false
	}
	repo, tag, err := registry.PullReference(entry.Manifest.Type, entry.Manifest.Name)
	if err != nil {
		return lock.Remote{}, false
	}
	log := logging.FromContext(ctx)
	endpoint := source.Desc.OCI.Registry
	desc, annotations, err := registry.ResolveArtifact(registry.WithSource(ctx, source.Base), endpoint, repo, tag)
	if err != nil {
		log.Debug("registry did not answer for this artifact", "endpoint", endpoint, "name", entry.Manifest.Name, "err", err)
		return lock.Remote{}, false
	}
	// Identity, not equivalence. A lock remote is an address for one exact
	// build: an equivalent artifact at that digest is a different build
	// wearing the right label, and restore would spend the bytes fetching it
	// and then reject it against the lock.
	if got := registry.Identity(annotations); got != entry.Identity {
		log.Debug("registry publishes a different build", "endpoint", endpoint,
			"name", entry.Manifest.Name, "published", describe(got), "locked", entry.Identity.Digest())
		return lock.Remote{}, false
	}
	return lock.Remote{
		Repository:     strings.TrimRight(registry.TrimBaseScheme(endpoint), "/") + "/" + repo,
		ManifestDigest: desc.Digest.String(),
	}, true
}

// collectionOf finds the configured source an artifact was built from, by the
// collection repository its manifest recorded.
//
// A collection that is unreadable, stale, or declares no registry answers
// nothing: its registry defaults cannot be trusted, and a coordinate guessed
// from a broken descriptor would be written into a tracked file.
func collectionOf(m meta.Manifest, cat catalog.Catalog) *catalog.Source {
	want := strings.TrimRight(strings.TrimSpace(m.Build.Source), "/")
	if want == "" {
		return nil
	}
	var found *catalog.Source
	for _, source := range cat {
		if strings.TrimRight(strings.TrimSpace(source.Desc.Source), "/") != want {
			continue
		}
		if found != nil {
			// Two collections claiming one repository: nothing here can say which
			// published the artifact, and picking either would be a guess.
			return nil
		}
		found = source
	}
	if found == nil || found.DescriptorErr != nil || found.Err != nil || found.Stale {
		return nil
	}
	if found.Desc.OCI.Registry == "" {
		return nil
	}
	return found
}

func describe(k meta.KeyRef) string {
	if k.Empty() {
		return "no key"
	}
	return k.Digest()
}
