package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakePackages stands in for GitHub's packages API for one package.
type fakePackages struct {
	mu sync.Mutex
	// ownerType is what the owner lookup answers: User or Organization.
	ownerType string
	// versions are served in pages of the requested size.
	versions []ghcrVersion
	// deleteStatus and deleteBody answer every version delete.
	deleteStatus int
	deleteBody   string
	// deleted records the version ids asked for.
	deleted []int64
	// seen records the paths served, for checking the escaping and the owner prefix.
	seen []string
}

func newFakePackages(t *testing.T) (*fakePackages, *ghcrBackend) {
	t.Helper()
	t.Setenv(EnvGitHubToken, "")
	useLayers(t, map[string]map[string]string{"user": {"ghcr.io": "octo"}})
	f := &fakePackages{ownerType: "Organization", deleteStatus: http.StatusNoContent}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, &ghcrBackend{
		api: server.URL, http: server.Client(), owner: "octo",
		pkg: url.PathEscape("cnt/sub"), scope: "ghcr.io/octo/cnt/sub",
	}
}

func (f *fakePackages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.EscapedPath())
	if r.Header.Get("Authorization") != "Bearer secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := r.URL.EscapedPath()
	switch {
	case path == "/users/octo":
		json.NewEncoder(w).Encode(map[string]string{"type": f.ownerType}) //nolint:errcheck
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/versions"):
		if f.versions == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		lo := min((page-1)*perPage, len(f.versions))
		hi := min(lo+perPage, len(f.versions))
		json.NewEncoder(w).Encode(f.versions[lo:hi]) //nolint:errcheck
	case r.Method == http.MethodDelete && strings.Contains(path, "/versions/"):
		id, _ := strconv.ParseInt(path[strings.LastIndex(path, "/")+1:], 10, 64)
		f.deleted = append(f.deleted, id)
		w.WriteHeader(f.deleteStatus)
		fmt.Fprint(w, f.deleteBody)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func version(id int64, digest string, tags ...string) ghcrVersion {
	v := ghcrVersion{ID: id, Name: "sha256:" + digest}
	v.Metadata.Container.Tags = tags
	return v
}

func TestGHCRBackendListsEveryPageOfVersions(t *testing.T) {
	f, b := newFakePackages(t)
	for i := range 101 {
		f.versions = append(f.versions, version(int64(i+1), fmt.Sprintf("%064x", i+1), "tag"+strconv.Itoa(i)))
	}
	held, err := b.Manifests(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 101 {
		t.Fatalf("manifests = %d, want 101 across two pages", len(held))
	}
	// An organization's packages are under /orgs, and the package path keeps its slash escaped.
	want := "GET /orgs/octo/packages/container/cnt%2Fsub/versions"
	if !slices.ContainsFunc(f.seen, func(s string) bool { return s == want }) {
		t.Errorf("paths = %v, want one to be %q", f.seen, want)
	}
}

func TestGHCRBackendUsesTheUserPrefixForAUser(t *testing.T) {
	f, b := newFakePackages(t)
	f.ownerType = "User"
	f.versions = []ghcrVersion{version(1, strings.Repeat("a", 64), "x")}
	if _, err := b.Manifests(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.seen, "GET /users/octo/packages/container/cnt%2Fsub/versions") {
		t.Errorf("paths = %v", f.seen)
	}
}

func TestGHCRBackendWithNoPackageHasNoManifests(t *testing.T) {
	_, b := newFakePackages(t)
	held, err := b.Manifests(t.Context())
	if err != nil || len(held) != 0 {
		t.Errorf("manifests = %v, err = %v; want none", held, err)
	}
}

func TestGHCRBackendRemovesAVersionAndNamesWhyItCannot(t *testing.T) {
	f, b := newFakePackages(t)
	f.versions = []ghcrVersion{version(7, strings.Repeat("a", 64), "x")}
	held, err := b.Manifests(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(t.Context(), held[0]); err != nil || !slices.Equal(f.deleted, []int64{7}) {
		t.Fatalf("remove = %v, deleted = %v", err, f.deleted)
	}

	// GHCR words this refusal differently for a public package.
	for _, body := range []string{
		`{"message":"You cannot delete the last tagged version of a package. You must delete the package instead."}`,
		`{"message":"Publicly visible package versions with more than 5000 downloads cannot be deleted."}`,
	} {
		f.deleteStatus, f.deleteBody = http.StatusBadRequest, body
		if err := b.Remove(t.Context(), held[0]); !errors.Is(err, ErrLastVersion) {
			t.Errorf("the only tagged version (%.30s…) = %v, want ErrLastVersion", body, err)
		}
	}
	f.deleteStatus, f.deleteBody = http.StatusForbidden, ""
	if err := b.Remove(t.Context(), held[0]); !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "delete:packages") {
		t.Errorf("a refused delete = %v, want ErrUnauthorized naming delete:packages", err)
	}
	f.deleteStatus = http.StatusNotFound
	if err := b.Remove(t.Context(), held[0]); err != nil {
		t.Errorf("a version already gone = %v, want success", err)
	}
}

func TestGHCRBackendNeedsACredential(t *testing.T) {
	_, b := newFakePackages(t)
	useLayers(t, nil)
	if _, err := b.Manifests(t.Context()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("no credential = %v, want ErrUnauthorized", err)
	}
}

func TestBackendForPicksGHCRByHost(t *testing.T) {
	if b, err := BackendFor("ghcr.io", "octo/cnt"); err != nil {
		t.Fatal(err)
	} else if _, ok := b.(*ghcrBackend); !ok {
		t.Errorf("ghcr.io backend = %T, want the GHCR backend", b)
	}
	if b, err := BackendFor("registry-1.docker.io", "octo/cnt"); err != nil {
		t.Fatal(err)
	} else if _, ok := b.(standardBackend); !ok {
		t.Errorf("docker backend = %T, want the standard backend", b)
	}
}

// A refused delete is the last-version rule only when no other version carries a tag.
func TestGHCRBackendReportsOtherRefusalsAsThemselves(t *testing.T) {
	f, b := newFakePackages(t)
	f.versions = []ghcrVersion{
		version(7, strings.Repeat("a", 64), "x"),
		version(8, strings.Repeat("b", 64), "y"),
	}
	held, err := b.Manifests(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.deleteStatus, f.deleteBody = http.StatusBadRequest, `{"message":"something else"}`
	err = b.Remove(t.Context(), held[0])
	if err == nil || errors.Is(err, ErrLastVersion) || !strings.Contains(err.Error(), "something else") {
		t.Errorf("another version is tagged: error = %v, want GitHub's own message", err)
	}
}
