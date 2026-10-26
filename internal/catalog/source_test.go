package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeSource writes a minimal collection: a descriptor, two recipes, and the
// index an HTTP source would be served from.
func fakeSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("source.json", `{"schema_version":1,"source":"https://example.invalid/r","default_distro":"ubuntu24","oci":{"registry":"oci://ghcr.io/lab/cnt/","audience":"restricted"}}`)
	write("recipes/cellranger/9.0.1", "#DESC:cellranger\n#URL:https://example.invalid\n")
	write("recipes/ubuntu24/base.def", "#DESC:base\n\nBootstrap: docker\n")
	write("recipes/grch38/star-gencode", starRecipe)
	write("recipes/README.md", "not a recipe")

	// The index an HTTP backend reads, built from the same files.
	cat, err := Open(t.Context(), []Spec{{Name: "local", Base: root}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	entries := cat.Entries(t.Context())
	data, err := json.Marshal(map[string]any{"schema_version": IndexSchemaVersion, "recipes": entries})
	if err != nil {
		t.Fatal(err)
	}
	write("index/recipes.json", string(data))
	return root
}

func TestOpenDirSource(t *testing.T) {
	root := fakeSource(t)
	cat, err := Open(t.Context(), []Spec{{Name: "local", Base: root}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}

	if got := cat.DefaultDistro(); got != "ubuntu24" {
		t.Errorf("DefaultDistro = %q, want ubuntu24", got)
	}
	if cat[0].Desc.Source != "https://example.invalid/r" {
		t.Errorf("descriptor not loaded: %+v", cat[0].Desc)
	}
	if got := cat[0].Desc.OCI.Registry; got != "ghcr.io/lab/cnt" {
		t.Errorf("OCI registry = %q", got)
	}
	if got := cat[0].Desc.OCI.Audience; got != "restricted" {
		t.Errorf("OCI audience = %q", got)
	}

	entries := cat.Entries(t.Context())
	want := []string{"cellranger/9.0.1", "grch38/star-gencode", "ubuntu24/base"}
	if got := slices.Sorted(maps.Keys(entries)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v (README.md skipped)", got, want)
	}
	if e := entries["ubuntu24/base"]; e.Type != TypeOS || e.Path != "recipes/ubuntu24/base.def" {
		t.Errorf("base entry = %+v", e)
	}
	if e := entries["grch38/star-gencode"]; !e.IsTemplate || e.Type != TypeData {
		t.Errorf("template entry = %+v", e)
	}
}

func TestParseDescriptorOCI(t *testing.T) {
	for _, tc := range []struct {
		name    string
		json    string
		want    Descriptor
		wantErr string
	}{
		{
			name: "absent OCI defaults safely",
			json: `{"schema_version":1}`,
			want: Descriptor{SchemaVersion: 1, OCI: OCI{Audience: "public"}},
		},
		{
			name: "scheme normalization",
			json: `{"schema_version":1,"oci":{"registry":"oci://ghcr.io/lab/cnt/","audience":"RESTRICTED"}}`,
			want: Descriptor{SchemaVersion: 1, OCI: OCI{Registry: "ghcr.io/lab/cnt", Audience: "restricted"}},
		},
		{name: "bad audience", json: `{"schema_version":1,"oci":{"registry":"ghcr.io/lab/cnt","audience":"private"}}`, wantErr: "audience"},
		{name: "host alone is not a repository", json: `{"schema_version":1,"oci":{"registry":"ghcr.io"}}`, wantErr: "registry/repository coordinate"},
		{name: "unsupported scheme", json: `{"schema_version":1,"oci":{"registry":"https://ghcr.io/lab/cnt"}}`, wantErr: "registry/repository coordinate"},
		{name: "Docker Hub takes user/repo", json: `{"schema_version":1,"oci":{"registry":"docker.io/lab/cnt/deep"}}`, wantErr: "two parts"},
		{name: "Docker Hub namespace alone is no repository", json: `{"schema_version":1,"oci":{"registry":"docker.io/lab"}}`, wantErr: "two parts"},
		{name: "newer schema", json: `{"schema_version":2}`, wantErr: "update CondaTainer"},
		{name: "missing schema", json: `{"default_distro":"ubuntu24"}`, wantErr: "schema_version 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDescriptor([]byte(tc.json))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("descriptor = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestInvalidDescriptorDoesNotDisableRecipes(t *testing.T) {
	root := fakeSource(t)
	if err := os.WriteFile(filepath.Join(root, "source.json"), []byte(`{"schema_version":1,"oci":{"registry":"ghcr.io"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := Open(t.Context(), []Spec{{Name: "local", Base: root}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	if cat[0].DescriptorErr == nil {
		t.Fatal("invalid descriptor was not reported")
	}
	if cat[0].Desc.OCI.Registry != "" {
		t.Errorf("invalid descriptor was retained: %+v", cat[0].Desc)
	}
	if _, found, err := cat.Lookup(t.Context(), "cellranger/9.0.1"); err != nil || !found {
		t.Errorf("recipe lookup = (found %v, err %v)", found, err)
	}
}

// Both backends must produce the same Entry for the same recipe, which is the
// one place the index and the walk can drift apart.
// ReadFresh returns the file as it is now, with the source's token, while a read
// through the cache still returns the copy cached before.
func TestReadFreshBypassesCacheAndSendsToken(t *testing.T) {
	root := fakeSource(t)
	script := filepath.Join(root, "helpers", "svc")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	local, err := Open(t.Context(), []Spec{{Name: "local", Base: root}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	write("v1")
	if got, err := local[0].ReadFresh(t.Context(), "helpers/svc"); err != nil || string(got) != "v1" {
		t.Errorf("directory source: ReadFresh = %q, %v", got, err)
	}

	spec := Spec{Name: "lab", Base: privateServer(t, root, false), Token: NewToken("good", "127.0.0.1/lab", "user")}
	remote, err := Open(t.Context(), []Spec{spec}, Cache{Dir: t.TempDir(), TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := remote.ReadPath(t.Context(), remote[0], "helpers/svc"); err != nil || string(got) != "v1" {
		t.Fatalf("cached read = %q, %v", got, err)
	}
	write("v2")
	if got, _ := remote.ReadPath(t.Context(), remote[0], "helpers/svc"); string(got) != "v1" {
		t.Errorf("a read through the cache = %q, want the cached v1", got)
	}
	if got, err := remote[0].ReadFresh(t.Context(), "helpers/svc"); err != nil || string(got) != "v2" {
		t.Errorf("http source: ReadFresh = %q, %v, want v2", got, err)
	}

	anon := Spec{Name: "anon", Base: spec.Base}
	noToken, err := Open(t.Context(), []Spec{anon}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noToken[0].ReadFresh(t.Context(), "helpers/svc"); err == nil {
		t.Error("ReadFresh read a private source without its token")
	}
}

// A directory source lists its helpers/ files, and an HTTP source leaves that to its index.
func TestHelperFiles(t *testing.T) {
	root := fakeSource(t)
	for _, name := range []string{"svc", "notes.md", ".hidden"} {
		path := filepath.Join(root, "helpers", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	local, err := Open(t.Context(), []Spec{{Name: "local", Base: root}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	files, isLocal, err := local[0].HelperFiles()
	if err != nil || !isLocal || !slices.Equal(files, []string{"helpers/notes.md", "helpers/svc"}) {
		t.Errorf("directory source: %v, local=%v, %v", files, isLocal, err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer srv.Close()
	remote, err := Open(t.Context(), []Spec{{Name: "remote", Base: srv.URL}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	if _, isLocal, _ := remote[0].HelperFiles(); isLocal {
		t.Error("an HTTP source was scanned as a directory")
	}
}

func TestBackendsAgree(t *testing.T) {
	root := fakeSource(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer srv.Close()

	local, err := Open(t.Context(), []Spec{{Name: "local", Base: root}}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := Open(t.Context(), []Spec{{Name: "remote", Base: srv.URL}},
		Cache{Dir: t.TempDir(), TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if remote[0].Desc.DefaultDistro != "ubuntu24" {
		t.Errorf("descriptor not fetched over HTTP: %+v", remote[0].Desc)
	}

	lEntries := local.Entries(t.Context())
	rEntries := remote.Entries(t.Context())
	if len(lEntries) != len(rEntries) {
		t.Fatalf("walk found %d entries, index %d", len(lEntries), len(rEntries))
	}
	for name, l := range lEntries {
		r, ok := rEntries[name]
		if !ok {
			t.Errorf("%s: missing from the index", name)
			continue
		}
		if l.Type != r.Type || l.Path != r.Path || l.Description != r.Description || l.URL != r.URL ||
			l.IsTemplate != r.IsTemplate || l.TargetTemplate != r.TargetTemplate ||
			!slices.Equal(l.Deps, r.Deps) {
			t.Errorf("%s:\n walk  %+v\n index %+v", name, l, r)
		}
		for k, v := range l.PH {
			if !slices.Equal(r.PH[k], v) {
				t.Errorf("%s: ph[%s] walk=%v index=%v", name, k, v, r.PH[k])
			}
		}
	}
}

func TestOpenRejectsBadSpec(t *testing.T) {
	if _, err := Open(t.Context(), nil, Cache{}); err == nil {
		t.Error("Open with no sources should fail")
	}
	if _, err := Open(t.Context(), []Spec{{Name: "x"}}, Cache{}); err == nil {
		t.Error("Open with an empty base should fail")
	}
}

// An unreachable source keeps its place, so first-wins ordering cannot silently
// promote the next source's recipes.
func TestUnreachableSourceKeepsItsPlace(t *testing.T) {
	root := fakeSource(t)
	cat, err := Open(t.Context(), []Spec{
		{Name: "dead", Base: filepath.Join(root, "does-not-exist")},
		{Name: "local", Base: root},
	}, Cache{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) != 2 || cat[0].Name != "dead" {
		t.Fatalf("catalog = %v", cat)
	}
	entries := cat.Entries(t.Context())
	if cat[0].Err == nil {
		t.Error("unreachable source should carry its error")
	}
	if _, ok := entries["cellranger/9.0.1"]; !ok {
		t.Error("the reachable source should still contribute")
	}
}

// A descriptor's source is written into every artifact the collection builds and
// published as org.opencontainers.image.source, so a bad one is caught where it
// enters rather than in each artifact built from it.
func TestParseDescriptorChecksTheSourceURL(t *testing.T) {
	for _, tc := range []struct{ name, json, wantErr string }{
		{"https", `{"schema_version":1,"source":"https://github.com/lab/r"}`, ""},
		{"http", `{"schema_version":1,"source":"http://git.lab.example/r"}`, ""},
		{"absent", `{"schema_version":1}`, ""},
		{"wrong scheme", `{"schema_version":1,"source":"ftp://example.invalid/r"}`, "http(s)"},
		{"not a url", `{"schema_version":1,"source":"github.com/lab/r"}`, "http(s)"},
		{"whitespace", `{"schema_version":1,"source":"https://example.invalid/a b"}`, "whitespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDescriptor([]byte(tc.json))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseDescriptor: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// privateServer serves root like a git host's raw files: a good token reads, a
// bad one is refused with 401, and no token gets 404 unless the source is public.
func privateServer(t *testing.T, root string, public bool) string {
	t.Helper()
	files := http.FileServer(http.Dir(root))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer good":
		case "":
			if !public {
				http.NotFound(w, r)
				return
			}
		default:
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHTTPSourceToken(t *testing.T) {
	root := fakeSource(t)
	tests := []struct {
		name    string
		public  bool
		token   string
		wantErr error
		ok      bool
		refused bool
	}{
		{"a private source reads with its token", false, "good", nil, true, false},
		{"a private source without a token is unreachable", false, "", nil, false, false},
		{"a stale token on a public source is set aside", true, "bad", nil, true, true},
		{"a stale token on a private source names the token", false, "bad", ErrTokenRefused, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := Spec{Name: "lab", Base: privateServer(t, root, tt.public)}
			if tt.token != "" {
				spec.Token = NewToken(tt.token, "127.0.0.1/lab", "extra-root")
			}
			cat, err := Open(t.Context(), []Spec{spec}, Cache{})
			if err != nil {
				t.Fatal(err)
			}
			entries, err := cat[0].Entries(t.Context())
			if (err == nil) != tt.ok || len(entries) > 0 != tt.ok {
				t.Fatalf("entries = %d, err = %v", len(entries), err)
			}
			if tt.wantErr != nil && (!errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), "127.0.0.1/lab (extra-root layer)")) {
				t.Errorf("err = %v, want %v naming the token", err, tt.wantErr)
			}
			if cat[0].TokenRefused != tt.refused {
				t.Errorf("TokenRefused = %v, want %v", cat[0].TokenRefused, tt.refused)
			}
		})
	}
}

func TestTokenNeverPrintsItsSecret(t *testing.T) {
	token := NewToken("ghp_secret", "raw.githubusercontent.com/lab", "user")
	for _, s := range []string{token.String(), fmt.Sprintf("%v %+v", token, Spec{Name: "lab", Token: token})} {
		if strings.Contains(s, "ghp_secret") {
			t.Errorf("printed the secret: %s", s)
		}
	}
}

func TestDecodeIndexRequiresTheVersionAndTheMember(t *testing.T) {
	var entries map[string]*Entry
	if err := DecodeIndex([]byte(`{"schema_version":1,"recipes":{"a/b":{"path":"recipes/a/b"}}}`), "recipes", &entries); err != nil || entries["a/b"] == nil {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
	for _, tc := range []struct{ name, doc, want string }{
		{"bare map", `{"a/b":{"path":"recipes/a/b"}}`, "schema_version 0"},
		{"newer", `{"schema_version":2,"recipes":{}}`, "update CondaTainer"},
		{"no member", `{"schema_version":1,"helpers":{}}`, `no "recipes"`},
	} {
		err := DecodeIndex([]byte(tc.doc), "recipes", &entries)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestParseDescriptorIgnoresAnAddedKey(t *testing.T) {
	desc, err := ParseDescriptor([]byte(`{"schema_version":1,"mirrors":["https://m.example"],"oci":{"registry":"ghcr.io/lab/cnt","note":"x"}}`))
	if err != nil || desc.OCI.Registry != "ghcr.io/lab/cnt" {
		t.Fatalf("descriptor = %+v, err = %v; a key this build does not know must not matter", desc, err)
	}
}
