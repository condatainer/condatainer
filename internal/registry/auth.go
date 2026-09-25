package registry

import (
	"cmp"
	"context"
	"net/http"
	"os"
	"strings"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// Environment credential, for a host with no usable Docker credential store.
const (
	// EnvGitHubToken is GitHub's own name and applies to ghcr.io alone. It is not
	// ours to rename, and it is already set in every GitHub Actions job.
	EnvGitHubToken = "GITHUB_TOKEN"

	// ghcrHost is the one registry EnvGitHubToken speaks for.
	ghcrHost = "ghcr.io"
	// defaultTokenUser is what a registry expects beside a token when the token
	// itself carries the identity. GHCR accepts any username with a valid PAT.
	defaultTokenUser = "x-access-token"
)

// credentialFunc resolves credentials for the repository at scope
// ("host/owner/repo", no tag). In order:
//
//   - GITHUB_TOKEN, for ghcr.io only.
//   - Stored credentials: most specific key first, nearest layer first.
//   - Anonymous. An unreadable store falls through to it.
func credentialFunc(scope string) auth.CredentialFunc {
	var files []authFile
	loaded := false
	return func(ctx context.Context, host string) (auth.Credential, error) {
		if cred, ok := envCredential(host); ok {
			return cred, nil
		}
		if !loaded {
			files, loaded = readLayers(), true
		}
		for _, key := range storeKeys(scope, host) {
			for _, file := range files {
				if entry, ok := file.Auths[key]; ok {
					if cred := entry.credential(); cred != auth.EmptyCredential {
						return cred, nil
					}
				}
			}
		}
		return auth.EmptyCredential, nil
	}
}

// readLayers reads every layer's credential file, nearest first. A layer that is
// unavailable or unreadable holds nothing.
func readLayers() []authFile {
	var files []authFile
	for _, layer := range credentialLayerNames {
		path, err := credentialFilePath(layer)
		if err != nil {
			continue
		}
		if file, err := readAuthFile(path); err == nil {
			files = append(files, file)
		}
	}
	return files
}

// storeKeys lists the keys that may hold a credential for host, most specific
// first: scope itself, each parent path, then the bare host. A scope on another
// host contributes only that host's key.
func storeKeys(scope, host string) []string {
	scopeHost, _, _ := strings.Cut(scope, "/")
	if !strings.EqualFold(scopeHost, host) {
		return []string{host}
	}
	var keys []string
	for path := scope; strings.Contains(path, "/"); path = path[:strings.LastIndex(path, "/")] {
		keys = append(keys, path)
	}
	return append(keys, scopeHost)
}

// HasCredential reports whether any credential is available for the registry
// named by ref, a bare host or a host/prefix coordinate.
//
//   - A credential that fails to open an artifact is ErrUnauthorized.
//   - A refusal with no credential is a closed door, the same as "not published".
func HasCredential(ctx context.Context, ref string) bool {
	host, _, _ := strings.Cut(TrimBaseScheme(ref), "/")
	if host == "" {
		return false
	}
	cred, err := credentialFunc(TrimBaseScheme(ref))(ctx, host)
	if err != nil {
		return false
	}
	return cred != auth.EmptyCredential
}

// envCredential builds a credential from GITHUB_TOKEN for ghcr.io, reporting
// false for any other host so a job's token is never sent elsewhere.
func envCredential(host string) (auth.Credential, bool) {
	if !strings.EqualFold(host, ghcrHost) {
		return auth.Credential{}, false
	}
	token := strings.TrimSpace(os.Getenv(EnvGitHubToken))
	if token == "" {
		return auth.Credential{}, false
	}
	return auth.Credential{Username: defaultTokenUser, Password: token}, true
}

// newAuthClient is the client every registry operation on the repository at scope
// uses: retrying transport, token cache and credential chain, wrapped by [inspectTransport].
//
//   - The retrying transport serves small requests: token exchange, HEAD, manifest reads.
//   - Rate-limited blob writes belong to [retryPolicy], which waits minutes and can replay a body.
//   - No client-level timeout: it would kill a long upload.
func newAuthClient(scope string) *auth.Client {
	inner := *retry.DefaultClient
	inner.Transport = &inspectTransport{next: cmp.Or(inner.Transport, http.DefaultTransport)}

	client := &auth.Client{
		Client:     &inner,
		Cache:      auth.NewCache(),
		Credential: credentialFunc(scope),
	}
	client.SetUserAgent(userAgent())
	return client
}
