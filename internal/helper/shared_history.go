package helper

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/helperhistory"
)

// RecordUsed records that name was run at location (project-root-relative,
// slash-separated) with its required and added overlays, in root's shared
// history.
func RecordUsed(root, name, location string, required, overlays []string) error {
	return helperhistory.RecordUsed(root, name, location, required, overlays)
}

// ReuseOption is a recorded combination as a launch can apply it.
type ReuseOption struct {
	Overlays []string          `json:"overlays"`
	Params   map[string]string `json:"params,omitempty"`
	LastUsed time.Time         `json:"last_used"`
}

// ListReusable returns the combinations recorded for name at location that have something to apply, newest first.
//   - Something to apply is added overlays, or params recovered from the recorded required names.
//   - Params are recovered by matching template, the helper's #REQUIRED_OVERLAYS:.
//   - #IMG_PACKAGES: params are never recorded or recovered.
func ListReusable(root, name, location, template string) ([]ReuseOption, error) {
	used, err := helperhistory.ListUsed(root, name, location)
	if err != nil {
		return nil, err
	}
	var out []ReuseOption
	for _, u := range used {
		params := paramsFromRequired(template, u.Required)
		if len(u.Overlays) == 0 && len(params) == 0 {
			continue
		}
		overlays := u.Overlays
		if overlays == nil {
			overlays = []string{}
		}
		out = append(out, ReuseOption{Overlays: overlays, Params: params, LastUsed: u.LastUsed})
	}
	return out, nil
}

var paramToken = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// paramsFromRequired recovers the #PARAM: values a recorded required list was filled from.
//   - Each template name holding a {KEY} is matched against the list.
//   - "r/{POSIT_R}" against "r/4.4.3" gives POSIT_R=4.4.3.
func paramsFromRequired(template string, required []string) map[string]string {
	params := map[string]string{}
	for _, name := range strings.Fields(template) {
		locs := paramToken.FindAllStringSubmatchIndex(name, -1)
		if len(locs) == 0 {
			continue
		}
		var pattern strings.Builder
		var keys []string
		last := 0
		for _, l := range locs {
			pattern.WriteString(regexp.QuoteMeta(name[last:l[0]]))
			pattern.WriteString("(.+?)")
			keys = append(keys, name[l[2]:l[3]])
			last = l[1]
		}
		pattern.WriteString(regexp.QuoteMeta(name[last:]))
		re, err := regexp.Compile(fmt.Sprintf("^%s$", pattern.String()))
		if err != nil {
			continue
		}
		for _, r := range required {
			m := re.FindStringSubmatch(r)
			if m == nil {
				continue
			}
			for i, k := range keys {
				if _, ok := params[k]; !ok {
					params[k] = m[i+1]
				}
			}
			break
		}
	}
	return params
}
