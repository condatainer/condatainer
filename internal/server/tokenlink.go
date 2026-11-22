package server

import (
	"net/url"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
)

// TokenLink returns link with the token added, for a command to print where a user clicks it.
//   - Only a link that opens this server gets it: host localhost or {label}.localhost.
//   - Any other link, and every link when no token is required or the token file cannot be read, comes back as it is.
//   - The dashboard never calls it, so the token stays out of the page and the API.
func TokenLink(link string) string {
	if !TokenRequired() {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	if host := u.Hostname(); host != "localhost" && !strings.HasSuffix(host, ".localhost") {
		return link
	}
	tok, _, err := readTokenFile(config.GetServerTokenPath())
	if err != nil {
		return link
	}
	return addQueryParam(link, tokenParam, tok)
}
