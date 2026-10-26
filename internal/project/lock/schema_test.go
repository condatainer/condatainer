package lock

import (
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

const digestA = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestMarshalIsDeterministicAndSorted(t *testing.T) {
	build := func() *Lock {
		l := New()
		l.Pins["star/2.7.11b"] = pinOf("provenance/star--2.7.11b@a31f902c12ab")
		l.Pins["cutadapt/5.0"] = pinOf("provenance/cutadapt--5.0@41b5204f99c1")
		l.Pins["grch38/genome/gencode49"] = pinOf("provenance/grch38--genome--gencode49@8ce02116f302")
		return l
	}

	first, err := build().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	second, err := build().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("two runs produced different bytes:\n%s\n---\n%s", first, second)
	}
	if !strings.HasSuffix(string(first), "}\n") {
		t.Errorf("output does not end in exactly one newline: %q", string(first[len(first)-3:]))
	}
	if !strings.Contains(string(first), "\n  \"pins\": {") {
		t.Errorf("output is not two-space indented:\n%s", first)
	}

	keys := []string{"cutadapt/5.0", "grch38/genome/gencode49", "star/2.7.11b"}
	at := -1
	for _, key := range keys {
		i := strings.Index(string(first), key)
		if i < at {
			t.Fatalf("pin keys are not sorted:\n%s", first)
		}
		at = i
	}
	if strings.Contains(string(first), "remotes") {
		t.Errorf("an empty remote map was serialized:\n%s", first)
	}
}

func TestRoundTripPreservesRemotes(t *testing.T) {
	l := New()
	l.Pins["star/2.7.11b"] = pinOf("provenance/star--2.7.11b@a31f902c12ab")
	if err := l.AddRemote("provenance/star--2.7.11b@a31f902c12ab", Remote{Repository: "ghcr.io/example/star", ManifestDigest: digestA}); err != nil {
		t.Fatal(err)
	}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Remotes["provenance/star--2.7.11b@a31f902c12ab"]
	if len(got) != 1 || got[0].Repository != "ghcr.io/example/star" || got[0].ManifestDigest != digestA {
		t.Fatalf("remotes did not round-trip: %#v", got)
	}
}

// Order is retry priority, so a repeat must not reorder and a duplicate must
// not accumulate.
func TestAddRemoteDedupesAndKeepsOrder(t *testing.T) {
	l := New()
	artifact := "provenance/star--2.7.11b@a31f902c12ab"
	first := Remote{Repository: "ghcr.io/example/star", ManifestDigest: digestA}
	second := Remote{Repository: "ghcr.io/mirror/star", ManifestDigest: digestA}
	for _, remote := range []Remote{first, second, first} {
		if err := l.AddRemote(artifact, remote); err != nil {
			t.Fatal(err)
		}
	}
	got := l.Remotes[artifact]
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("remotes = %#v", got)
	}
}

func TestAddRemoteRejectsBadLocations(t *testing.T) {
	artifact := "provenance/star--2.7.11b@a31f902c12ab"
	tests := []struct {
		why    string
		remote Remote
	}{
		{"a scheme is not part of a repository", Remote{Repository: "oci://ghcr.io/example/star", ManifestDigest: digestA}},
		{"a tag is mutable and is not an remote", Remote{Repository: "ghcr.io/example/star:latest", ManifestDigest: digestA}},
		{"an empty repository addresses nothing", Remote{Repository: "", ManifestDigest: digestA}},
		{"a tag is not a digest", Remote{Repository: "ghcr.io/example/star", ManifestDigest: "latest"}},
		{"a short digest is not sha256", Remote{Repository: "ghcr.io/example/star", ManifestDigest: "sha256:abcd"}},
		{"a digest is hex", Remote{Repository: "ghcr.io/example/star", ManifestDigest: "sha256:" + strings.Repeat("z", 64)}},
	}
	for _, tt := range tests {
		t.Run(tt.why, func(t *testing.T) {
			if err := New().AddRemote(artifact, tt.remote); err == nil {
				t.Fatalf("accepted %#v", tt.remote)
			}
		})
	}
}

// A registry may carry a port, which is a colon that does not introduce a tag.
func TestAddRemoteAcceptsARegistryPort(t *testing.T) {
	if err := New().AddRemote("provenance/star--2.7.11b@a31f902c12ab",
		Remote{Repository: "localhost:5000/example/star", ManifestDigest: digestA}); err != nil {
		t.Fatalf("rejected a ported registry: %v", err)
	}
}

// Every lock path is hostile input: it names a directory a later step reads.
func TestEntryPathsAreConstrained(t *testing.T) {
	tests := []struct {
		why  string
		path string
	}{
		{"absolute", "/etc/passwd"},
		{"traversal", "provenance/../../etc"},
		{"outside artifacts", "elsewhere/star@abc"},
		{"unclean", "provenance/./star@abc"},
		{"nested", "provenance/deep/star@abc"},
		{"empty", ""},
		{"backslash", `artifacts\star@abc`},
	}
	for _, tt := range tests {
		t.Run(tt.why, func(t *testing.T) {
			l := New()
			l.Pins["star/2.7.11b"] = pinOf(tt.path)
			if _, err := l.Marshal(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %q: %v", tt.path, err)
			}
		})
	}
}

func TestUnmarshalRejectsUnknownFieldsAndSchemas(t *testing.T) {
	tests := []struct {
		why  string
		data string
		want error
	}{
		{"unknown top-level field", `{"schema_version":1,"pins":{},"surprise":true}`, ErrInvalid},
		{"unknown pin field", `{"schema_version":1,"pins":{"a":{"artifact":"provenance/a@b","pinned":true}}}`, ErrInvalid},
		{"future schema", `{"schema_version":99,"pins":{}}`, ErrSchema},
		{"future schema with a new field", `{"schema_version":2,"pins":{},"surprise":true}`, ErrSchema},
		{"missing schema", `{"pins":{}}`, ErrSchema},
		{"trailing content", `{"schema_version":1,"pins":{}} {}`, ErrInvalid},
		{"remote outside artifacts", `{"schema_version":1,"pins":{},"remotes":{"/abs":[]}}`, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.why, func(t *testing.T) {
			if _, err := Unmarshal([]byte(tt.data)); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

// A duplicated remote in a hand-edited lock is a conflict, not something to
// silently collapse on the next write.
func TestUnmarshalRejectsDuplicateRemotes(t *testing.T) {
	data := `{"schema_version":1,"pins":{},"remotes":{"provenance/a@b":[` +
		`{"repository":"ghcr.io/x/y","manifest_digest":"` + digestA + `"},` +
		`{"repository":"ghcr.io/x/y","manifest_digest":"` + digestA + `"}]}}`
	if _, err := Unmarshal([]byte(data)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

// Closure directories are reached through manifest edges, so the pin set
// is only the root set. Pruning against it alone would delete the closure.
func TestPinnedArtifactsIsRootsOnly(t *testing.T) {
	l := New()
	l.Pins["star/2.7.11b"] = pinOf("provenance/star--2.7.11b@a31f902c12ab")
	l.Pins["also/star"] = pinOf("provenance/star--2.7.11b@a31f902c12ab")
	if got := l.PinnedArtifacts(); len(got) != 1 || !got["provenance/star--2.7.11b@a31f902c12ab"] {
		t.Fatalf("PinnedArtifacts = %#v", got)
	}
}

func TestOCIValidation(t *testing.T) {
	tests := []struct {
		why     string
		oci     OCI
		source  string
		wantErr bool
	}{
		{"nothing recorded", OCI{}, "", false},
		{"a plain coordinate", OCI{Push: "ghcr.io/my-lab/rnaseq-2026/cnt"}, "", false},
		{"a declared audience", OCI{Push: "ghcr.io/lab/p", Audience: "restricted"}, "", false},
		{"a registry with a port", OCI{Push: "localhost:5000/lab/p"}, "", false},
		{"a project source", OCI{Push: "ghcr.io/lab/p"}, "https://github.com/lab/p", false},

		{"a host with no repository", OCI{Push: "ghcr.io"}, "", true},
		{"a scheme", OCI{Push: "oci://ghcr.io/lab/p"}, "", true},
		{"a tag", OCI{Push: "ghcr.io/lab/p:latest"}, "", true},
		{"a digest", OCI{Push: "ghcr.io/lab/p@sha256:abc"}, "", true},
		{"a trailing slash", OCI{Push: "ghcr.io/lab/p/"}, "", true},
		{"a leading slash", OCI{Push: "/lab/p"}, "", true},
		// Not down-cased: two names differing only in case would collide into one
		// repository, and publishing one over the other silently is worse.
		{"an upper-case segment", OCI{Push: "ghcr.io/Lab/p"}, "", true},
		{"an unknown audience", OCI{Push: "ghcr.io/lab/p", Audience: "private"}, "", true},
		{"an audience with no destination", OCI{Audience: "restricted"}, "", true},
		{"a source that is not a URL", OCI{}, "github.com/lab/p", true},
		{"a source with whitespace", OCI{}, "https://github.com/lab/ p", true},
	}
	for _, tt := range tests {
		l := New()
		l.OCI, l.Source = tt.oci, tt.source
		if err := l.Validate(); (err != nil) != tt.wantErr {
			t.Errorf("%s: Validate() = %v, wantErr %v", tt.why, err, tt.wantErr)
		}
	}
}

// The block is absent until something records one, so a project that never ran
// `project registry set` must produce the bytes it always did.
func TestOCIRoundTripsAndStaysOutOfAnUnsetLock(t *testing.T) {
	plain, err := New().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "oci") || strings.Contains(string(plain), "source") {
		t.Errorf("an unset lock names a destination:\n%s", plain)
	}

	l := New()
	l.Source = "https://github.com/lab/p"
	l.OCI = OCI{Push: "ghcr.io/lab/p/cnt", Audience: "restricted"}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Field order is the document's contract: a lock only appears in a diff when
	// it changed.
	if want := "\"schema_version\""; !strings.HasPrefix(string(data), "{\n  "+want) {
		t.Errorf("schema_version is not first:\n%s", data)
	}
	if i, j := strings.Index(string(data), "\"source\""), strings.Index(string(data), "\"oci\""); i < 0 || j < 0 || i > j {
		t.Errorf("source must precede oci:\n%s", data)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.OCI != l.OCI || got.Source != l.Source {
		t.Errorf("round trip lost the destination: %#v / %q", got.OCI, got.Source)
	}
	if got.OCI.Empty() {
		t.Error("a recorded destination reports itself empty")
	}
}

func TestMatchIsValidatedAndOmittedWhenDefault(t *testing.T) {
	l := New()
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "match") {
		t.Errorf("an unset match was written:\n%s", data)
	}
	l.Match = MatchIdentity
	if data, err = l.Marshal(); err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(data)
	if err != nil || back.Match != MatchIdentity {
		t.Fatalf("match did not round-trip: %v", err)
	}
	l.Match = "exact"
	if _, err := l.Marshal(); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unknown match was accepted: %v", err)
	}
}

func TestPinAndArchsNameRealArchitectures(t *testing.T) {
	const path = "provenance/star--2.7.11b@a31f902c12ab"
	tests := []struct {
		why   string
		pin   map[string]string
		archs []string
	}{
		{"unknown pin architecture", map[string]string{"s390x": path}, nil},
		{"noarch with another", map[string]string{"noarch": path, "amd64": path}, nil},
		{"no artifact", map[string]string{}, nil},
		{"noarch as a covered architecture", map[string]string{"amd64": path}, []string{"noarch"}},
		{"duplicate covered architecture", map[string]string{"amd64": path}, []string{"amd64", "amd64"}},
	}
	for _, tt := range tests {
		t.Run(tt.why, func(t *testing.T) {
			l := New()
			l.Archs = tt.archs
			l.Pins["star/2.7.11b"] = PinEntry{Artifacts: tt.pin}
			if _, err := l.Marshal(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestMarshalSortsTheCoveredArchitectures(t *testing.T) {
	l := New()
	l.Archs = []string{"arm64", "amd64"}
	data, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"archs": [`+"\n"+`    "amd64",`) {
		t.Errorf("archs are not sorted:\n%s", data)
	}
}

// A lock newer than this build says so, whatever else in it this build cannot read.
func TestUnmarshalTellsANewerLockToUpdate(t *testing.T) {
	_, err := Unmarshal([]byte(`{"schema_version":2,"pins":{"a":{"artifacts":7}},"surprise":true}`))
	if !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), "update CondaTainer") {
		t.Fatalf("error = %v, want ErrSchema naming the update", err)
	}
}

// testdata/lock_v1.json is a version 1 lock as the first release writes it. Every later
// build must keep reading it to the same pins, so it is never edited to fit a change.
func TestVersion1FixtureStaysReadable(t *testing.T) {
	data, err := os.ReadFile("testdata/lock_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	l, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if l.Match != MatchIdentity || l.OCI.Push != "ghcr.io/my-lab/rnaseq/cnt" || l.OCI.Audience != "restricted" ||
		l.Source != "https://github.com/my-lab/rnaseq" || !slices.Equal(l.Archs, []string{"amd64", "arm64"}) {
		t.Errorf("header fields = %+v", l)
	}
	want := map[string]string{
		BaseKey + "|amd64":                          "provenance/ubuntu24--base@0123456789ab",
		BaseKey + "|arm64":                          "provenance/ubuntu24--base@ba9876543210",
		"grch38/genome/gencode49|noarch":            "provenance/grch38--genome--gencode49@8ce02116f302",
		PathPrefix + "overlays/combined.sqf|noarch": "provenance/env@41b5204f99c1",
		"star/2.7.11b|amd64":                        "provenance/star--2.7.11b@a31f902c12ab",
		"star/2.7.11b|arm64":                        "provenance/star--2.7.11b@f00dfeed1234",
	}
	got := map[string]string{}
	for key, pin := range l.Pins {
		for arch, path := range pin.Artifacts {
			got[key+"|"+arch] = path
		}
	}
	if !maps.Equal(got, want) {
		t.Errorf("pins = %v, want %v", got, want)
	}
	if !l.Pins[PathPrefix+"overlays/combined.sqf"].Manual {
		t.Error("the manual pin lost its flag")
	}
	if len(l.Remotes["provenance/star--2.7.11b@a31f902c12ab"]) != 1 {
		t.Errorf("remotes = %v, want one for the star artifact", l.Remotes)
	}
}
