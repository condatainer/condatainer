package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// fakeRegistry serves just enough of the distribution API to resolve a manifest
// and list tags. It exists so the classification every fallback decision depends
// on is exercised through the real oras client rather than asserted on a mock.
type fakeRegistry struct {
	server *httptest.Server
	// mu guards the maps: push and pull both run requests on server goroutines.
	mu sync.Mutex
	// content is keyed "<repo>/<tag-or-digest>".
	content map[string]ocispec.Descriptor
	blobs   map[digest.Digest][]byte
	tags    map[string][]string
	// status, when set, is returned for every request, standing in for a
	// registry that is down, private, or empty.
	status int
	// onBlob, when set, runs just before a payload blob is served. It is the
	// seam for a test that needs something to happen *during* a transfer.
	onBlob func()
	// bearer makes reads need a token from /token, which is handed out only to
	// a request with no credential: a public registry refusing a stale one.
	bearer bool
	// refuseAnonymous makes /token refuse a request with no credential too.
	refuseAnonymous bool
	// acceptUser is the one username /token hands a token to.
	acceptUser string
	// deleteStatus, when set, is the answer to every manifest delete.
	deleteStatus int
	// refusePut, when set, is a reference whose manifest write is refused.
	refusePut string
	// manifestGets counts manifest reads that return a body, the requests a pull limit counts.
	manifestGets int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{
		content: map[string]ocispec.Descriptor{},
		blobs:   map[digest.Digest][]byte{},
		tags:    map[string][]string{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// base is the registry host, which is loopback, so newRepository speaks plain
// HTTP to it and no certificate is involved.
func (f *fakeRegistry) base() string { return strings.TrimPrefix(f.server.URL, "http://") }

func (f *fakeRegistry) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status != 0 {
		// No Www-Authenticate challenge: there is no token endpoint to retry against.
		w.WriteHeader(f.status)
		return
	}
	if f.bearer {
		if r.URL.Path == "/token" {
			user, _, basic := r.BasicAuth()
			if basic && (f.acceptUser == "" || user != f.acceptUser) || !basic && f.refuseAnonymous {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"token": "anonymous"}) //nolint:errcheck
			return
		}
		if r.Header.Get("Authorization") != "Bearer anonymous" {
			w.Header().Set("Www-Authenticate", `Bearer realm="`+f.server.URL+`/token",service="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")

	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		f.accept(w, r, path)
		return
	}
	if r.Method == http.MethodDelete {
		f.remove(w, path)
		return
	}

	if repo, ok := strings.CutSuffix(path, "/tags/list"); ok {
		tags, known := f.tags[repo]
		if !known {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags}) //nolint:errcheck
		return
	}

	if _, ref, ok := cutLast(path, "/blobs/"); ok {
		body, found := f.blobs[digest.Digest(ref)]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.onBlob != nil {
			f.onBlob()
		}
		f.write(w, r, "application/octet-stream", ref, body)
		return
	}

	repo, ref, ok := cutLast(path, "/manifests/")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	desc, found := f.content[repo+"/"+ref]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		f.manifestGets++
	}
	f.write(w, r, desc.MediaType, desc.Digest.String(), f.blobs[desc.Digest])
}

// remove deletes a manifest by digest, with every tag on it. A delete by tag is
// refused, as a registry without tag deletion does.
func (f *fakeRegistry) remove(w http.ResponseWriter, path string) {
	if f.deleteStatus != 0 {
		w.WriteHeader(f.deleteStatus)
		return
	}
	repo, ref, ok := cutLast(path, "/manifests/")
	if !ok || !strings.HasPrefix(ref, digestPrefix) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	desc, found := f.content[repo+"/"+ref]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var kept []string
	for _, tag := range f.tags[repo] {
		if f.content[repo+"/"+tag].Digest == desc.Digest {
			delete(f.content, repo+"/"+tag)
		} else {
			kept = append(kept, tag)
		}
	}
	f.tags[repo] = kept
	delete(f.content, repo+"/"+ref)
	w.Header().Set("Docker-Content-Digest", desc.Digest.String())
	w.WriteHeader(http.StatusAccepted)
}

// accept handles the write half of the distribution API: a blob upload session
// and a manifest PUT. Enough for a real push to complete against it, so a push
// test exercises the protocol rather than a mock of it.
func (f *fakeRegistry) accept(w http.ResponseWriter, r *http.Request, path string) {
	if repo, _, ok := cutLast(path, "/blobs/uploads/"); ok {
		// POST opens a session; the PUT that follows carries the bytes.
		if r.Method == http.MethodPost && r.URL.Query().Get("digest") == "" {
			w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/session")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dgst := digest.FromBytes(body)
		if want := r.URL.Query().Get("digest"); want != "" && want != dgst.String() {
			http.Error(w, "digest mismatch: uploaded "+dgst.String(), http.StatusBadRequest)
			return
		}
		f.blobs[dgst] = body
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.WriteHeader(http.StatusCreated)
		return
	}

	if repo, ref, ok := cutLast(path, "/manifests/"); ok {
		if ref == f.refusePut {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		desc := ocispec.Descriptor{
			MediaType: r.Header.Get("Content-Type"),
			Digest:    digest.FromBytes(body),
			Size:      int64(len(body)),
		}
		f.blobs[desc.Digest] = body
		f.content[repo+"/"+ref] = desc
		f.content[repo+"/"+desc.Digest.String()] = desc
		if !strings.HasPrefix(ref, digestPrefix) && !slices.Contains(f.tags[repo], ref) {
			f.tags[repo] = append(f.tags[repo], ref)
		}
		w.Header().Set("Docker-Content-Digest", desc.Digest.String())
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeRegistry) write(w http.ResponseWriter, r *http.Request, mediaType, dgst string, body []byte) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", dgst)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(body) //nolint:errcheck
}

// blobStore lets a test publish payloads through the real chunked push, so a
// pull test is a round trip rather than an assertion about a hand-built layout.
type fakeBlobs struct{ f *fakeRegistry }

func (b fakeBlobs) Exists(_ context.Context, target ocispec.Descriptor) (bool, error) {
	_, ok := b.f.blobs[target.Digest]
	return ok, nil
}

func (b fakeBlobs) Push(_ context.Context, expected ocispec.Descriptor, content io.Reader) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	b.f.blobs[expected.Digest] = data
	return nil
}

// cutLast splits around the final occurrence of sep, so a repository path with
// slashes in it stays intact.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// publish stores content under its digest and every given reference.
func (f *fakeRegistry) publish(t *testing.T, repo, mediaType string, payload any, refs ...string) ocispec.Descriptor {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(body),
		Size:      int64(len(body)),
	}
	f.blobs[desc.Digest] = body
	for _, ref := range append(refs, desc.Digest.String()) {
		f.content[repo+"/"+ref] = desc
	}
	return desc
}

// publishArtifact stores one manifest under every tag and lists the tags, the
// shape a push produces.
func publishArtifact(t *testing.T, f *fakeRegistry, repo string, ann map[string]string, tags ...string) ocispec.Descriptor {
	t.Helper()
	desc := f.publish(t, repo, ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactTypeOverlay,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{{MediaType: MediaTypeOverlayBlob, Size: 10}},
		Annotations:  ann,
	}, tags...)
	for _, tag := range tags {
		if !slices.Contains(f.tags[repo], tag) {
			f.tags[repo] = append(f.tags[repo], tag)
		}
	}
	return desc
}

func TestResolveArtifactReturnsTheManifest(t *testing.T) {
	f := newFakeRegistry(t)
	want := Annotations(fullManifest(), "zstd")
	published := publishArtifact(t, f, "cnt", want, "star--2.7.11b__amd64")

	desc, ann, err := ResolveArtifact(context.Background(), f.base(), "cnt", "star--2.7.11b__amd64")
	if err != nil {
		t.Fatalf("ResolveArtifact: %v", err)
	}
	if desc.Digest != published.Digest || desc.ArtifactType != ArtifactTypeOverlay {
		t.Errorf("resolved %s (%q), want %s (%q)", desc.Digest, desc.ArtifactType, published.Digest, ArtifactTypeOverlay)
	}
	if ann[AnnIdentitySHA] != want[AnnIdentitySHA] || ann[AnnCompression] != "zstd" {
		t.Errorf("annotations = %v, want the manifest's own", ann)
	}
	// The resolved descriptor must be usable as an address on its own.
	if _, _, err := ResolveArtifact(context.Background(), f.base(), "cnt", desc.Digest.String()); err != nil {
		t.Errorf("resolving the returned digest: %v", err)
	}
}

// A repository holds one manifest per architecture, so an index in it is not
// something CondaTainer published.
func TestResolveArtifactRefusesAnIndex(t *testing.T) {
	f := newFakeRegistry(t)
	f.publish(t, "cnt", ocispec.MediaTypeImageIndex, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
	}, "star--2.7.11b__amd64")

	_, _, err := ResolveArtifact(context.Background(), f.base(), "cnt", "star--2.7.11b__amd64")
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("err = %v, want ErrInvalidArtifact", err)
	}
}

// publishBuild stores a manifest under the tags a catalog push writes for m.
func publishBuild(t *testing.T, f *fakeRegistry, m meta.Manifest) Tags {
	t.Helper()
	tags, err := TagsFor(m, TagRequest{Name: true, Equiv: true})
	if err != nil {
		t.Fatal(err)
	}
	publishArtifact(t, f, "cnt", Annotations(m, ""), tags.List()...)
	return tags
}

func TestLookup(t *testing.T) {
	f := newFakeRegistry(t)
	m := fullManifest()
	m.Name = "star/2.7.11b"
	tags := publishBuild(t, f, m)
	identity, equiv := m.Keys.Identity.SHA256, m.Keys.Equiv.SHA256

	tests := []struct {
		why  string
		addr Address
		want string
	}{
		{"a name reads the name tag", Address{Name: m.Name, Arch: ArchAMD64}, tags.Name},
		{"a complete identity reads the build tag", Address{Name: m.Name, Identity: identity, Arch: ArchAMD64}, tags.Build},
		{"a short identity prefix is matched against the build tags", Address{Name: m.Name, Identity: "aaa", Arch: ArchAMD64}, tags.Build},
		{"an equivalence key reads the equivalence tag", Address{Name: m.Name, Equiv: equiv, Arch: ArchAMD64}, tags.Equiv},
		{"the scheme is half the identity", Address{Name: m.Name, Identity: identity, Scheme: "script-identity-v1", Arch: ArchAMD64}, tags.Build},
	}
	for _, tt := range tests {
		found, err := Lookup(context.Background(), f.base(), "cnt", tt.addr)
		if err != nil {
			t.Errorf("%s: %v", tt.why, err)
			continue
		}
		if found.Tag != tt.want || found.Arch != ArchAMD64 {
			t.Errorf("%s: found %s (%s), want %s", tt.why, found.Tag, found.Arch, tt.want)
		}
	}
}

func TestLookupRefusals(t *testing.T) {
	f := newFakeRegistry(t)
	m := fullManifest()
	m.Name = "star/2.7.11b"
	publishBuild(t, f, m)
	other := m
	other.Keys.Identity.SHA256 = "aaab" + strings.Repeat("a", 60)
	publishBuild(t, f, other)

	_, err := Lookup(context.Background(), f.base(), "cnt", Address{Name: m.Name, Identity: "aaa", Arch: ArchAMD64})
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("a prefix of two builds: err = %v, want ErrMismatch", err)
	}
	_, err = Lookup(context.Background(), f.base(), "cnt", Address{Name: m.Name, Identity: "ccc", Arch: ArchAMD64})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("a prefix of no build: err = %v, want ErrNotFound", err)
	}
	_, err = Lookup(context.Background(), f.base(), "cnt",
		Address{Name: m.Name, Identity: m.Keys.Identity.SHA256, Scheme: "conda-explicit-v1", Arch: ArchAMD64})
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("one digest under another scheme: err = %v, want ErrMismatch", err)
	}
	_, err = Lookup(context.Background(), f.base(), "cnt", Address{Name: m.Name, Identity: "aaa", Equiv: "bbb", Arch: ArchAMD64})
	if err == nil {
		t.Error("an address with an identity and an equivalence key was accepted")
	}
}

// A tag carries a key prefix, so the full key in the annotations decides.
func TestLookupComparesTheFullKey(t *testing.T) {
	f := newFakeRegistry(t)
	m := fullManifest()
	m.Name = "star/2.7.11b"
	collider := m
	collider.Keys.Identity.SHA256 = strings.Repeat("a", 12) + strings.Repeat("c", 52)
	tag, err := BuildTag(m.Name, m.Keys.Identity, ArchAMD64)
	if err != nil {
		t.Fatal(err)
	}
	publishArtifact(t, f, "cnt", Annotations(collider, ""), tag)

	_, err = Lookup(context.Background(), f.base(), "cnt", Address{Name: m.Name, Identity: m.Keys.Identity.SHA256, Arch: ArchAMD64})
	if !errors.Is(err, ErrMismatch) {
		t.Fatalf("err = %v, want ErrMismatch", err)
	}
}

// With no architecture given, the host's tag is tried before noarch, and a
// miss on both leaves PublishedArches to say what exists.
func TestLookupDefaultsToTheHostThenNoarch(t *testing.T) {
	if host := meta.NativeArch(); host != ArchAMD64 && host != ArchARM64 {
		t.Skip("not a distribution architecture")
	}
	f := newFakeRegistry(t)
	portable := fullManifest()
	portable.Name = "ubuntu24/build-essential"
	portable.Platform.Arch = meta.ArchNone
	tags := publishBuild(t, f, portable)

	found, err := Lookup(context.Background(), f.base(), "cnt", Address{Name: portable.Name})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if found.Tag != tags.Name || found.Arch != ArchNoarch {
		t.Errorf("found %s (%s), want %s (noarch)", found.Tag, found.Arch, tags.Name)
	}

	foreign := ArchARM64
	if meta.NativeArch() == ArchARM64 {
		foreign = ArchAMD64
	}
	elsewhere := fullManifest()
	elsewhere.Name = "cellranger/9.0.1"
	elsewhere.Platform.Arch = foreign
	publishBuild(t, f, elsewhere)

	_, err = Lookup(context.Background(), f.base(), "cnt", Address{Name: elsewhere.Name})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	addr := Address{Name: elsewhere.Name}
	if got := PublishedArches(context.Background(), f.base(), "cnt", addr); !slices.Equal(got, []string{foreign}) {
		t.Errorf("PublishedArches = %v, want [%s]", got, foreign)
	}
	explained := ExplainMiss(context.Background(), f.base(), "cnt", addr, err)
	if !errors.Is(explained, ErrUnsupportedPlatform) || !strings.Contains(explained.Error(), "published for "+foreign+" only") {
		t.Errorf("ExplainMiss = %v, want the architectures published", explained)
	}
	if got := ExplainMiss(context.Background(), f.base(), "cnt", Address{Name: "absent/1.0"}, ErrNotFound); !errors.Is(got, ErrNotFound) || errors.Is(got, ErrUnsupportedPlatform) {
		t.Errorf("a name published nowhere: ExplainMiss = %v, want ErrNotFound", got)
	}
}

func TestResolveArtifactNotFound(t *testing.T) {
	f := newFakeRegistry(t)
	_, _, err := ResolveArtifact(context.Background(), f.base(), "cellranger", "9.0.1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// An expired token must never read as "not published": that is the difference
// between an error and a silent forty-minute rebuild.
func TestResolveArtifactUnauthorized(t *testing.T) {
	f := newFakeRegistry(t)
	f.status = http.StatusUnauthorized

	_, _, err := ResolveArtifact(context.Background(), f.base(), "cellranger", "9.0.1")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("an authorization failure was also reported as not published")
	}
}

func TestListTags(t *testing.T) {
	f := newFakeRegistry(t)
	want := []string{"ubuntu24--base__amd64", "ubuntu24--base__a31f902c12ab__amd64", "ubuntu24--base__noarch"}
	f.tags["cnt"] = want

	tags, err := ListTags(context.Background(), f.base(), "cnt")
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	for _, want := range want {
		if !slices.Contains(tags, want) {
			t.Errorf("tags = %v, missing %q", tags, want)
		}
	}
}

// A name nobody has pushed yet is the ordinary state of a name, not a failure.
func TestListTagsOnAnUnpublishedRepository(t *testing.T) {
	f := newFakeRegistry(t)
	tags, err := ListTags(context.Background(), f.base(), "cellranger")
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("tags = %v, want none", tags)
	}
}

func TestListTagsReportsAnUnreadableRegistry(t *testing.T) {
	f := newFakeRegistry(t)
	f.status = http.StatusUnauthorized

	if _, err := ListTags(context.Background(), f.base(), "cellranger"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

// A probe answers from the tag alone, so it reads no manifest.
func TestProbeReadsNoManifest(t *testing.T) {
	f := newFakeRegistry(t)
	published := publishArtifact(t, f, "lab/cnt", map[string]string{AnnTitle: "probe/1"}, "probe--1__noarch")

	found, err := Probe(t.Context(), f.base(), "lab/cnt", Address{Name: "probe/1", Arch: ArchNoarch})
	if err != nil {
		t.Fatal(err)
	}
	if found.Desc.Digest != published.Digest || found.Annotations != nil {
		t.Errorf("probe = %+v, want the digest %s and no annotations", found, published.Digest)
	}
	if f.manifestGets != 0 {
		t.Errorf("probe read %d manifests, want none", f.manifestGets)
	}
	if _, err := Probe(t.Context(), f.base(), "lab/cnt", Address{Name: "absent/1", Arch: ArchNoarch}); !errors.Is(err, ErrNotFound) {
		t.Errorf("probe of an unpublished name = %v, want ErrNotFound", err)
	}
}

// A lookup and the pull that follows read the manifest once between them.
func TestAManifestIsFetchedOncePerDigest(t *testing.T) {
	f := newFakeRegistry(t)
	desc := publishArtifact(t, f, "lab/cnt", map[string]string{AnnTitle: "once/1"}, "once--1__noarch")

	repository, err := newRepository(f.base(), "lab/cnt")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := fetchManifest(t.Context(), repository, desc); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := ResolveArtifact(t.Context(), f.base(), "lab/cnt", "once--1__noarch"); err != nil {
		t.Fatal(err)
	}
	if f.manifestGets != 1 {
		t.Errorf("manifest reads = %d, want 1 for one digest", f.manifestGets)
	}
}
