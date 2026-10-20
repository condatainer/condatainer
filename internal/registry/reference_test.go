package registry

import (
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
)

// Normalize and TrimBaseScheme both "clean up a reference" and do opposite things:
// one supplies a registry host that was left out, the other removes a scheme that
// was typed. Confusing them silently corrupts a base, so pin a case each way.
func TestNormalizeAndTrimBaseSchemeAreNotInterchangeable(t *testing.T) {
	t.Run("Normalize supplies a host, TrimBaseScheme does not", func(t *testing.T) {
		const short = "ubuntu:24.04"
		got, err := Normalize(short)
		if err != nil {
			t.Fatalf("Normalize: %v", err)
		}
		if got != "docker.io/library/ubuntu:24.04" {
			t.Errorf("Normalize(%q) = %q", short, got)
		}
		if got := TrimBaseScheme(short); got != short {
			t.Errorf("TrimBaseScheme(%q) = %q, want it unchanged", short, got)
		}
	})

	t.Run("TrimBaseScheme removes a scheme, Normalize rejects one", func(t *testing.T) {
		const scheme = "oci://ghcr.io/lab/cnt"
		if got := TrimBaseScheme(scheme); got != "ghcr.io/lab/cnt" {
			t.Errorf("TrimBaseScheme(%q) = %q", scheme, got)
		}
		if _, err := Normalize(scheme); err == nil {
			t.Errorf("Normalize(%q) accepted a URI", scheme)
		}
	})
}

func TestTrimBaseScheme(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ghcr.io/lab/cnt", "ghcr.io/lab/cnt"},
		{"oci://ghcr.io/lab/cnt", "ghcr.io/lab/cnt"},
		{"oras://ghcr.io/lab/cnt", "ghcr.io/lab/cnt"},
		{"  ghcr.io/lab/cnt  ", "ghcr.io/lab/cnt"},
		{"ghcr.io/lab/cnt/", "ghcr.io/lab/cnt"},
		{"oci://ghcr.io/lab/cnt///", "ghcr.io/lab/cnt"},
		{"localhost:5000/cnt", "localhost:5000/cnt"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := TrimBaseScheme(tt.in); got != tt.want {
			t.Errorf("TrimBaseScheme(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A digest joins with @ and a tag with :, so one function serves both selectors.
func TestFullRef(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct{ base, repo, tag, want string }{
		{"ghcr.io/lab", "cnt", "cellranger--9.0.1__amd64", "ghcr.io/lab/cnt:cellranger--9.0.1__amd64"},
		{"oci://ghcr.io/lab/", "cnt", "ubuntu24--base__noarch", "ghcr.io/lab/cnt:ubuntu24--base__noarch"},
		{"ghcr.io/lab", "cnt", digest, "ghcr.io/lab/cnt@" + digest},
	}
	for _, tt := range tests {
		if got := FullRef(tt.base, tt.repo, tt.tag); got != tt.want {
			t.Errorf("FullRef(%q, %q, %q) = %q, want %q", tt.base, tt.repo, tt.tag, got, tt.want)
		}
	}
}

// Plain HTTP is a downgrade, so it must reach loopback and stop there.
func TestIsLoopback(t *testing.T) {
	for _, host := range []string{"localhost", "localhost:5000", "127.0.0.1", "127.0.0.1:5000", "::1", "[::1]:5000"} {
		if !isLoopback(host) {
			t.Errorf("isLoopback(%q) = false, want true", host)
		}
	}
	for _, host := range []string{
		"ghcr.io", "registry.example.test:5000", "localhost.example.test", "127.0.0.1.example.test",
	} {
		if isLoopback(host) {
			t.Errorf("isLoopback(%q) = true — TLS must not be downgraded for it", host)
		}
	}
}

func builtManifest(name, arch string, buildType meta.BuildType) meta.Manifest {
	return meta.Manifest{
		Name:      name,
		Type:      catalog.TypeApp,
		BuildType: buildType,
		Platform:  meta.Platform{OS: "linux", Arch: arch},
		Keys: meta.Keys{
			Identity: meta.KeyRef{Scheme: "script-identity-v1", SHA256: "a31f902c12ab" + strings.Repeat("0", 52)},
			Equiv:    meta.KeyRef{Scheme: "script-equiv-v1", SHA256: "e90d3b7714cf" + strings.Repeat("1", 52)},
		},
	}
}

func TestTagSpelling(t *testing.T) {
	identity := meta.KeyRef{Scheme: "script-identity-v1", SHA256: "a31f902c12ab" + strings.Repeat("0", 52)}
	equiv := meta.KeyRef{Scheme: "script-equiv-v1", SHA256: "e90d3b7714cf" + strings.Repeat("1", 52)}
	tests := []struct {
		name, arch                string
		wantName, wantBuild, want string
	}{
		{"star/2.7.11b", "amd64", "star--2.7.11b__amd64", "star--2.7.11b__a31f902c12ab__amd64", "star--2.7.11b__eq-e90d3b7714cf__amd64"},
		{"ubuntu24/build-essential", "noarch", "ubuntu24--build-essential__noarch", "ubuntu24--build-essential__a31f902c12ab__noarch", "ubuntu24--build-essential__eq-e90d3b7714cf__noarch"},
		{"grch38/star/2.7.11b/Gencode49", "arm64", "grch38--star--2.7.11b--Gencode49__arm64", "grch38--star--2.7.11b--Gencode49__a31f902c12ab__arm64", "grch38--star--2.7.11b--Gencode49__eq-e90d3b7714cf__arm64"},
		{"myenv", "amd64", "myenv__amd64", "myenv__a31f902c12ab__amd64", "myenv__eq-e90d3b7714cf__amd64"},
	}
	for _, tt := range tests {
		gotName, err := NameTag(tt.name, tt.arch)
		if err != nil {
			t.Fatalf("NameTag(%q): %v", tt.name, err)
		}
		gotBuild, _ := BuildTag(tt.name, identity, tt.arch)
		gotEquiv, _ := EquivTag(tt.name, equiv, tt.arch)
		if gotName != tt.wantName || gotBuild != tt.wantBuild || gotEquiv != tt.want {
			t.Errorf("%s/%s = %q, %q, %q; want %q, %q, %q", tt.name, tt.arch,
				gotName, gotBuild, gotEquiv, tt.wantName, tt.wantBuild, tt.want)
		}
	}
}

func TestTagsRefuseWhatCannotBeATag(t *testing.T) {
	identity := meta.KeyRef{Scheme: "script-identity-v1", SHA256: strings.Repeat("a", 64)}
	if _, err := NameTag("star/2.7.11b", "s390x"); err == nil {
		t.Error("an architecture nothing is published for must be refused")
	}
	// Refused, never truncated: a truncated tag is another artifact's address.
	if _, err := NameTag(strings.Repeat("segment/", 20)+"1.0", "amd64"); err == nil {
		t.Error("a name too long for an OCI tag must be refused")
	}
	for _, name := range []string{"", "star/2.7.11b:tag", "star//2.7.11b", "star/2__0", "star/-1", "star/1.0."} {
		if _, err := NameTag(name, "amd64"); err == nil {
			t.Errorf("NameTag(%q) accepted a name that does not fit the tag grammar", name)
		}
	}
	if _, err := BuildTag("star/2.7.11b", meta.KeyRef{}, "amd64"); err == nil {
		t.Error("a build with no identity has no build tag")
	}
	if _, err := BuildTag("star/2.7.11b", identity, "amd64"); err != nil {
		t.Errorf("BuildTag: %v", err)
	}
}

// Each build type takes the tags its consumers read: the equivalence tag exists
// for the prebuilt check, which a Conda build and a frozen environment never use.
func TestTagsFor(t *testing.T) {
	tests := []struct {
		why       string
		buildType meta.BuildType
		arch      string
		req       TagRequest
		want      []string
	}{
		{"a script build takes all three", meta.BuildTypeScript, "amd64", TagRequest{Name: true, Equiv: true},
			[]string{"star__a31f902c12ab__amd64", "star__eq-e90d3b7714cf__amd64", "star__amd64"}},
		{"a def build takes all three", meta.BuildTypeDef, "noarch", TagRequest{Name: true, Equiv: true},
			[]string{"star__a31f902c12ab__noarch", "star__eq-e90d3b7714cf__noarch", "star__noarch"}},
		{"a Conda build takes no equivalence tag", meta.BuildTypeConda, "arm64", TagRequest{Name: true, Equiv: true},
			[]string{"star__a31f902c12ab__arm64", "star__arm64"}},
		{"a frozen environment takes no equivalence tag", meta.BuildTypeSnapshot, "amd64", TagRequest{Name: true, Equiv: true},
			[]string{"star__a31f902c12ab__amd64", "star__amd64"}},
		{"a project closure entry takes only the build tag", meta.BuildTypeScript, "amd64", TagRequest{},
			[]string{"star__a31f902c12ab__amd64"}},
		{"a project pin adds the name tag", meta.BuildTypeScript, "amd64", TagRequest{Name: true},
			[]string{"star__a31f902c12ab__amd64", "star__amd64"}},
	}
	for _, tt := range tests {
		tags, err := TagsFor(builtManifest("star", tt.arch, tt.buildType), tt.req)
		if err != nil {
			t.Fatalf("%s: %v", tt.why, err)
		}
		if got := tags.List(); !slices.Equal(got, tt.want) {
			t.Errorf("%s: tags = %v, want %v", tt.why, got, tt.want)
		}
	}
}
