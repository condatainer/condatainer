package registry

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/artifact/meta"
)

const removeRepo = "lab/cnt"

// seededBuild is one published build of a name for tests.
type seededBuild struct {
	name, arch, id, eq string
	created            time.Time
	// tags are the tags besides the build tag.
	extra []string
}

func hexKey(prefix string) string { return prefix + strings.Repeat("0", 64-len(prefix)) }

// seed publishes a build with its build tag and the tags it asks for, and returns the build tag.
func seed(t *testing.T, f *fakeRegistry, b seededBuild) string {
	t.Helper()
	build, err := BuildTag(b.name, meta.KeyRef{Scheme: "script-identity-v1", SHA256: hexKey(b.id)}, b.arch)
	if err != nil {
		t.Fatal(err)
	}
	ann := map[string]string{
		AnnTitle: b.name, AnnArch: b.arch,
		AnnIdentityScheme: "script-identity-v1", AnnIdentitySHA: hexKey(b.id),
		AnnEquivScheme: "script-equiv-v1", AnnEquivSHA: hexKey(b.eq),
		AnnCreated: b.created.UTC().Format(time.RFC3339),
	}
	publishArtifact(t, f, removeRepo, ann, append([]string{build}, b.extra...)...)
	return build
}

func spellTag(t *testing.T, name, qualifier, arch string) string {
	t.Helper()
	tag, err := spell(name, qualifier, arch)
	if err != nil {
		t.Fatal(err)
	}
	return tag
}

func backendOf(t *testing.T, f *fakeRegistry) Backend {
	t.Helper()
	b, err := BackendFor(f.base(), removeRepo)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseTag(t *testing.T) {
	for _, tc := range []struct {
		tag  string
		want parsedTag
	}{
		{"star--1__amd64", parsedTag{Kind: kindName, Encoded: "star--1", Arch: "amd64"}},
		{"star--1__noarch", parsedTag{Kind: kindName, Encoded: "star--1", Arch: "noarch"}},
		{"star--1__9f2c1ab4d3e8__arm64", parsedTag{Kind: kindBuild, Encoded: "star--1", Qualifier: "9f2c1ab4d3e8", Arch: "arm64"}},
		{"star--1__eq-e90d3b7714cf__amd64", parsedTag{Kind: kindEquiv, Encoded: "star--1", Qualifier: "e90d3b7714cf", Arch: "amd64"}},
		{"latest", parsedTag{}},
		{"star--1__amd64__x", parsedTag{}},
		{"star--1__9f2c1ab__amd64", parsedTag{}},
		{"star--1__eq-zzzzzzzzzzzz__amd64", parsedTag{}},
		{"star--1__riscv", parsedTag{}},
		{"__amd64", parsedTag{}},
	} {
		if got := parseTag(tc.tag); got != tc.want {
			t.Errorf("parseTag(%q) = %+v, want %+v", tc.tag, got, tc.want)
		}
	}
}

func TestStandardBackendGroupsTagsByDigest(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	a := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "aaaaaaaaaaaa", eq: "eeeeeeeeeeee", created: now,
		extra: []string{"stable"}})
	seed(t, f, seededBuild{name: "star/1", arch: "arm64", id: "bbbbbbbbbbbb", eq: "eeeeeeeeeeee", created: now})

	held, err := backendOf(t, f).Manifests(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("manifests = %+v, want 2", held)
	}
	i := slices.IndexFunc(held, func(h Held) bool { return slices.Contains(h.Tags, a) })
	if i < 0 || !slices.Equal(held[i].Tags, []string{"stable", a}) {
		t.Errorf("tags of the first build = %+v, want its build tag and stable", held)
	}

	if err := backendOf(t, f).Remove(t.Context(), held[i]); err != nil {
		t.Fatal(err)
	}
	if remaining, _ := ListTags(t.Context(), f.base(), removeRepo); len(remaining) != 1 {
		t.Errorf("tags after removal = %v, want only the other architecture's", remaining)
	}
}

func TestStandardBackendNamesWhyARemovalFailed(t *testing.T) {
	f := newFakeRegistry(t)
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "aaaaaaaaaaaa", eq: "eeeeeeeeeeee", created: time.Now()})
	b := backendOf(t, f)
	held, err := b.Manifests(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status int
		want   error
	}{{405, ErrNoRemoval}, {403, ErrUnauthorized}} {
		f.deleteStatus = tc.status
		if err := b.Remove(t.Context(), held[0]); !errors.Is(err, tc.want) {
			t.Errorf("status %d: error = %v, want %v", tc.status, err, tc.want)
		}
	}
}

// Removing the build a name serves hands the name tag, and the equivalence tag, to the next build.
func TestPlanRemoveHandsTagsToTheNewestOtherBuild(t *testing.T) {
	f := newFakeRegistry(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nameTag := spellTag(t, "star/1", "", "amd64")
	eqX := spellTag(t, "star/1", "eq-"+hexKey("eeeeeeeeeeee")[:12], "amd64")
	eqY := spellTag(t, "star/1", "eq-"+hexKey("ffffffffffff")[:12], "amd64")
	old := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: t0})
	mid := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "222222222222", eq: "eeeeeeeeeeee", created: t0.Add(time.Hour), extra: []string{eqX}})
	cur := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "333333333333", eq: "ffffffffffff", created: t0.Add(2 * time.Hour), extra: []string{nameTag, eqY}})
	ctx, b := t.Context(), backendOf(t, f)

	plan, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Identity: "333333"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].Tag != nameTag || plan.Moves[0].ToBuild != mid {
		t.Fatalf("moves = %+v, want the name tag to the middle build (the equivalence tag has no other build and goes with it)", plan.Moves)
	}
	if len(plan.Removes) != 1 || !slices.Contains(plan.Removes[0].Tags, cur) {
		t.Fatalf("removes = %+v, want the current build", plan.Removes)
	}

	// The moves are made before the manifest goes.
	order := &orderBackend{Backend: b, f: f, tag: nameTag, wantTo: mid}
	if _, err := Apply(ctx, f.base(), removeRepo, order, plan); err != nil {
		t.Fatal(err)
	}
	if order.removed != 1 || order.err != nil {
		t.Fatalf("removed %d, check = %v", order.removed, order.err)
	}

	// Now the middle build, holding the name tag and the only equivalence tag of its class.
	plan, err = PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Identity: "222222"})
	if err != nil {
		t.Fatal(err)
	}
	moved := map[string]string{}
	for _, m := range plan.Moves {
		moved[m.Tag] = m.ToBuild
	}
	if moved[nameTag] != old || moved[eqX] != old || len(moved) != 2 {
		t.Fatalf("moves = %v, want both tags to the oldest build", moved)
	}
	if _, err := Apply(ctx, f.base(), removeRepo, b, plan); err != nil {
		t.Fatal(err)
	}
	if tags, _ := ListTags(ctx, f.base(), removeRepo); !slices.Contains(tags, nameTag) || !slices.Contains(tags, eqX) || slices.Contains(tags, mid) {
		t.Errorf("tags after the second removal = %v", tags)
	}
}

// orderBackend checks, at the moment a manifest is removed, that the moved tag already points at its new home.
type orderBackend struct {
	Backend
	f       *fakeRegistry
	tag     string
	wantTo  string
	removed int
	err     error
}

func (o *orderBackend) Remove(ctx context.Context, h Held) error {
	o.f.mu.Lock()
	got, want := o.f.content[removeRepo+"/"+o.tag], o.f.content[removeRepo+"/"+o.wantTo]
	o.f.mu.Unlock()
	if got.Digest != want.Digest {
		o.err = errors.New("the name tag had not moved yet")
	}
	o.removed++
	return o.Backend.Remove(ctx, h)
}

func TestPlanRemoveByNameRemovesEveryBuildAndNothingElse(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	nameTag := spellTag(t, "star/1", "", "amd64")
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: now})
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "222222222222", eq: "eeeeeeeeeeee", created: now, extra: []string{nameTag}})
	seed(t, f, seededBuild{name: "star/1", arch: "arm64", id: "333333333333", eq: "eeeeeeeeeeee", created: now})
	other := seed(t, f, seededBuild{name: "samtools/1", arch: "amd64", id: "444444444444", eq: "eeeeeeeeeeee", created: now})
	ctx, b := t.Context(), backendOf(t, f)

	plan, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removes) != 3 || len(plan.Moves) != 0 {
		t.Fatalf("plan = %+v, want 3 removals and no moves", plan)
	}
	plan, err = PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Arch: "arm64"})
	if err != nil || len(plan.Removes) != 1 {
		t.Fatalf("arm64 only: plan = %+v, err = %v", plan, err)
	}
	plan, _ = PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1"})
	if _, err := Apply(ctx, f.base(), removeRepo, b, plan); err != nil {
		t.Fatal(err)
	}
	if tags, _ := ListTags(ctx, f.base(), removeRepo); !slices.Equal(tags, []string{other}) {
		t.Errorf("tags left = %v, want only the other name's", tags)
	}
}

func TestPlanRemoveRefusals(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "ab1111111111", eq: "eeeeeeeeeeee", created: now})
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "ab2222222222", eq: "eeeeeeeeeeee", created: now})
	seed(t, f, seededBuild{name: "star/2", arch: "amd64", id: "cc3333333333", eq: "eeeeeeeeeeee", created: now, extra: []string{"keepme"}})
	ctx, b := t.Context(), backendOf(t, f)

	if _, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Identity: "ab"}); !errors.Is(err, ErrMismatch) {
		t.Errorf("an ambiguous prefix = %v, want ErrMismatch", err)
	}
	if _, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Identity: "99"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such build = %v, want ErrNotFound", err)
	}
	if _, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/2", Identity: "cc"}); err == nil {
		t.Error("a manifest with a tag of unknown spelling was planned for removal")
	}
	plan, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/2"})
	if err != nil || len(plan.Removes) != 0 || len(plan.Kept) != 1 {
		t.Errorf("whole name with a foreign tag: plan = %+v, err = %v; want it kept", plan, err)
	}
	if _, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Arch: "riscv"}); err == nil {
		t.Error("an unknown architecture was accepted")
	}
}

func TestPlanGCTakesOnlyWhatNothingServes(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	named := spellTag(t, "star/1", "", "amd64")
	eq := spellTag(t, "star/1", "eq-"+hexKey("eeeeeeeeeeee")[:12], "amd64")
	orphan := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: now})
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "222222222222", eq: "eeeeeeeeeeee", created: now, extra: []string{named}})
	seed(t, f, seededBuild{name: "star/1", arch: "arm64", id: "333333333333", eq: "ffffffffffff", created: now, extra: []string{eq}})
	seed(t, f, seededBuild{name: "star/1", arch: "arm64", id: "444444444444", eq: "ffffffffffff", created: now, extra: []string{"manual"}})
	elsewhere := seed(t, f, seededBuild{name: "samtools/1", arch: "amd64", id: "555555555555", eq: "eeeeeeeeeeee", created: now})
	ctx, b := t.Context(), backendOf(t, f)

	plan, err := PlanGC(ctx, b, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var gone []string
	for _, h := range plan.Removes {
		gone = append(gone, h.Tags...)
	}
	slices.Sort(gone)
	if want := []string{elsewhere, orphan}; !slices.Equal(gone, want) {
		t.Errorf("collected %v, want %v", gone, want)
	}
	if len(plan.Kept) != 1 {
		t.Errorf("kept = %+v, want the manifest with a foreign tag", plan.Kept)
	}
	if plan, _ = PlanGC(ctx, b, "star/1", nil); len(plan.Removes) != 1 || plan.Removes[0].Tags[0] != orphan {
		t.Errorf("limited to star/1: removes = %+v", plan.Removes)
	}
}

// A manifest that gained a tag after the plan was made is not removed.
func TestApplySkipsWhatChangedSincePlanning(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	orphan := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: now})
	b, ctx := backendOf(t, f), t.Context()
	plan, err := PlanGC(ctx, b, "", nil)
	if err != nil || len(plan.Removes) != 1 {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
	// A push finishes its tags between the plan and the answer.
	name := spellTag(t, "star/1", "", "amd64")
	f.mu.Lock()
	f.content[removeRepo+"/"+name] = f.content[removeRepo+"/"+orphan]
	f.tags[removeRepo] = append(f.tags[removeRepo], name)
	f.mu.Unlock()

	res, err := Apply(ctx, f.base(), removeRepo, b, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || len(res.Skipped) != 1 {
		t.Errorf("result = %+v, want the manifest skipped", res)
	}
	if tags, _ := ListTags(ctx, f.base(), removeRepo); len(tags) != 2 {
		t.Errorf("tags = %v, want both kept", tags)
	}
}

// A project collection keeps what the lock pins, with its tags, and drops the rest; an empty pin set would drop everything.
func TestPlanUnpinnedKeepsOnlyWhatIsPinned(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	nameTag := spellTag(t, "star/1", "", "amd64")
	kept := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: now, extra: []string{nameTag}})
	gone := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "222222222222", eq: "eeeeeeeeeeee", created: now})
	ctx, b := t.Context(), backendOf(t, f)

	held, err := b.Manifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]bool{}
	for _, h := range held {
		if slices.Contains(h.Tags, kept) {
			pinned[h.Desc.Digest.String()] = true
		}
	}
	plan, err := PlanUnpinned(ctx, b, pinned)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removes) != 1 || plan.Removes[0].Tags[0] != gone || len(plan.Moves) != 0 {
		t.Fatalf("plan = %+v, want only the unpinned build and no moves", plan)
	}
	if _, err := PlanUnpinned(ctx, b, map[string]bool{}); err == nil {
		t.Error("an empty pin set planned to remove the whole repository")
	}
}

// A name the source has no recipe for loses every build, served or not.
func TestPlanGCWithoutRecipesRemovesNamesTheSourceLacks(t *testing.T) {
	f := newFakeRegistry(t)
	now := time.Now()
	kept := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: now,
		extra: []string{spellTag(t, "star/1", "", "amd64")}})
	seed(t, f, seededBuild{name: "old/1", arch: "amd64", id: "222222222222", eq: "eeeeeeeeeeee", created: now,
		extra: []string{spellTag(t, "old/1", "", "amd64")}})
	seed(t, f, seededBuild{name: "old/1", arch: "arm64", id: "333333333333", eq: "eeeeeeeeeeee", created: now})
	ctx, b := t.Context(), backendOf(t, f)

	plan, err := PlanGC(ctx, b, "", func(name string) bool { return name == "star/1" })
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removes) != 2 {
		t.Fatalf("removes = %+v, want both builds of old/1", plan.Removes)
	}
	for _, h := range plan.Removes {
		if slices.Contains(h.Tags, kept) {
			t.Errorf("a build of a name with a recipe was removed: %+v", h)
		}
	}
	if plan, _ = PlanGC(ctx, b, "", nil); len(plan.Removes) != 1 {
		t.Errorf("without the check, removes = %+v, want only the unserved build", plan.Removes)
	}
}

// A tag that cannot be moved stops the run before anything is removed.
func TestApplyRemovesNothingWhenAMoveFails(t *testing.T) {
	f := newFakeRegistry(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nameTag := spellTag(t, "star/1", "", "amd64")
	seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "111111111111", eq: "eeeeeeeeeeee", created: t0})
	cur := seed(t, f, seededBuild{name: "star/1", arch: "amd64", id: "222222222222", eq: "eeeeeeeeeeee", created: t0.Add(time.Hour), extra: []string{nameTag}})
	ctx, b := t.Context(), backendOf(t, f)

	plan, err := PlanRemove(ctx, f.base(), removeRepo, b, RemoveTarget{Name: "star/1", Identity: "222222"})
	if err != nil || len(plan.Moves) != 1 {
		t.Fatalf("plan = %+v, err = %v; want one move", plan, err)
	}
	f.refusePut = nameTag
	order := &orderBackend{Backend: b}
	res, err := Apply(ctx, f.base(), removeRepo, order, plan)
	if err == nil {
		t.Fatal("a refused move was reported as success")
	}
	if order.removed != 0 || len(res.Removed) != 0 {
		t.Errorf("removed %d manifest(s) after a failed move, want none", order.removed)
	}
	tags, _ := ListTags(ctx, f.base(), removeRepo)
	if !slices.Contains(tags, cur) || !slices.Contains(tags, nameTag) {
		t.Errorf("tags after the failed move = %v, want the current build and its name tag kept", tags)
	}
}
