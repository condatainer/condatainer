package registry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/logging"
)

// Environment credential, for a host with no stored credential.
const (
	// EnvGitHubToken is GitHub's own name and applies to ghcr.io alone. It is not
	// ours to rename, and it is already set in every GitHub Actions job.
	EnvGitHubToken = "GITHUB_TOKEN"

	// ghcrHost is the one registry EnvGitHubToken speaks for.
	ghcrHost = "ghcr.io"

	// EnvLayer is the Layer of a credential that came from EnvGitHubToken.
	EnvLayer = "env"
)

// credentialFiles lists the credential files to search, nearest first. Tests
// replace it.
var credentialFiles = config.CredentialFiles

// credentialFile is one layer's credential file. Tests replace it.
var credentialFile = config.CredentialFile

// configuredSources lists the recipe sources in search order. Tests replace it.
var configuredSources = func() []catalog.Spec {
	var out []catalog.Spec
	for _, s := range config.ResolvedSources() {
		out = append(out, s.Spec)
	}
	return out
}

type sourceKey struct{}
type pushKey struct{}

// WithSource marks ctx as reading for the recipe source at base, so that
// source's registry token is tried first.
func WithSource(ctx context.Context, base string) context.Context {
	return context.WithValue(ctx, sourceKey{}, base)
}

func withPush(ctx context.Context) context.Context {
	return context.WithValue(ctx, pushKey{}, true)
}

// chain lists the credentials to try for host while working on the repository
// at scope ("host/owner/repo", no tag), in order:
//   - A push: the login, GITHUB_TOKEN, then source registry tokens.
//   - A read for one source (WithSource): that source's registry token, the login, GITHUB_TOKEN.
//   - Any other read: the login, source registry tokens in search order, GITHUB_TOKEN.
//
// A login is the most specific key, nearest layer. A source token applies when
// its registry is scope or a parent of it. A scope on another host contributes
// only that host's key.
func chain(ctx context.Context, scope, host string) []credential.Found {
	key := host
	if scopeHost, _, _ := strings.Cut(scope, "/"); strings.EqualFold(scopeHost, host) {
		key = scope
	}
	files := credentialFiles()
	var login, env []credential.Found
	if found, ok := credential.Lookup(files, credential.Registry, key); ok {
		login = append(login, found)
	}
	if cred, ok := envCredential(host); ok {
		env = append(env, credential.Found{Credential: cred, Key: EnvGitHubToken, Layer: EnvLayer})
	}
	var sources, preferred []credential.Found
	want, _ := ctx.Value(sourceKey{}).(string)
	for _, spec := range configuredSources() {
		found, ok := credential.LookupSourceRegistry(files, spec.Base)
		if !ok || !covers(found.Key, key) {
			continue
		}
		found.Source = spec.Name
		if want != "" && spec.Base == want {
			preferred = append(preferred, found)
		}
		sources = append(sources, found)
	}
	switch {
	case ctx.Value(pushKey{}) != nil:
		return concat(login, env, sources)
	case want != "":
		return concat(preferred, login, env)
	}
	return concat(login, sources, env)
}

func concat(parts ...[]credential.Found) []credential.Found {
	var out []credential.Found
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// covers reports whether a registry root applies to key: key is the root or
// lies under it.
func covers(root, key string) bool {
	root, key = strings.ToLower(root), strings.ToLower(key)
	return key == root || strings.HasPrefix(key, root+"/")
}

// CredentialFor is the first credential ctx's reads of the registry named by ref
// would send, ref being a bare host or a host/prefix coordinate.
func CredentialFor(ctx context.Context, ref string) (credential.Found, bool) {
	scope := TrimBaseScheme(ref)
	host, _, _ := strings.Cut(scope, "/")
	if host == "" {
		return credential.Found{}, false
	}
	if c := chain(ctx, scope, host); len(c) > 0 {
		return c[0], true
	}
	return credential.Found{}, false
}

// HasCredential reports whether any credential is available for the registry
// named by ref.
//
//   - A refusal with a credential is ErrUnauthorized once every credential and a read without one are refused.
//   - A refusal with no credential is a closed door, the same as "not published".
func HasCredential(ctx context.Context, ref string) bool {
	_, ok := CredentialFor(ctx, ref)
	return ok
}

// envCredential builds a credential from GITHUB_TOKEN for ghcr.io, reporting
// false for any other host so a job's token is never sent elsewhere.
func envCredential(host string) (credential.Credential, bool) {
	if !strings.EqualFold(host, ghcrHost) {
		return credential.Credential{}, false
	}
	token := strings.TrimSpace(os.Getenv(EnvGitHubToken))
	if token == "" {
		return credential.Credential{}, false
	}
	return credential.Credential{Username: credential.TokenUser, Secret: token}, true
}

// client sends requests with the first credential of its chain. A read that
// credential gets refused (401) moves to the next, and finally to none; later
// reads start from where that ended. Each refused credential is warned about
// once, when a later one answers. A push never moves on.
type client struct {
	scope string
	inner *http.Client

	once    sync.Once
	cands   []credential.Found
	clients []*auth.Client // one per candidate, then an anonymous one
	push    bool

	mu      sync.Mutex
	at      int
	refused []credential.Found
	warned  int
}

func (c *client) init(ctx context.Context) {
	c.once.Do(func() {
		host, _, _ := strings.Cut(c.scope, "/")
		c.push = ctx.Value(pushKey{}) != nil
		c.cands = chain(ctx, c.scope, host)
		for _, found := range c.cands {
			c.clients = append(c.clients, c.authClient(host, found))
		}
		c.clients = append(c.clients, c.authClient(host, credential.Found{}))
	})
}

// authClient sends found to host only; an empty found is anonymous.
func (c *client) authClient(host string, found credential.Found) *auth.Client {
	a := &auth.Client{Client: c.inner, Cache: auth.NewCache()}
	if found.Secret != "" {
		cred := auth.Credential{Username: found.Username, Password: found.Secret}
		a.Credential = func(_ context.Context, h string) (auth.Credential, error) {
			if !strings.EqualFold(h, host) {
				return auth.EmptyCredential, nil
			}
			return cred, nil
		}
	}
	a.SetUserAgent(userAgent())
	return a
}

func (c *client) Do(req *http.Request) (*http.Response, error) {
	c.init(req.Context())
	read := req.Method == http.MethodGet || req.Method == http.MethodHead
	if !read || c.push {
		return c.clients[0].Do(req)
	}
	for {
		i := c.current()
		resp, err := c.clients[i].Do(req)
		if !refused(resp, err) {
			c.warnRefused(req.Context())
			return resp, err
		}
		if i == len(c.clients)-1 {
			if failed := c.refusedSoFar(); len(failed) > 0 {
				discard(resp)
				return nil, refusedError(failed)
			}
			return resp, err
		}
		discard(resp)
		c.advance(i)
		req = req.Clone(req.Context())
	}
}

func (c *client) current() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// advance moves past candidate i, unless another request already has.
func (c *client) advance(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at == i {
		c.refused = append(c.refused, c.cands[i])
		c.at = i + 1
	}
}

func (c *client) refusedSoFar() []credential.Found {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]credential.Found(nil), c.refused...)
}

func (c *client) warnRefused(ctx context.Context) {
	c.mu.Lock()
	pending := c.refused[c.warned:]
	c.warned = len(c.refused)
	c.mu.Unlock()
	for _, found := range pending {
		logging.FromContext(ctx).Warn(refusedMessage(found) + "; continuing without it")
	}
}

// firstCredential is the credential this client sends first, if it has one.
func (c *client) firstCredential() (credential.Found, bool) {
	if len(c.cands) == 0 {
		return credential.Found{}, false
	}
	return c.cands[0], true
}

// refused reports a 401, as a response or as the token exchange's error.
func refused(resp *http.Response, err error) bool {
	if err != nil {
		var errResp *errcode.ErrorResponse
		return errors.As(err, &errResp) && errResp.StatusCode == http.StatusUnauthorized
	}
	return resp != nil && resp.StatusCode == http.StatusUnauthorized
}

func discard(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxCapturedBody))
		_ = resp.Body.Close()
	}
}

// refusedError is ErrUnauthorized naming each credential that was refused.
func refusedError(failed []credential.Found) error {
	msgs := make([]string, len(failed))
	for i, found := range failed {
		msgs[i] = refusedMessage(found)
	}
	return fmt.Errorf("%w: %s", ErrUnauthorized, strings.Join(msgs, "; "))
}

// refusedMessage names a refused credential and how to replace or drop it.
func refusedMessage(found credential.Found) string {
	switch {
	case found.Layer == EnvLayer:
		return EnvGitHubToken + " was refused; unset it or replace it"
	case found.Source != "":
		return fmt.Sprintf("the registry token of source %s (%s layer) was refused; add the source again with a new token, or remove it with `condatainer config source remove %s -l %s`",
			found.Source, found.Layer, found.Source, found.Layer)
	}
	return fmt.Sprintf("the login for %s (%s layer) was refused; replace it with `condatainer registry login %s -l %s`, or remove it with `condatainer registry logout %s -l %s`",
		found.Key, found.Layer, found.Key, found.Layer, found.Key, found.Layer)
}

// newAuthClient is the client every registry operation on the repository at scope
// uses: retrying transport, token cache and credential chain, wrapped by [inspectTransport].
//
//   - The retrying transport serves small requests: token exchange, HEAD, manifest reads.
//   - Rate-limited blob writes belong to [retryPolicy], which waits minutes and can replay a body.
//   - No client-level timeout: it would kill a long upload.
func newAuthClient(scope string) *client {
	inner := *retry.DefaultClient
	inner.Transport = &inspectTransport{next: cmp.Or(inner.Transport, http.DefaultTransport)}
	return &client{scope: scope, inner: &inner}
}
