package slot

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/image/squashfs"
	"github.com/condatainer/condatainer/internal/utils"
)

// Resolved is a binding and the overlay file it resolved to.
type Resolved struct {
	Binding
	Path string
}

// Check reads what the bound overlay provides and returns its exact name.
//   - env= is checked against the variables the overlay exports, from its runtime document. Nothing is mounted.
//   - bin= is checked against the PATH folders of the overlay: <prefix>/bin and the PATH entries its #ENV: adds. A command is found when the name is a file or a symlink there.
//   - Only a .sqf is listed for bin=. A slot that needs a command does not bind any other kind of overlay.
//   - A missing variable or command is an error that names the slot and the overlay.
func Check(r Resolved) (string, error) {
	rt, err := meta.ReadRuntime(r.Path)
	if err != nil {
		return "", fmt.Errorf("slot %q: %s: %w", r.Name, r.Path, err)
	}
	exports := map[string]bool{}
	for _, e := range rt.Env {
		exports[e.Key] = true
	}
	for _, v := range r.Env {
		if !exports[v] {
			return rt.Name, fmt.Errorf("slot %q: %s does not export %s", r.Name, rt.Name, v)
		}
	}
	if len(r.Bin) == 0 {
		return rt.Name, nil
	}
	if !utils.IsSqf(r.Path) {
		return rt.Name, fmt.Errorf("slot %q: bin= can only be checked in a .sqf overlay, and %s is not one", r.Name, r.Path)
	}
	dirs := pathDirs(rt)
	for _, cmd := range r.Bin {
		if !slices.ContainsFunc(dirs, func(dir string) bool {
			return squashfs.PathExists(r.Path, strings.TrimPrefix(dir, "/")+"/"+cmd)
		}) {
			return rt.Name, fmt.Errorf("slot %q: %s puts no %s on PATH", r.Name, rt.Name, cmd)
		}
	}
	return rt.Name, nil
}

// pathDirs are the folders the overlay puts on PATH: <prefix>/bin and the absolute entries of a PATH variable it exports.
func pathDirs(rt meta.Runtime) []string {
	var dirs []string
	if rt.Prefix != "" {
		dirs = append(dirs, rt.Prefix+"/bin")
	}
	for _, e := range rt.Env {
		if e.Key != "PATH" {
			continue
		}
		for _, d := range strings.Split(e.Resolved(rt.Prefix), ":") {
			if strings.HasPrefix(d, "/") && !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	return dirs
}

// Collisions refuses two slots whose overlays export one variable that both list in env= with different values.
//   - One overlay would otherwise overwrite the other's variable. The error names the variable and both overlays.
func Collisions(rs []Resolved) error {
	type owner struct{ slot, overlay, value string }
	byVar := map[string][]owner{}
	for _, r := range rs {
		if len(r.Env) == 0 {
			continue
		}
		rt, err := meta.ReadRuntime(r.Path)
		if err != nil {
			return fmt.Errorf("slot %q: %s: %w", r.Name, r.Path, err)
		}
		for _, e := range rt.Env {
			if slices.Contains(r.Env, e.Key) {
				byVar[e.Key] = append(byVar[e.Key], owner{r.Name, rt.Name, e.Resolved(rt.Prefix)})
			}
		}
	}
	vars := make([]string, 0, len(byVar))
	for v := range byVar {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	var problems []error
	for _, v := range vars {
		owners := byVar[v]
		for _, o := range owners[1:] {
			if o.value != owners[0].value {
				problems = append(problems, fmt.Errorf("slots %q and %q both export %s with different values: %s and %s",
					owners[0].slot, o.slot, v, owners[0].overlay, o.overlay))
				break
			}
		}
	}
	return errors.Join(problems...)
}

// Provides reports whether the overlay at path has what the slot needs. Nothing is reported about why not.
func Provides(d Decl, path string) bool {
	_, err := Check(Resolved{Binding: Binding{Decl: d}, Path: path})
	return err == nil
}

// Options lists the overlays that can fill the slot, sorted.
//   - A strict slot with choices lists exactly its choices.
//   - Otherwise the choices come first, then every installed overlay that has what the slot needs.
//   - installed maps an overlay name to its files, as the scan of installed overlays returns it. A slot with no env= and no bin= lists nothing but its choices, since any overlay would fit.
func Options(d Decl, installed map[string][]string) []string {
	if d.Strict && len(d.Choices) > 0 {
		return append([]string(nil), d.Choices...)
	}
	out := append([]string(nil), d.Choices...)
	if len(d.Env) == 0 && len(d.Bin) == 0 {
		return out
	}
	names := make([]string, 0, len(installed))
	for name := range installed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if Covers(out, name) || len(installed[name]) == 0 {
			continue
		}
		if Provides(d, installed[name][0]) {
			out = append(out, name)
		}
	}
	return out
}
