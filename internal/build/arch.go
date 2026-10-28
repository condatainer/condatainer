package build

import (
	"context"
	"errors"
	"fmt"

	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// ErrForeignArch reports a plan made for another architecture that was asked to run.
var ErrForeignArch = errors.New("plan is for another architecture")

// ErrPlanOnly reports a plan that skipped the recipes' questions and was asked to run.
var ErrPlanOnly = errors.New("plan was made without asking the recipes' questions")

type planArchKey struct{}

// ForArch returns a context that plans for arch: recipes are checked against it,
// and prebuilts are looked up for it. A graph planned this way can be read but
// not run unless arch is this machine's. Empty is this machine's.
func ForArch(ctx context.Context, arch string) context.Context {
	return context.WithValue(ctx, planArchKey{}, arch)
}

// planArch is the architecture ctx plans for.
func planArch(ctx context.Context) string {
	if arch, _ := ctx.Value(planArchKey{}).(string); arch != "" {
		return arch
	}
	return meta.NativeArch()
}

type planOnlyKey struct{}

// PlanOnly returns a context for a plan that is read and never run: a recipe's
// questions are not asked, and its answers stay empty. They change what a build
// downloads, never the equivalence a plan reports.
func PlanOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, planOnlyKey{}, true)
}

// planOnly reports whether ctx is for a plan that is never run.
func planOnly(ctx context.Context) bool {
	only, _ := ctx.Value(planOnlyKey{}).(bool)
	return only
}

// refuseUnrunnable refuses to act on a plan made only to be read: one made
// without the recipes' answers, or for another architecture.
func refuseUnrunnable(ctx context.Context) error {
	if planOnly(ctx) {
		return ErrPlanOnly
	}
	if arch := planArch(ctx); arch != meta.NativeArch() {
		return fmt.Errorf("%w: %s, and this machine is %s", ErrForeignArch, arch, meta.NativeArch())
	}
	return nil
}
