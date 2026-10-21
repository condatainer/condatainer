package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// workers is how many tags are resolved at once.
const workers = 8

// githubAPI is where GHCR packages are managed.
const githubAPI = "https://api.github.com"

// Held is one manifest of a repository with every tag on it.
type Held struct {
	Desc ocispec.Descriptor
	Tags []string
	// id is the package version a GHCR manifest lives in.
	id int64
}

// Backend lists and removes the manifests of one repository.
type Backend interface {
	// Manifests lists every manifest with its tags.
	Manifests(ctx context.Context) ([]Held, error)
	// Remove removes a manifest and every tag on it.
	Remove(ctx context.Context, h Held) error
}

// BackendFor picks how the repository at "<base>/<repo>" lists and removes manifests.
//   - ghcr.io rejects the standard delete calls, so its packages are managed through GitHub's API.
//   - Any other registry uses the standard calls.
func BackendFor(base, repo string) (Backend, error) {
	repository, err := newRepository(base, repo)
	if err != nil {
		return nil, err
	}
	host := registryHost(base)
	if !strings.EqualFold(host, ghcrHost) {
		return standardBackend{repository: repository, host: host}, nil
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("%s/%s names no package owner", host, repo)
	}
	return &ghcrBackend{
		api: githubAPI, http: retry.DefaultClient, owner: owner,
		pkg: url.PathEscape(name), scope: TrimBaseScheme(base) + "/" + repo,
	}, nil
}

// standardBackend uses the registry's own tag list and manifest delete.
type standardBackend struct {
	repository *remote.Repository
	host       string
}

// Manifests resolves every tag to its manifest, a few at a time, and groups the tags by digest.
// A tag removed since the listing is skipped.
func (b standardBackend) Manifests(ctx context.Context) ([]Held, error) {
	var tags []string
	err := b.repository.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	})
	if err != nil {
		if err = classify(err); errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list tags of %s: %w", b.repository.Reference, err)
	}
	descs := make([]ocispec.Descriptor, len(tags))
	found := make([]bool, len(tags))
	var group errgroup.Group
	group.SetLimit(workers)
	for i, tag := range tags {
		group.Go(func() error {
			desc, err := b.repository.Resolve(ctx, tag)
			switch {
			case err == nil:
				descs[i], found[i] = desc, true
			case errors.Is(classify(err), ErrNotFound):
			default:
				return classify(err)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	byDigest := map[digest.Digest]*Held{}
	var order []digest.Digest
	for i, tag := range tags {
		if !found[i] {
			continue
		}
		h, ok := byDigest[descs[i].Digest]
		if !ok {
			h = &Held{Desc: descs[i]}
			byDigest[descs[i].Digest] = h
			order = append(order, descs[i].Digest)
		}
		h.Tags = append(h.Tags, tag)
	}
	return collect(byDigest, order), nil
}

// Remove deletes the manifest by digest, which drops every tag on it.
func (b standardBackend) Remove(ctx context.Context, h Held) error {
	err := b.repository.Delete(ctx, h.Desc)
	if err == nil || errors.Is(err, errdef.ErrNotFound) {
		return nil
	}
	var resp *errcode.ErrorResponse
	if errors.As(err, &resp) && resp.StatusCode == http.StatusMethodNotAllowed {
		return fmt.Errorf("%w: %s refuses to delete; remove it in the registry's own console", ErrNoRemoval, b.host)
	}
	err = classify(err)
	if errors.Is(err, ErrUnauthorized) {
		return fmt.Errorf("%w; removing needs a login that may delete from %s%s", err, b.host, deleteHint(b.host))
	}
	return fmt.Errorf("cannot remove %s: %w", h.Desc.Digest, err)
}

func deleteHint(host string) string {
	if strings.Contains(host, "docker.io") {
		return ", such as an access token with Read, Write and Delete permission"
	}
	return ""
}

// collect orders held manifests by their first tag, with their tags sorted.
func collect(byDigest map[digest.Digest]*Held, order []digest.Digest) []Held {
	out := make([]Held, 0, len(order))
	for _, d := range order {
		h := byDigest[d]
		slices.Sort(h.Tags)
		out = append(out, *h)
	}
	slices.SortFunc(out, func(a, b Held) int { return strings.Compare(firstTag(a), firstTag(b)) })
	return out
}

func firstTag(h Held) string {
	if len(h.Tags) == 0 {
		return h.Desc.Digest.String()
	}
	return h.Tags[0]
}

// ghcrBackend manages a package through GitHub's REST API.
type ghcrBackend struct {
	api   string
	http  *http.Client
	owner string
	// pkg is the package name with "/" escaped.
	pkg string
	// scope is the repository coordinate the credential is looked up for.
	scope string
	// prefix is "/users/<owner>" or "/orgs/<owner>", found on first use.
	prefix string
	// listed is what the last listing returned, to tell a version that is the package's last tagged one.
	listed []Held
}

type ghcrVersion struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Metadata struct {
		Container struct {
			Tags []string `json:"tags"`
		} `json:"container"`
	} `json:"metadata"`
}

// call sends one request with the repository's credential. A body is returned for any status.
func (b *ghcrBackend) call(ctx context.Context, method, path string) (int, []byte, error) {
	ctx = withPush(ctx)
	found := chain(ctx, b.scope, ghcrHost)
	if len(found) == 0 {
		return 0, nil, fmt.Errorf("%w: no credential for %s; log in with `condatainer registry login %s`", ErrUnauthorized, ghcrHost, ghcrHost)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.api+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+found[0].Secret)
	req.Header.Set("User-Agent", userAgent())
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, nil, classify(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, body, err
}

// packages is the API prefix for the package owner's packages.
func (b *ghcrBackend) packages(ctx context.Context) (string, error) {
	if b.prefix == "" {
		code, body, err := b.call(ctx, http.MethodGet, "/users/"+url.PathEscape(b.owner))
		if err != nil {
			return "", err
		}
		if code != http.StatusOK {
			return "", b.refused(code, body, "read the owner "+b.owner)
		}
		var who struct{ Type string }
		if err := json.Unmarshal(body, &who); err != nil {
			return "", fmt.Errorf("cannot read the owner %s: %w", b.owner, err)
		}
		b.prefix = "/users/" + url.PathEscape(b.owner)
		if who.Type == "Organization" {
			b.prefix = "/orgs/" + url.PathEscape(b.owner)
		}
	}
	return b.prefix + "/packages/container/" + b.pkg, nil
}

// refused turns an unexpected status into an error.
func (b *ghcrBackend) refused(code int, body []byte, doing string) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: GitHub refused to %s; log in with a token that has the delete:packages scope", ErrUnauthorized, doing)
	case http.StatusNotFound:
		return fmt.Errorf("%w: GitHub has no package for %s", ErrNotFound, doing)
	}
	var msg struct{ Message string }
	_ = json.Unmarshal(body, &msg)
	return fmt.Errorf("GitHub answered %d trying to %s: %s", code, doing, msg.Message)
}

// Manifests lists the package's versions, a page at a time. A package that does not exist yet has none.
func (b *ghcrBackend) Manifests(ctx context.Context) ([]Held, error) {
	base, err := b.packages(ctx)
	if err != nil {
		return nil, err
	}
	const perPage = 100
	var out []Held
	for page := 1; ; page++ {
		code, body, err := b.call(ctx, http.MethodGet, fmt.Sprintf("%s/versions?per_page=%d&page=%d", base, perPage, page))
		if err != nil {
			return nil, err
		}
		if code == http.StatusNotFound && page == 1 {
			return nil, nil
		}
		if code != http.StatusOK {
			return nil, b.refused(code, body, "list the versions of "+b.owner+"/"+b.pkg)
		}
		var versions []ghcrVersion
		if err := json.Unmarshal(body, &versions); err != nil {
			return nil, fmt.Errorf("cannot read the versions of %s/%s: %w", b.owner, b.pkg, err)
		}
		for _, v := range versions {
			tags := slices.Clone(v.Metadata.Container.Tags)
			slices.Sort(tags)
			out = append(out, Held{Desc: ocispec.Descriptor{Digest: digest.Digest(v.Name)}, Tags: tags, id: v.ID})
		}
		if len(versions) < perPage {
			b.listed = out
			return out, nil
		}
	}
}

// lastTagged reports whether h is the only version of the listed package that carries a tag.
// GHCR refuses to remove it, and words the refusal differently for a public package.
func (b *ghcrBackend) lastTagged(h Held) bool {
	if len(h.Tags) == 0 {
		return false
	}
	for _, other := range b.listed {
		if other.id != h.id && len(other.Tags) > 0 {
			return false
		}
	}
	return true
}

// Remove deletes the package version, which drops every tag on it.
func (b *ghcrBackend) Remove(ctx context.Context, h Held) error {
	base, err := b.packages(ctx)
	if err != nil {
		return err
	}
	code, body, err := b.call(ctx, http.MethodDelete, fmt.Sprintf("%s/versions/%d", base, h.id))
	if err != nil {
		return err
	}
	switch {
	case code == http.StatusNoContent, code == http.StatusNotFound:
		return nil
	case code == http.StatusBadRequest && b.lastTagged(h):
		return fmt.Errorf("%w: %s/%s; to remove it, delete the package in GitHub's package settings", ErrLastVersion, b.owner, b.pkg)
	}
	return b.refused(code, body, "remove "+h.Desc.Digest.String())
}
