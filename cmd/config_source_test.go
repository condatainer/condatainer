package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
)

// recipeServer serves an empty collection. A private one answers 404 to a read
// without the token "good", the way a git host hides a private repository.
func recipeServer(t *testing.T, private bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "index"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index", "recipes.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := http.FileServer(http.Dir(root))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer good":
		case "":
			if private {
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

// addSource runs `config source add` in the user layer with stdin as the token.
func addSource(t *testing.T, name, url, stdin string) error {
	t.Helper()
	prevLayer, prevOpts := setLayer, sourceOpts
	t.Cleanup(func() { setLayer, sourceOpts = prevLayer, prevOpts })
	setLayer, sourceOpts.tokenStdin = "user", stdin != ""
	configSourceAddCmd.SetIn(strings.NewReader(stdin))
	configSourceAddCmd.SetContext(t.Context())
	return runSourceAdd(configSourceAddCmd, []string{name, url})
}

func userSources(t *testing.T) (names []string, path string) {
	t.Helper()
	path, err := config.GetUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range config.LayerSourceList(path) {
		names = append(names, s.Name)
	}
	return names, path
}

func TestSourceAdd(t *testing.T) {
	public, private := recipeServer(t, false), recipeServer(t, true)
	userFile, err := config.CredentialFile("user")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, path := userSources(t)
		os.Remove(path)
		os.Remove(userFile.Path)
	})

	if err := addSource(t, "pub", public, ""); err != nil {
		t.Fatalf("public: %v", err)
	}
	if _, ok := credential.Lookup([]credential.File{userFile}, credential.Source, public); ok {
		t.Error("a public source stored a token")
	}

	if err := addSource(t, "lab", private, "wrong\n"); err == nil {
		t.Fatal("a wrong token was accepted")
	}
	if names, _ := userSources(t); strings.Join(names, ",") != "pub" {
		t.Fatalf("a refused source was written: %v", names)
	}

	if err := addSource(t, "lab", private, "good\n"); err != nil {
		t.Fatalf("private: %v", err)
	}
	found, ok := credential.Lookup([]credential.File{userFile}, credential.Source, private)
	if !ok || found.Secret != "good" {
		t.Errorf("token = %+v, %v", found, ok)
	}
	names, path := userSources(t)
	if strings.Join(names, ",") != "pub,lab" {
		t.Errorf("sources = %v", names)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "good") {
		t.Error("the token reached config.yaml")
	}
}

// recipeDir is an empty directory collection.
func recipeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runInUserLayer runs a source command in the user layer with the given
// position flags.
func runInUserLayer(t *testing.T, run func(*cobra.Command, []string) error, c *cobra.Command, pos config.Position, args ...string) error {
	t.Helper()
	prevLayer, prevOpts := setLayer, sourceOpts
	t.Cleanup(func() { setLayer, sourceOpts = prevLayer, prevOpts })
	setLayer = "user"
	sourceOpts.first, sourceOpts.last, sourceOpts.before, sourceOpts.after = pos.First, pos.Last, pos.Before, pos.After
	c.SetContext(t.Context())
	return run(c, args)
}

func TestSourceMoveAndRemove(t *testing.T) {
	_, path := userSources(t)
	t.Cleanup(func() { os.Remove(path) })
	for _, name := range []string{"a", "b", "c"} {
		if err := addSource(t, name, recipeDir(t), ""); err != nil {
			t.Fatal(err)
		}
	}
	order := func() string { names, _ := userSources(t); return strings.Join(names, ",") }
	move := func(name string, pos config.Position) error {
		return runInUserLayer(t, runSourceMove, configSourceMoveCmd, pos, name)
	}
	remove := func(name string) error {
		return runInUserLayer(t, runSourceRemove, configSourceRemoveCmd, config.Position{}, name)
	}

	if err := move("c", config.Position{First: true}); err != nil || order() != "c,a,b" {
		t.Fatalf("move --first: %v, order %s", err, order())
	}
	if err := move("c", config.Position{After: "b"}); err != nil || order() != "a,b,c" {
		t.Fatalf("move --after: %v, order %s", err, order())
	}
	if err := move("a", config.Position{}); err == nil {
		t.Error("move without a position was accepted")
	}
	if err := move("cnt", config.Position{Before: "b"}); err != nil || order() != "a,cnt,b,c" {
		t.Fatalf("moving the default cnt: %v, order %s", err, order())
	}
	if err := remove("cnt"); err != nil || order() != "a,b,c" {
		t.Fatalf("remove cnt: %v, order %s", err, order())
	}
	if err := remove("cnt"); err == nil {
		t.Error("removing the default cnt was accepted")
	}
	if err := remove("b"); err != nil || order() != "a,c" {
		t.Fatalf("remove: %v, order %s", err, order())
	}
}

// Removing a source takes its recipe token with it.
func TestSourceRemoveDropsItsToken(t *testing.T) {
	private := recipeServer(t, true)
	userFile, _ := config.CredentialFile("user")
	_, path := userSources(t)
	t.Cleanup(func() { os.Remove(path); os.Remove(userFile.Path) })

	if err := addSource(t, "lab", private, "good\n"); err != nil {
		t.Fatal(err)
	}
	if err := runInUserLayer(t, runSourceRemove, configSourceRemoveCmd, config.Position{}, "lab"); err != nil {
		t.Fatal(err)
	}
	if _, ok := credential.Lookup([]credential.File{userFile}, credential.Source, private); ok {
		t.Error("the token outlived its source")
	}
}

// A pasted token arrives wrapped in the terminal's paste markers; anything else
// unprintable would be refused by the server as a header value.
func TestCleanSecret(t *testing.T) {
	if got, err := cleanSecret("\x1b[200~ghp_abc\x1b[201~\n"); err != nil || got != "ghp_abc" {
		t.Errorf("paste = %q, %v", got, err)
	}
	for _, bad := range []string{"ghp\x1babc", "ghp abc", "ghp\tabc"} {
		if _, err := cleanSecret(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// Both lines of --token-stdin land with the source: the second is checked
// against the source's registry and stored with it, never as a login.
func TestSourceAddStoresTheRegistryTokenWithTheSource(t *testing.T) {
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); ok && user == credential.TokenUser && pass == "reg" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Www-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(reg.Close)
	endpoint := strings.TrimPrefix(reg.URL, "http://") + "/lab"

	root := t.TempDir()
	for rel, body := range map[string]string{
		"index/recipes.json": "{}",
		"source.json":        `{"schema":1,"oci":{"registry":"` + endpoint + `"}}`,
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := http.FileServer(http.Dir(root))
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(src.Close)

	userFile, _ := config.CredentialFile("user")
	_, path := userSources(t)
	t.Cleanup(func() { os.Remove(path); os.Remove(userFile.Path) })

	if err := addSource(t, "lab", src.URL, "good\nreg\n"); err != nil {
		t.Fatal(err)
	}
	found, ok := credential.LookupSourceRegistry([]credential.File{userFile}, src.URL)
	if !ok || found.Key != endpoint || found.Secret != "reg" {
		t.Errorf("registry token = %+v, %v", found, ok)
	}
	if logins, _ := credential.List([]credential.File{userFile}, credential.Registry); len(logins) != 0 {
		t.Errorf("the registry token was saved as a login: %+v", logins)
	}
}
