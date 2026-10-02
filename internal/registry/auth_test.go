package registry

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/logging"
)

func TestEnvCredential(t *testing.T) {
	tests := []struct {
		name  string
		token string
		host  string
		want  bool
	}{
		{"no token is not a credential", "", "ghcr.io", false},
		{"GITHUB_TOKEN speaks for ghcr.io", "gh", "ghcr.io", true},
		{"the host matches case-insensitively", "gh", "GHCR.IO", true},
		// A CI job's GitHub token must never reach an unrelated registry.
		{"GITHUB_TOKEN goes nowhere else", "gh", "registry.example.test", false},
		{"whitespace is not a token", "   ", "ghcr.io", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvGitHubToken, tt.token)
			cred, ok := envCredential(tt.host)
			if ok != tt.want {
				t.Fatalf("ok = %v, want %v", ok, tt.want)
			}
			if ok && (cred.Username != credential.TokenUser || cred.Secret != tt.token) {
				t.Errorf("credential = %+v", cred)
			}
		})
	}
}

// useLayers gives each named layer a credential file in a temp directory holding
// the given users (secret "secret") by key, and leaves the other layers
// unavailable.
func useLayers(t *testing.T, layers map[string]map[string]string) map[string]credential.File {
	t.Helper()
	files := map[string]credential.File{}
	var ordered []credential.File
	for _, layer := range []string{"user", "extra-root", "app-root"} {
		users, ok := layers[layer]
		if !ok {
			continue
		}
		f := credential.File{Layer: layer, Path: filepath.Join(t.TempDir(), credential.FileName)}
		for key, user := range users {
			if err := credential.Save(f, credential.Registry, key, credential.Credential{Username: user, Secret: "secret"}); err != nil {
				t.Fatal(err)
			}
		}
		files[layer] = f
		ordered = append(ordered, f)
	}
	prevFiles, prevFile, prevSources := credentialFiles, credentialFile, configuredSources
	credentialFiles = func() []credential.File { return ordered }
	configuredSources = func() []catalog.Spec { return nil }
	credentialFile = func(layer string) (credential.File, error) {
		if f, ok := files[layer]; ok {
			return f, nil
		}
		return credential.File{}, errors.New("layer not available")
	}
	t.Cleanup(func() { credentialFiles, credentialFile, configuredSources = prevFiles, prevFile, prevSources })
	return files
}

// useSourceToken configures a source named name at base, with a registry token
// for registry stored in f under user.
func useSourceToken(t *testing.T, f credential.File, name, base, registry, user string) {
	t.Helper()
	cred := credential.Credential{Username: user, Secret: "secret"}
	if err := credential.SaveSourceRegistry(f, credential.TrimScheme(base), registry, cred); err != nil {
		t.Fatal(err)
	}
	prev := configuredSources()
	configuredSources = func() []catalog.Spec { return append(prev, catalog.Spec{Name: name, Base: base}) }
}

func users(c []credential.Found) []string {
	var out []string
	for _, f := range c {
		out = append(out, f.Username)
	}
	return out
}

// A missing store means anonymous access, which is the normal case for a public
// artifact — never an error.
func TestChainIsEmptyWithNothingStored(t *testing.T) {
	t.Setenv(EnvGitHubToken, "")
	useLayers(t, nil)
	if c := chain(t.Context(), "registry.example.test/lab/repo", "registry.example.test"); len(c) != 0 {
		t.Errorf("chain = %+v, want none", c)
	}
}

// The most specific login key wins over a nearer layer, and among layers holding
// the same key the nearest wins.
func TestChainPrefersTheMostSpecificLoginThenTheNearestLayer(t *testing.T) {
	t.Setenv(EnvGitHubToken, "")
	useLayers(t, map[string]map[string]string{
		"user": {"ghcr.io": "personal-host"},
		"extra-root": {
			"ghcr.io":               "group-host",
			"ghcr.io/my-lab/rnaseq": "group-repo",
			"ghcr.io/my-lab":        "group-owner",
		},
	})
	tests := []struct{ scope, want string }{
		{"ghcr.io/my-lab/rnaseq/cnt", "group-repo"},
		{"ghcr.io/my-lab/other/cnt", "group-owner"},
		{"ghcr.io/someone/else", "personal-host"},
		{"ghcr.io", "personal-host"},
	}
	for _, tt := range tests {
		if got := users(chain(t.Context(), tt.scope, "ghcr.io")); len(got) == 0 || got[0] != tt.want {
			t.Errorf("scope %s: chain %v, want %s first", tt.scope, got, tt.want)
		}
	}
}

// A login is never sent to another host.
func TestChainKeepsHostsApart(t *testing.T) {
	t.Setenv(EnvGitHubToken, "")
	useLayers(t, map[string]map[string]string{"user": {"ghcr.io/my-lab/rnaseq": "repo"}})
	if c := chain(t.Context(), "ghcr.io/my-lab/rnaseq/cnt", "registry.example.test"); len(c) != 0 {
		t.Errorf("another host received %+v", c)
	}
}

// Which credential comes first depends on what the read or write is for.
func TestChainOrder(t *testing.T) {
	t.Setenv(EnvGitHubToken, "env")
	files := useLayers(t, map[string]map[string]string{"user": {"ghcr.io/lab": "login"}})
	useSourceToken(t, files["user"], "lab", "https://recipes.example.test/lab", "ghcr.io/lab", "source")
	scope := "ghcr.io/lab/cnt"
	for _, tt := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"a read", t.Context(), "login,source,x-access-token"},
		{"a read for the source", WithSource(t.Context(), "https://recipes.example.test/lab"), "source,login,x-access-token"},
		{"a push", withPush(t.Context()), "login,x-access-token,source"},
	} {
		if got := strings.Join(users(chain(tt.ctx, scope, "ghcr.io")), ","); got != tt.want {
			t.Errorf("%s: chain %s, want %s", tt.name, got, tt.want)
		}
	}
}

// A refused credential is set aside for the next one, then for none, with a
// warning naming it; when everything is refused, the error names them all.
func TestReadMovesPastARefusedCredential(t *testing.T) {
	t.Setenv(EnvGitHubToken, "")
	f := newFakeRegistry(t)
	f.bearer = true
	f.tags["lab/cnt"] = []string{"1.0"}
	files := useLayers(t, map[string]map[string]string{"extra-root": {f.base(): "stale"}})
	useSourceToken(t, files["extra-root"], "lab", "https://recipes.example.test/lab", f.base(), "good")

	f.acceptUser = "good"
	var lines []string
	ctx := logging.WithLogger(context.Background(), slog.New(recordingHandler{lines: &lines}))
	if tags, err := ListTags(ctx, f.base(), "lab/cnt"); err != nil || len(tags) != 1 {
		t.Fatalf("ListTags = %v, %v", tags, err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "the login for "+f.base()+" (extra-root layer) was refused") {
		t.Errorf("warnings = %q", lines)
	}

	f.acceptUser, f.refuseAnonymous = "nobody", true
	_, err := ListTags(context.Background(), f.base(), "lab/cnt")
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "registry login "+f.base()+" -l extra-root") ||
		!strings.Contains(err.Error(), "source lab") {
		t.Fatalf("err = %v, want ErrUnauthorized naming both credentials", err)
	}
}
