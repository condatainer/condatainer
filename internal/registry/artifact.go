package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// ResolveArtifact locates "<base>/<repo>" at ref and returns the manifest
// descriptor plus its annotations, without the payload.
//
//   - ref is a tag or a "sha256:…" digest.
//   - A repository holds one manifest per architecture, so a reference never names an index.
func ResolveArtifact(ctx context.Context, base, repo, ref string) (ocispec.Descriptor, map[string]string, error) {
	repository, err := newRepository(base, repo)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	desc, annotations, err := resolveManifest(ctx, repository, ref)
	if err != nil {
		return desc, nil, fmt.Errorf("cannot resolve %s: %w", FullRef(base, repo, ref), classify(err))
	}
	return desc, annotations, nil
}

// resolveDescriptor resolves ref to its image manifest's descriptor without reading the manifest.
//   - Resolving is a HEAD, which no pull limit counts. Reading the manifest is a GET, which one does.
func resolveDescriptor(ctx context.Context, repository *remote.Repository, ref string) (ocispec.Descriptor, error) {
	desc, err := repository.Resolve(ctx, ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if desc.MediaType == ocispec.MediaTypeImageIndex {
		return desc, fmt.Errorf("%w: it is an image index, and a repository holds one manifest per architecture",
			ErrInvalidArtifact)
	}
	return desc, nil
}

// ResolveTag returns the descriptor of the manifest ref names in "<base>/<repo>", without reading it.
func ResolveTag(ctx context.Context, base, repo, ref string) (ocispec.Descriptor, error) {
	repository, err := newRepository(base, repo)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	desc, err := resolveDescriptor(ctx, repository, ref)
	if err != nil {
		return desc, fmt.Errorf("cannot resolve %s: %w", FullRef(base, repo, ref), classify(err))
	}
	return desc, nil
}

// resolveManifest resolves ref to its image manifest and reads the annotations.
func resolveManifest(ctx context.Context, repository *remote.Repository, ref string) (ocispec.Descriptor, map[string]string, error) {
	desc, err := resolveDescriptor(ctx, repository, ref)
	if err != nil {
		return desc, nil, err
	}
	manifest, err := fetchManifest(ctx, repository, desc)
	if err != nil {
		return desc, nil, err
	}
	desc.ArtifactType = manifest.ArtifactType
	return desc, manifest.Annotations, nil
}

// manifests remembers the manifests this process has read or written, by repository and digest.
//   - A digest names its content, so a remembered manifest never goes stale.
//   - A lookup and the pull that follows share one fetch, which matters where each manifest fetch counts against a pull limit.
//   - The bytes are kept as the registry holds them, since a tag can only be written with the exact bytes of its digest.
var manifests = struct {
	sync.Mutex
	byDigest map[string]rememberedManifest
}{byDigest: map[string]rememberedManifest{}}

type rememberedManifest struct {
	raw      []byte
	manifest ocispec.Manifest
}

// manifestCacheLimit bounds the memory of manifests.
const manifestCacheLimit = 512

func manifestKey(repository *remote.Repository, desc ocispec.Descriptor) string {
	return repository.Reference.Registry + "/" + repository.Reference.Repository + "@" + desc.Digest.String()
}

// fetchManifest reads and decodes an image manifest, once per digest.
func fetchManifest(ctx context.Context, repository *remote.Repository, desc ocispec.Descriptor) (ocispec.Manifest, error) {
	remembered, err := rememberedFor(ctx, repository, desc)
	return remembered.manifest, err
}

// manifestBytes returns the manifest exactly as the registry holds it, once per digest.
func manifestBytes(ctx context.Context, repository *remote.Repository, desc ocispec.Descriptor) ([]byte, error) {
	remembered, err := rememberedFor(ctx, repository, desc)
	return remembered.raw, err
}

func rememberedFor(ctx context.Context, repository *remote.Repository, desc ocispec.Descriptor) (rememberedManifest, error) {
	key := manifestKey(repository, desc)
	manifests.Lock()
	cached, ok := manifests.byDigest[key]
	manifests.Unlock()
	if ok {
		return cached, nil
	}
	data, err := content.FetchAll(ctx, repository, desc)
	if err != nil {
		return rememberedManifest{}, fmt.Errorf("failed to fetch manifest: %w", err)
	}
	return rememberManifest(repository, desc, data)
}

// rememberManifest records a manifest this process holds the bytes of.
func rememberManifest(repository *remote.Repository, desc ocispec.Descriptor, raw []byte) (rememberedManifest, error) {
	var manifest ocispec.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return rememberedManifest{}, fmt.Errorf("invalid image manifest: %w", err)
	}
	remembered := rememberedManifest{raw: raw, manifest: manifest}
	manifests.Lock()
	if len(manifests.byDigest) >= manifestCacheLimit {
		clear(manifests.byDigest)
	}
	manifests.byDigest[manifestKey(repository, desc)] = remembered
	manifests.Unlock()
	return remembered, nil
}

// Address names one manifest of a repository by artifact name.
//   - With neither Identity nor Equiv it is the manifest the name tag serves.
//   - Identity selects one build, and Equiv the newest build of an equivalence class.
type Address struct {
	Name string
	// Identity is a complete key digest or a hex prefix of one. Scheme, when set, must also match.
	Identity string
	Scheme   string
	// Equiv is an equivalence digest or a hex prefix of at least a tag's width.
	Equiv string
	// Arch is the architecture wanted. Empty is the host architecture, then noarch.
	Arch string
}

// Found is the manifest an Address located.
type Found struct {
	Desc ocispec.Descriptor
	// Annotations are the manifest's own, empty for a [Probe].
	Annotations map[string]string
	Tag         string
	Arch        string
}

// arches lists the architectures to try, in order.
func (a Address) arches() ([]string, error) {
	if a.Arch != "" {
		if !ValidArch(a.Arch) {
			return nil, fmt.Errorf("%q is not an architecture a manifest is published for", a.Arch)
		}
		return []string{a.Arch}, nil
	}
	if host := meta.NativeArch(); host == ArchAMD64 || host == ArchARM64 {
		return []string{host, ArchNoarch}, nil
	}
	return []string{ArchNoarch}, nil
}

// tag spells the tag this address has for one architecture.
//   - An identity prefix shorter than a tag's width is matched against the repository's build tags.
func (a Address) tag(ctx context.Context, repository *remote.Repository, arch string) (string, error) {
	switch {
	case a.Identity != "" && a.Equiv != "":
		return "", fmt.Errorf("an address takes an identity or an equivalence key, not both")
	case a.Identity != "":
		if !isHex(a.Identity) {
			return "", fmt.Errorf("identity %q is not a hexadecimal digest", a.Identity)
		}
		if len(a.Identity) >= keyChars {
			return spell(a.Name, a.Identity[:keyChars], arch)
		}
		return a.shortBuildTag(ctx, repository, arch)
	case a.Equiv != "":
		if !isHex(a.Equiv) || len(a.Equiv) < keyChars {
			return "", fmt.Errorf("equivalence key %q needs %d hexadecimal characters", a.Equiv, keyChars)
		}
		return spell(a.Name, equivMarker+a.Equiv[:keyChars], arch)
	}
	return NameTag(a.Name, arch)
}

// shortBuildTag finds the one build tag of the name and architecture that starts with a short identity prefix.
func (a Address) shortBuildTag(ctx context.Context, repository *remote.Repository, arch string) (string, error) {
	encoded, err := EncodeName(a.Name)
	if err != nil {
		return "", err
	}
	head, tail := encoded+tagSeparator, tagSeparator+arch
	var matches []string
	err = repository.Tags(ctx, "", func(page []string) error {
		for _, tag := range page {
			qualifier, ok := strings.CutPrefix(tag, head)
			if !ok {
				continue
			}
			if qualifier, ok = strings.CutSuffix(qualifier, tail); ok && strings.HasPrefix(qualifier, a.Identity) && isHex(qualifier) {
				matches = append(matches, tag)
			}
		}
		return nil
	})
	if err != nil {
		return "", classify(err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: no %s build of %s starts with %s", ErrNotFound, arch, a.Name, a.Identity)
	case 1:
		return matches[0], nil
	}
	slices.Sort(matches)
	return "", fmt.Errorf("%w: %s matches %d builds of %s: %s",
		ErrMismatch, a.Identity, len(matches), a.Name, strings.Join(matches, ", "))
}

// verify checks that a located manifest carries the key the address asked for.
func (a Address) verify(ann map[string]string) error {
	if a.Identity != "" {
		got := Identity(ann)
		if !strings.HasPrefix(got.SHA256, a.Identity) || a.Scheme != "" && got.Scheme != a.Scheme {
			return fmt.Errorf("%w: identity %s, wanted %s", ErrMismatch, describeKey(got), a.Identity)
		}
	}
	if a.Equiv != "" {
		if got := Equiv(ann); !strings.HasPrefix(got.SHA256, a.Equiv) {
			return fmt.Errorf("%w: equivalence %s, wanted %s", ErrMismatch, describeKey(got), a.Equiv)
		}
	}
	return nil
}

// Lookup resolves an address in "<base>/<repo>".
//   - It tries each architecture in turn and returns the first manifest found.
//   - A miss on every architecture is ErrNotFound. [PublishedArches] says what exists instead.
func Lookup(ctx context.Context, base, repo string, a Address) (Found, error) {
	return lookup(ctx, base, repo, a, true)
}

// Probe resolves an address like [Lookup] without reading the manifest.
//   - It answers whether the tag exists and which digest it holds, from the tag alone.
//   - The tag carries the key's prefix, so the full key is not compared.
//   - A caller that goes on to download reads the manifest then and checks the keys.
func Probe(ctx context.Context, base, repo string, a Address) (Found, error) {
	return lookup(ctx, base, repo, a, false)
}

func lookup(ctx context.Context, base, repo string, a Address, read bool) (Found, error) {
	arches, err := a.arches()
	if err != nil {
		return Found{}, err
	}
	repository, err := newRepository(base, repo)
	if err != nil {
		return Found{}, err
	}
	var miss error
	for _, arch := range arches {
		tag, err := a.tag(ctx, repository, arch)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				miss = err
				continue
			}
			return Found{}, err
		}
		if held, known := indexed(ctx, base, repo, tag); known {
			if !held {
				miss = fmt.Errorf("cannot resolve %s: %w", FullRef(base, repo, tag), ErrNotFound)
				continue
			}
			if !read {
				return Found{Tag: tag, Arch: arch}, nil
			}
		}
		var desc ocispec.Descriptor
		var ann map[string]string
		if read {
			desc, ann, err = resolveManifest(ctx, repository, tag)
		} else {
			desc, err = resolveDescriptor(ctx, repository, tag)
		}
		if err != nil {
			err = fmt.Errorf("cannot resolve %s: %w", FullRef(base, repo, tag), classify(err))
			if errors.Is(err, ErrNotFound) {
				miss = err
				continue
			}
			return Found{}, err
		}
		if !read {
			return Found{Desc: desc, Tag: tag, Arch: arch}, nil
		}
		if err := a.verify(ann); err != nil {
			return Found{}, fmt.Errorf("%s: %w", FullRef(base, repo, tag), err)
		}
		return Found{Desc: desc, Annotations: ann, Tag: tag, Arch: arch}, nil
	}
	return Found{}, miss
}

// PublishedArches lists the architectures a has a manifest for, so a miss on the
// host architecture can say what is published instead. A failed lookup of one
// architecture counts as not published.
func PublishedArches(ctx context.Context, base, repo string, a Address) []string {
	var out []string
	for _, arch := range []string{ArchAMD64, ArchARM64, ArchNoarch} {
		probe := a
		probe.Arch = arch
		if _, err := Lookup(ctx, base, repo, probe); err == nil {
			out = append(out, arch)
		}
	}
	return out
}

// ExplainMiss turns a not-found lookup into ErrUnsupportedPlatform when the
// address is published for other architectures, naming them. Any other error
// passes through.
func ExplainMiss(ctx context.Context, base, repo string, a Address, err error) error {
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	arches := PublishedArches(ctx, base, repo, a)
	if len(arches) == 0 {
		return err
	}
	return fmt.Errorf("%w: %s is published for %s only: %w", ErrUnsupportedPlatform,
		a.Name, strings.Join(arches, ", "), err)
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
