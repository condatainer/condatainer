package libexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// spec is one package named on the command line, with the version asked for. An empty version means the newest.
type spec struct {
	name, version string
}

// versionPart matches a full or partial version: 1, 1.5 or 1.5.4.
var versionPart = regexp.MustCompile(`^\d+(\.\d+)*$`)

// tagPattern matches a release tag: the version, a leading v if any, and an optional build number (2.8.1-1).
var tagPattern = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)(?:-(\d+))?$`)

// parseSpecs reads the names given to Update, each name or name@version.
// With no names every package is wanted, at the newest version. Order follows packages.
func parseSpecs(args []string) ([]spec, error) {
	if len(args) == 0 {
		var all []spec
		for _, p := range packages {
			all = append(all, spec{name: p})
		}
		return all, nil
	}
	asked := map[string]spec{}
	for _, arg := range args {
		name, version, _ := strings.Cut(arg, "@")
		if !slices.Contains(packages, name) {
			return nil, fmt.Errorf("unknown toolchain package %q (available: %s)", name, strings.Join(packages, ", "))
		}
		if _, dup := asked[name]; dup {
			return nil, fmt.Errorf("%s is named twice", name)
		}
		if strings.Contains(arg, "@") && !versionPart.MatchString(version) {
			return nil, fmt.Errorf("%q is not a version; write it as %s@1.5 or %s@1.5.4", arg, name, name)
		}
		asked[name] = spec{name: name, version: version}
	}
	var out []spec
	for _, p := range packages {
		if s, ok := asked[p]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// pickTag returns the newest of tags whose version is want, or begins with want and a dot (1.5 matches 1.5.4, not 1.50.0).
//   - Tags that are not versions are ignored.
//   - Of two tags with one version, the higher build number wins.
func pickTag(tags []string, want string) (string, error) {
	var best string
	var bestVersion string
	bestBuild := -1
	for _, tag := range tags {
		m := tagPattern.FindStringSubmatch(tag)
		if m == nil || (m[1] != want && !strings.HasPrefix(m[1], want+".")) {
			continue
		}
		build, _ := strconv.Atoi(m[2])
		if best == "" || utils.CompareVersions(m[1], bestVersion) > 0 || (m[1] == bestVersion && build > bestBuild) {
			best, bestVersion, bestBuild = tag, m[1], build
		}
	}
	if best == "" {
		return "", fmt.Errorf("no release matches version %s", want)
	}
	return best, nil
}

// listReleases reads the published releases of the repository at api, newest first, up to a few hundred.
// Drafts and pre-releases are left out.
func listReleases(ctx context.Context, api string) ([]release, error) {
	var all []release
	for page := 1; page <= 5; page++ {
		var batch []struct {
			release
			Draft      bool `json:"draft"`
			Prerelease bool `json:"prerelease"`
		}
		if err := getJSON(ctx, fmt.Sprintf("%s?per_page=100&page=%d", api, page), &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			if !r.Draft && !r.Prerelease {
				all = append(all, r.release)
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// getJSON reads the JSON at url into out.
func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("failed to look up releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to look up releases: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("unreadable release description: %w", err)
	}
	return nil
}
