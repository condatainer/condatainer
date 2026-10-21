package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/capsule"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/image"
)

// Architectures a manifest is published for. A tag and the architecture
// annotation spell them alike.
const (
	ArchAMD64  = "amd64"
	ArchARM64  = "arm64"
	ArchNoarch = meta.ArchNone
)

// ValidArch reports whether arch is one a manifest is published for.
func ValidArch(arch string) bool {
	return arch == ArchAMD64 || arch == ArchARM64 || arch == ArchNoarch
}

const (
	// tagSeparator joins the encoded name, a qualifier and the architecture.
	// Name segments never hold a doubled separator, so it cannot occur inside a name.
	tagSeparator = "__"
	// equivMarker starts an equivalence qualifier. A build qualifier is bare hex.
	equivMarker = "eq-"
	// keyChars is how many hex characters of a key a tag carries.
	keyChars = capsule.IdentityChars
)

var (
	nameSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$`)
	ociTagPattern      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// TrimBaseScheme strips an optional oci:// or oras:// scheme and trailing
// slashes from a registry host or repository coordinate.
func TrimBaseScheme(base string) string {
	base = strings.TrimSpace(base)
	for _, scheme := range []string{"oci://", "oras://"} {
		base = strings.TrimPrefix(base, scheme)
	}
	return strings.TrimRight(base, "/")
}

// FullRef joins a registry host, a repository path, and a tag or digest into one
// pullable reference. A digest joins with "@", a tag with ":".
func FullRef(base, repo, tag string) string {
	if strings.HasPrefix(tag, digestPrefix) {
		return TrimBaseScheme(base) + "/" + repo + "@" + tag
	}
	return TrimBaseScheme(base) + "/" + repo + ":" + tag
}

// digestPrefix is the only digest algorithm this package writes or accepts.
const digestPrefix = "sha256:"

// EncodeName spells an artifact name as the first part of its tags. Every segment
// must match the name grammar, so a doubled separator is refused.
func EncodeName(name string) (string, error) {
	normalized := catalog.Normalize(name)
	if normalized == "" || strings.ContainsAny(normalized, ":@") {
		return "", fmt.Errorf("registry name %q is empty or carries a selector", name)
	}
	for _, segment := range strings.Split(normalized, "/") {
		if !nameSegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("name segment %q of %q does not fit a registry tag", segment, name)
		}
	}
	return image.EncodeArtifactName(normalized), nil
}

// NameTag is the tag a name is served under for one architecture.
func NameTag(name, arch string) (string, error) { return spell(name, "", arch) }

// BuildTag is the tag of one build for one architecture.
func BuildTag(name string, identity meta.KeyRef, arch string) (string, error) {
	prefix, err := keyPrefix(identity)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return spell(name, prefix, arch)
}

// EquivTag is the tag of the newest build of an equivalence class for one architecture.
func EquivTag(name string, equiv meta.KeyRef, arch string) (string, error) {
	prefix, err := keyPrefix(equiv)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return spell(name, equivMarker+prefix, arch)
}

func keyPrefix(k meta.KeyRef) (string, error) {
	sha := strings.TrimPrefix(k.SHA256, digestPrefix)
	if len(sha) < keyChars {
		return "", fmt.Errorf("records no key to tag it under")
	}
	return sha[:keyChars], nil
}

// spell joins name, qualifier and architecture, refusing a tag that does not fit.
// A tag is refused and never truncated: a truncated tag is another artifact's address.
func spell(name, qualifier, arch string) (string, error) {
	encoded, err := EncodeName(name)
	if err != nil {
		return "", err
	}
	if !ValidArch(arch) {
		return "", fmt.Errorf("%q is not an architecture a manifest is published for", arch)
	}
	tag := encoded
	if qualifier != "" {
		tag += tagSeparator + qualifier
	}
	tag += tagSeparator + arch
	if len(tag) > maxTagLength || !ociTagPattern.MatchString(tag) {
		return "", fmt.Errorf("%s does not fit an OCI tag as %q", name, tag)
	}
	return tag, nil
}

// IsNameTag reports whether tag is a name tag, which has one separator. A build
// or equivalence tag has two.
func IsNameTag(tag string) bool { return strings.Count(tag, tagSeparator) == 1 }

// tagKind is what a tag's spelling says it is.
type tagKind int

const (
	kindOther tagKind = iota
	kindName
	kindBuild
	kindEquiv
)

// parsedTag is a tag split into the parts its spelling carries.
type parsedTag struct {
	Kind tagKind
	// Encoded is the encoded name, Qualifier the hex key, Arch the architecture.
	Encoded, Qualifier, Arch string
}

// parseTag reads a tag's spelling. Any tag this package does not write is kindOther.
func parseTag(tag string) parsedTag {
	parts := strings.Split(tag, tagSeparator)
	switch {
	case len(parts) == 2 && parts[0] != "" && ValidArch(parts[1]):
		return parsedTag{Kind: kindName, Encoded: parts[0], Arch: parts[1]}
	case len(parts) == 3 && parts[0] != "" && ValidArch(parts[2]):
		q := parts[1]
		if hex, ok := strings.CutPrefix(q, equivMarker); ok && len(hex) == keyChars && isHex(hex) {
			return parsedTag{Kind: kindEquiv, Encoded: parts[0], Qualifier: hex, Arch: parts[2]}
		}
		if len(q) == keyChars && isHex(q) {
			return parsedTag{Kind: kindBuild, Encoded: parts[0], Qualifier: q, Arch: parts[2]}
		}
	}
	return parsedTag{}
}

// TagRequest says which tags a push writes besides the build tag.
type TagRequest struct {
	// Name also writes the name tag.
	Name bool
	// Equiv also writes the equivalence tag, for a build type that takes one.
	Equiv bool
}

// Tags are the tags one push writes for one manifest. An unwritten tag is empty.
type Tags struct {
	Build, Equiv, Name string
}

// List returns the written tags, build first, so a push that stops part way leaves a tagged manifest.
func (t Tags) List() []string {
	var out []string
	for _, tag := range []string{t.Build, t.Equiv, t.Name} {
		if tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// TagsFor spells the tags a push of m writes.
//   - Every tag is spelled before anything is uploaded, so a name that does not fit fails first.
//   - Only a script or def build takes an equivalence tag. A Conda build has no prebuilt to match, and a frozen environment's equivalence key is its identity.
func TagsFor(m meta.Manifest, req TagRequest) (Tags, error) {
	arch := m.Platform.Arch
	build, err := BuildTag(m.Name, m.Keys.Identity, arch)
	if err != nil {
		return Tags{}, err
	}
	tags := Tags{Build: build}
	if req.Equiv && takesEquivTag(m.BuildType) {
		if tags.Equiv, err = EquivTag(m.Name, m.Keys.Equiv, arch); err != nil {
			return Tags{}, err
		}
	}
	if req.Name {
		if tags.Name, err = NameTag(m.Name, arch); err != nil {
			return Tags{}, err
		}
	}
	return tags, nil
}

// TakesEquivTag reports whether a build type is published under an equivalence tag.
func TakesEquivTag(t meta.BuildType) bool { return takesEquivTag(t) }

func takesEquivTag(t meta.BuildType) bool {
	return t == meta.BuildTypeScript || t == meta.BuildTypeDef
}

// isHex reports whether s is a nonempty string of lowercase hex digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	return strings.TrimLeft(s, "0123456789abcdef") == ""
}
