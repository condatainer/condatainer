package build

import (
	"context"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/utils"
)

// depNode is one module in a dependency plan.
type depNode struct {
	Dep    utils.Dep
	Entry  *catalog.Entry // nil when no source provides it
	Source *catalog.Source
	Vars   map[string]string // set when the name came from a template

	// Installed is the version Have reported, when one satisfies the dep.
	Installed string
}

// Name is the module path this node resolves to. The constraint is not part of
// it: ">=1.16" selected the version, it does not name the module.
func (n depNode) Name() string {
	if n.Entry != nil && !n.Entry.IsTemplate {
		return n.Entry.Name
	}
	if n.Dep.Version == "" {
		return n.Dep.Name
	}
	return n.Dep.Name + "/" + n.Dep.Version
}

// depPlan is a build order and the subset of it still to act on.
type depPlan struct {
	Order   []depNode // topological, dependencies first
	Missing []depNode // the nodes to build, pull or hand to a fallback
}

// resolveDeps walks the dependency graph of roots and returns it in build order.
//   - It runs over Entry, so a whole tree is computed from the index without fetching a recipe.
//   - A node no source provides carries a nil Entry rather than failing the walk — that is the normal path for a conda package.
func resolveDeps(ctx context.Context, cat catalog.Catalog, roots []string, have catalog.Have) (*depPlan, error) {
	r := &resolver{cat: cat, have: have, seen: map[string]bool{}, onStack: map[string]bool{}}
	for _, root := range roots {
		dep, err := utils.ParseDep(root)
		if err != nil {
			return nil, err
		}
		if err := r.visit(ctx, dep, nil, ""); err != nil {
			return nil, err
		}
	}

	plan := &depPlan{Order: r.order}
	for _, n := range r.order {
		if n.Installed == "" {
			plan.Missing = append(plan.Missing, n)
		}
	}
	return plan, nil
}

type resolver struct {
	cat     catalog.Catalog
	have    catalog.Have
	seen    map[string]bool
	onStack map[string]bool
	order   []depNode
	stack   []string
}

// visit resolves one dep and everything below it, appending in dependency-first
// order. vars carries the parent's expansion, since a template dep is
// substituted before it has a name at all. parent is the name that depended on
// this one, empty for a root.
func (r *resolver) visit(ctx context.Context, dep utils.Dep, vars map[string]string, parent string) error {
	name := catalog.ReplaceVars(dep.String(), vars)
	dep, err := utils.ParseDep(name)
	if err != nil {
		return err
	}
	key := dep.Name + "/" + dep.Version

	if r.onStack[key] {
		return fmt.Errorf("dependency cycle: %s", strings.Join(append(r.stack, key), " -> "))
	}
	if r.seen[key] {
		return nil // diamond: already placed, and placed before its dependents
	}

	node, err := r.resolveOne(ctx, dep)
	if err != nil {
		return err
	}
	// Refused from the index, before any recipe is fetched: a build that cannot
	// run here is not worth starting the ones before it.
	if node.Entry != nil && node.Installed == "" {
		if err := node.Entry.BuildableOn(planArch(ctx)); err != nil {
			if parent != "" {
				err = fmt.Errorf("%w (needed by %s)", err, parent)
			}
			return err
		}
	}

	r.onStack[key], r.stack = true, append(r.stack, key)
	if node.Entry != nil && node.Installed == "" {
		for _, raw := range node.Entry.Deps {
			child, err := utils.ParseDep(raw)
			if err != nil {
				return err
			}
			if err := r.visit(ctx, child, node.Vars, key); err != nil {
				return err
			}
		}
	}
	r.onStack[key], r.stack = false, r.stack[:len(r.stack)-1]

	r.seen[key] = true
	r.order = append(r.order, node)
	return nil
}

// resolveOne picks the version a dep gets and finds what provides it.
//   - The preferred version wins when it resolves.
//   - Otherwise the newest installed in range, so a bare #DEP: does not rebuild the moment upstream moves; then the newest available; then nothing provides it, and the caller's fallback picks its own.
func (r *resolver) resolveOne(ctx context.Context, dep utils.Dep) (depNode, error) {
	node := depNode{Dep: dep}

	if v := r.have.Newest(dep); v != "" {
		node.Installed = v
		if dep.Version == "" {
			node.Dep.Version = v
		}
	}

	m, found, err := r.cat.Lookup(ctx, dep.Name+"/"+node.Dep.Version)
	if err != nil {
		return node, err
	}
	if !found && node.Dep.Version == "" {
		if v := r.newestAvailable(ctx, dep); v != "" {
			node.Dep.Version = v
			m, found, err = r.cat.Lookup(ctx, dep.Name+"/"+v)
			if err != nil {
				return node, err
			}
		}
	}
	if found {
		node.Entry, node.Source, node.Vars = m.Entry, m.Source, m.Vars
	}
	return node, nil
}

// newestAvailable returns the newest version the catalog offers in range.
func (r *resolver) newestAvailable(ctx context.Context, dep utils.Dep) string {
	versions, err := r.cat.Versions(ctx, dep.Name)
	if err != nil {
		return ""
	}
	for _, v := range versions {
		if dep.Satisfies(v) {
			return v
		}
	}
	return ""
}
