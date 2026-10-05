package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/opencontainers/go-digest"

	"github.com/condatainer/condatainer/internal/artifact/capsule"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/image"
)

// RollingTag moves with every push of a version-less artifact, so a puller that
// does not know a build date still resolves the current one.
const RollingTag = "latest"

// dateTagLayout formats a version-less artifact's build-date tag.
const dateTagLayout = "20060102"

var (
	ociRepoSegmentPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	ociTagPattern         = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// TrimBaseScheme strips an optional oci:// or oras:// scheme and a trailing
// slash from a registry base, which already includes the owner and prefix.
func TrimBaseScheme(base string) string {
	base = strings.TrimSpace(base)
	for _, scheme := range []string{"oci://", "oras://"} {
		base = strings.TrimPrefix(base, scheme)
	}
	return strings.TrimRight(base, "/")
}

// FullRef joins a registry base, a repository path, and a tag or digest into one
// pullable reference. A digest joins with "@", a tag with ":".
func FullRef(base, repo, tag string) string {
	if strings.HasPrefix(tag, digestPrefix) {
		return TrimBaseScheme(base) + "/" + repo + "@" + tag
	}
	return TrimBaseScheme(base) + "/" + repo + ":" + tag
}

// digestPrefix is the only digest algorithm this package writes or accepts.
const digestPrefix = "sha256:"

// isVersionLess reports whether an artifact is addressed by build date and a
// rolling tag because its name carries no version segment. The type decides how
// many segments make a version:
//
//   - os needs three: ubuntu24/build-essential is version-less, ubuntu24/r/4.4.4 is not.
//   - app and data need two: cellranger/9.0.1 is versioned, a bare `myenv` is not.
//
// The catalog parses every name as <name>/<version>, so the shape alone cannot tell.
func isVersionLess(typ catalog.Type, name string) bool {
	segments := strings.Count(catalog.Normalize(name), "/") + 1
	switch typ {
	case catalog.TypeOS:
		return segments < 3
	default:
		return segments < 2
	}
}

// ValidateIdentity rejects a name that cannot map reversibly onto an OCI repository and tag.
//   - Repository segments must already be lowercase.
//   - Nothing is down-cased here: two distinct names that differ only in case would collide into one repository, and silently publishing one over the other is worse than refusing both.
func ValidateIdentity(nameVersion string, versionLess bool) error {
	nv := catalog.Normalize(nameVersion)
	if nv == "" || strings.ContainsAny(nv, ":@") {
		return fmt.Errorf("registry identity %q is empty or carries a selector", nameVersion)
	}
	parts := strings.Split(nv, "/")
	repoParts := parts
	if !versionLess {
		if len(parts) < 2 {
			return fmt.Errorf("versioned artifact %q must be name/version", nameVersion)
		}
		tag := parts[len(parts)-1]
		if !ociTagPattern.MatchString(tag) || tag == "." || tag == ".." {
			return fmt.Errorf("identity tag segment %q is not OCI-safe", tag)
		}
		repoParts = parts[:len(parts)-1]
	}
	for _, segment := range repoParts {
		if segment == "." || segment == ".." || !ociRepoSegmentPattern.MatchString(segment) {
			return fmt.Errorf("identity repository segment %q must already be lowercase and OCI-safe", segment)
		}
	}
	return nil
}

// SplitPullSpec separates an artifact name from an optional selector.
//   - The forms are "name", "name:tag", and "name@sha256:<hex>".
//   - A colon counts as a tag separator only after the final slash, so a registry host with a port stays the separate base argument's problem.
func SplitPullSpec(spec string) (name, selector string, err error) {
	spec = strings.TrimSpace(spec)
	if at := strings.LastIndex(spec, "@"); at >= 0 {
		name, selector = catalog.Normalize(spec[:at]), spec[at+1:]
		if name == "" || digest.Digest(selector).Validate() != nil {
			return "", "", fmt.Errorf("invalid digest pull reference %q", spec)
		}
		return name, selector, nil
	}
	if slash, colon := strings.LastIndex(spec, "/"), strings.LastIndex(spec, ":"); colon > slash {
		name, selector = catalog.Normalize(spec[:colon]), spec[colon+1:]
		if name == "" || selector == "" {
			return "", "", fmt.Errorf("invalid tagged pull reference %q", spec)
		}
		return name, selector, nil
	}
	return catalog.Normalize(spec), "", nil
}

// PushReference derives the repository and ordered tags for publishing m. The first tag is canonical; a version-less artifact also gets [RollingTag].
//   - Tags are architecture-independent.
//   - A native artifact's tag resolves to an image index whose children carry per-arch platform descriptors, so pushing on each architecture builds one multi-arch tag.
func PushReference(m meta.Manifest) (repo string, tags []string, err error) {
	nv := catalog.Normalize(m.Name)
	versionLess := isVersionLess(m.Type, nv)
	if err := ValidateIdentity(nv, versionLess); err != nil {
		return "", nil, err
	}

	if versionLess {
		// No fallback to the push time: a date tag is an address, and one that
		// records when it was uploaded rather than when it was built is a lie
		// that only shows up as a wrong artifact months later.
		if m.Build.Created.IsZero() {
			return "", nil, fmt.Errorf("%s records no build time, so it has no date tag", m.Name)
		}
		return nv, []string{m.Build.Created.UTC().Format(dateTagLayout), RollingTag}, nil
	}

	idx := strings.LastIndex(nv, "/")
	return nv[:idx], []string{nv[idx+1:]}, nil
}

// PullReference derives the repository and single tag to fetch nameVersion,
// mirroring PushReference's canonical tag. A version-less artifact resolves
// RollingTag, since the puller does not know its build date; an explicit date or
// digest arrives through SplitPullSpec. typ comes from the recipe, as there is no
// manifest to read yet.
func PullReference(typ catalog.Type, nameVersion string) (repo, tag string, err error) {
	nv := catalog.Normalize(nameVersion)
	versionLess := isVersionLess(typ, nv)
	if err := ValidateIdentity(nv, versionLess); err != nil {
		return "", "", err
	}
	if versionLess {
		return nv, RollingTag, nil
	}
	idx := strings.LastIndex(nv, "/")
	return nv[:idx], nv[idx+1:], nil
}

// projectTagSeparator joins an encoded artifact name to its identity prefix in a
// project's flat tag namespace.
//
// Doubled for the same reason `--` works as the name separator: catalog's
// segment grammar allows a single `.`, `_` or `-` between alphanumerics and
// never two, so a doubled separator cannot occur inside a name. `@`, which the
// store filename uses, is not legal in an OCI tag at all.
const projectTagSeparator = "__"

// ProjectTags renders the tags a project publishes one artifact under, canonical
// first. The name lives in the tag because a project keeps one repository.
//
//   - The qualified tag is the retention anchor; every artifact gets one.
//   - A tag only keeps a manifest referenced: locks record digests, nothing fetches by tag.
//   - selected also writes the plain name tag, a moving handle; dropping it breaks no restore.
func ProjectTags(m meta.Manifest, selected bool) ([]string, error) {
	encoded := image.EncodeArtifactName(catalog.Normalize(m.Name))
	if encoded == "" {
		return nil, fmt.Errorf("artifact has no name to publish under")
	}
	sha := strings.TrimPrefix(m.Keys.Identity.SHA256, "sha256:")
	if len(sha) < capsule.IdentityChars {
		return nil, fmt.Errorf("%s records no identity to publish under", m.Name)
	}
	qualified := encoded + projectTagSeparator + sha[:capsule.IdentityChars]

	tags := []string{qualified}
	if selected {
		tags = append(tags, encoded)
	}
	for _, tag := range tags {
		// Refused, never truncated: a truncated tag is a different artifact's
		// address, and the name is what a puller reads back out of it.
		if len(tag) > maxTagLength || !ociTagPattern.MatchString(tag) {
			return nil, fmt.Errorf("%s does not fit an OCI tag as %q", m.Name, tag)
		}
	}
	return tags, nil
}

// ParseProjectTag decodes a project tag into the artifact name and the identity
// prefix qualifying it, if any. Anything else reports false, so a catalog tag is
// never read as a name. Accepted:
//
//   - a tag with a `__<hex>` identity suffix, whose name may be one component;
//   - a plain tag containing `--`, or [meta.EnvName].
func ParseProjectTag(tag string) (name, sha string, ok bool) {
	encoded, qualified := tag, false
	if base, prefix, found := strings.Cut(tag, projectTagSeparator); found {
		if base == "" || !isHex(prefix) {
			return "", "", false
		}
		encoded, sha, qualified = base, prefix, true
	}
	name = image.DecodeArtifactName(encoded)
	if catalog.Normalize(name) != name || name == "" {
		return "", "", false
	}
	if !qualified && !strings.Contains(encoded, "--") && name != meta.EnvName {
		return "", "", false
	}
	return name, sha, true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	return strings.TrimLeft(s, "0123456789abcdef") == ""
}
