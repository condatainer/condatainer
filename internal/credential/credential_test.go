package credential

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func layerFile(t *testing.T, layer string) File {
	t.Helper()
	return File{Layer: layer, Path: filepath.Join(t.TempDir(), FileName)}
}

func TestKeysWalkFromTheScopeToItsHost(t *testing.T) {
	got := Keys("https://raw.githubusercontent.com/lab/recipes/main/")
	want := []string{
		"raw.githubusercontent.com/lab/recipes/main",
		"raw.githubusercontent.com/lab/recipes",
		"raw.githubusercontent.com/lab",
		"raw.githubusercontent.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %q, want %q", got, want)
	}
}

func TestLookupPrefersTheMostSpecificKeyThenTheNearestLayer(t *testing.T) {
	user, group := layerFile(t, "user"), layerFile(t, "extra-root")
	for _, s := range []struct {
		f         File
		key, name string
	}{
		{user, "ghcr.io", "personal"},
		{group, "ghcr.io", "group-host"},
		{group, "ghcr.io/lab", "group-owner"},
	} {
		if err := Save(s.f, Registry, s.key, Credential{Username: s.name, Secret: "s"}); err != nil {
			t.Fatal(err)
		}
	}
	files := []File{user, group}
	if found, _ := Lookup(files, Registry, "ghcr.io/lab/cnt"); found.Username != "group-owner" || found.Layer != "extra-root" {
		t.Errorf("lab = %+v", found)
	}
	if found, _ := Lookup(files, Registry, "ghcr.io/other"); found.Username != "personal" || found.Key != "ghcr.io" {
		t.Errorf("other = %+v", found)
	}
	if _, ok := Lookup(files, Registry, "registry.example.test/lab"); ok {
		t.Error("a credential was found for another host")
	}
}

func TestListShowsKeysAndLayersWithoutSecrets(t *testing.T) {
	user, group := layerFile(t, "user"), layerFile(t, "extra-root")
	if err := Save(user, Registry, "ghcr.io/lab/rnaseq", Credential{Username: "bob", Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(group, Registry, "ghcr.io", Credential{Username: "group", Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	got, err := List([]File{user, group}, Registry)
	if err != nil {
		t.Fatal(err)
	}
	want := []Stored{
		{Key: "ghcr.io", Layer: "extra-root", Username: "group", Path: group.Path, Perm: "0600"},
		{Key: "ghcr.io/lab/rnaseq", Layer: "user", Username: "bob", Path: user.Path, Perm: "0600"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A shared layer's directory is not ours to create.
func TestSaveRefusesAMissingSharedDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", FileName)
	cred := Credential{Username: "u", Secret: "s"}

	if err := Save(File{Layer: "extra-root", Path: missing}, Registry, "ghcr.io", cred); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("extra-root: %v", err)
	}
	if err := Save(File{Layer: "user", Path: missing}, Registry, "ghcr.io", cred); err != nil {
		t.Fatalf("user: %v", err)
	}
	if info, err := os.Stat(filepath.Dir(missing)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the user's directory was not created private: %v", err)
	}
}

// A credential file follows its directory: private in a personal one, group
// read-write in a group-writable one.
func TestSaveFollowsTheDirectory(t *testing.T) {
	for _, tt := range []struct {
		dir, want os.FileMode
	}{
		{0o700, 0o600},
		{0o770, 0o660},
	} {
		dir := filepath.Join(t.TempDir(), "layer")
		if err := os.Mkdir(dir, tt.dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, tt.dir); err != nil { // Mkdir is subject to the umask
			t.Fatal(err)
		}
		f := File{Layer: "extra-root", Path: filepath.Join(dir, FileName)}
		if err := Save(f, Registry, "ghcr.io", Credential{Username: "u", Secret: "s"}); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(f.Path); err != nil || info.Mode().Perm() != tt.want {
			t.Errorf("directory %o: mode = %v, %v; want %o", tt.dir, info.Mode().Perm(), err, tt.want)
		}
	}
}

// A token is found, listed and removed only as its own kind.
func TestKindsAreSeparate(t *testing.T) {
	f := layerFile(t, "user")
	key := "raw.githubusercontent.com/lab/recipes/main"
	if err := Save(f, Source, key, Credential{Username: TokenUser, Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup([]File{f}, Registry, key); ok {
		t.Error("a source token was found as a registry login")
	}
	if found, ok := Lookup([]File{f}, Source, key); !ok || found.Secret != "s" {
		t.Errorf("source lookup = %+v, %v", found, ok)
	}
	if stored, _ := List([]File{f}, Registry); len(stored) != 0 {
		t.Errorf("registry list shows %+v", stored)
	}
	if err := Remove(f, Registry, key); err == nil {
		t.Error("a registry removal took a source token")
	}
}

// A source's registry token lives in its entry: a new recipe token keeps it, and
// removing the source takes it along.
func TestSourceRegistryTokenLivesWithItsSource(t *testing.T) {
	f := layerFile(t, "extra-root")
	base, key := "https://raw.githubusercontent.com/lab/recipes/main", "raw.githubusercontent.com/lab/recipes/main"
	if err := Save(f, Source, key, Credential{Username: TokenUser, Secret: "recipe"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSourceRegistry(f, key, "ghcr.io/lab", Credential{Username: TokenUser, Secret: "registry"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(f, Source, key, Credential{Username: TokenUser, Secret: "recipe2"}); err != nil {
		t.Fatal(err)
	}
	found, ok := LookupSourceRegistry([]File{f}, base)
	if !ok || found.Key != "ghcr.io/lab" || found.Secret != "registry" || found.Layer != "extra-root" {
		t.Fatalf("registry token = %+v, %v", found, ok)
	}
	if logins, _ := List([]File{f}, Registry); len(logins) != 0 {
		t.Errorf("a source token is listed as a login: %+v", logins)
	}
	if err := Remove(f, Source, key); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupSourceRegistry([]File{f}, base); ok {
		t.Error("the registry token outlived its source")
	}
}
