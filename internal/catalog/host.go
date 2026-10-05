package catalog

import (
	"net/http"
	"net/url"
	"strings"
)

// hostAPI is how one kind of host serves a file of a collection and takes a token.
type hostAPI struct {
	name string
	// fileURL is the URL of path, relative to the collection root, under base.
	fileURL func(base, path string) string
	// authorize puts token on req.
	authorize func(req *http.Request, token string)
}

// flatBearer reads base/path and sends the token as a Bearer token (RFC 6750).
// Any host not in hostAPIs is read this way.
var flatBearer = hostAPI{name: "http", fileURL: flatURL, authorize: bearer}

// hostAPIs are the hosts known by name, keyed by URL host.
var hostAPIs = map[string]hostAPI{
	// Raw file content, at <owner>/<repo>/<ref>/<path>, with a fine-grained or
	// classic token as Bearer:
	// https://docs.github.com/en/rest/authentication/authenticating-to-the-rest-api
	"raw.githubusercontent.com": {name: "github", fileURL: flatURL, authorize: bearer},
}

// hostFor is the hostAPI for base's host, or flatBearer.
func hostFor(base string) hostAPI {
	u, err := url.Parse(base)
	if err != nil {
		return flatBearer
	}
	if api, ok := hostAPIs[strings.ToLower(u.Hostname())]; ok {
		return api
	}
	return flatBearer
}

func flatURL(base, path string) string { return strings.TrimSuffix(base, "/") + "/" + path }

func bearer(req *http.Request, token string) { req.Header.Set("Authorization", "Bearer "+token) }
