package publish

import (
	"context"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/registry"
)

// buildPlan vendors the artifacts, pins the named ones, and plans a push.
func buildPlan(t *testing.T, root string, l *lock.Lock, opts Options) *Plan {
	t.Helper()
	verified, problems := lock.Verify(root, l)
	if len(problems) > 0 {
		t.Fatalf("fixture does not verify: %v", problems)
	}
	plan, err := Build(context.Background(), root, l, verified, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return plan
}

func lockWith(t *testing.T, root string, push string) *lock.Lock {
	t.Helper()
	l := lock.New()
	l.OCI = lock.OCI{Push: push}
	return l
}

func TestBuildPublishesThePinsAlone(t *testing.T) {
	root := projectRoot(t)
	_, depManifest := vendorData(t, root, "cutadapt/5.0", "echo cutadapt\n")
	pin, _ := vendorData(t, root, "grch38/genome/gencode49", "echo index\n", meta.Dependency{
		Name:     depManifest.Name,
		Type:     depManifest.Type,
		Identity: depManifest.Keys.Identity,
		Equiv:    depManifest.Keys.Equiv,
		Role:     "data",
	})

	l := lockWith(t, root, "ghcr.io/lab/p/cnt")
	l.Pins["grch38/genome/gencode49"] = lock.PinEntry{Artifact: pin}

	plan := buildPlan(t, root, l, Options{})
	if len(plan.Steps) != 1 || plan.Steps[0].Artifact != pin {
		t.Fatalf("the plan is not the pins alone: %#v", plan.Steps)
	}
	// A pin takes the build tag, then the name tag, each ending in the architecture.
	tags := plan.Steps[0].Tags
	if len(tags) != 2 || registry.IsNameTag(tags[0]) || !registry.IsNameTag(tags[1]) ||
		!strings.HasSuffix(tags[0], "__amd64") || !strings.HasSuffix(tags[1], "__amd64") {
		t.Errorf("a pin's tags = %v, want its build tag and its name tag for amd64", tags)
	}
}

// A push with no destination cannot be planned, and the message has to name the
// command that fixes it.
func TestBuildRefusesWithoutADestination(t *testing.T) {
	root := projectRoot(t)
	l := lock.New()
	verified, _ := lock.Verify(root, l)
	_, err := Build(context.Background(), root, l, verified, Options{})
	if err == nil || !strings.Contains(err.Error(), "project registry set") {
		t.Errorf("err = %v, want one naming `project registry set`", err)
	}
}

// A public endpoint refuses an undeclared app, and the refusal has to reach the
// plan's problems rather than being silently dropped from the set.
func TestBuildRefusesWhatTheEndpointWillNotTake(t *testing.T) {
	root := projectRoot(t)
	app := vendorApp(t, root, "star/2.7.11b", "echo star\n")

	l := lockWith(t, root, "ghcr.io/lab/p/cnt")
	l.Pins["star/2.7.11b"] = lock.PinEntry{Artifact: app}

	plan := buildPlan(t, root, l, Options{})
	if len(plan.Steps) != 1 || plan.Steps[0].Disposition != Refused {
		t.Fatalf("an undeclared app was not refused: %#v", plan.Steps)
	}
	if plan.Complete() {
		t.Error("a refusal did not reach the plan's problems")
	}

	// Declared, and it publishes. The type rule is a default for an unanswered
	// question, not an absolute.
	l.OCI.Audience = "restricted"
	if plan := buildPlan(t, root, l, Options{}); plan.Steps[0].Disposition != Upload {
		t.Errorf("a restricted endpoint refused an app: %#v", plan.Steps[0])
	}
}

// Two pins sharing a manifest name: neither can hold the name tag, since
// it is a claim about which one the project uses.
func TestAmbiguousNamesLoseTheNameTag(t *testing.T) {
	root := projectRoot(t)
	one, _ := vendorData(t, root, "grch38/genome/gencode49", "echo one\n")
	two, _ := vendorData(t, root, "grch38/genome/gencode49", "echo two\n")
	if one == two {
		t.Fatal("the fixture produced one artifact, not two identities of one name")
	}

	l := lockWith(t, root, "ghcr.io/lab/p/cnt")
	l.Pins["grch38/genome/gencode49"] = lock.PinEntry{Artifact: one}
	l.Pins["path:overlays/gencode49.sqf"] = lock.PinEntry{Artifact: two}

	// Build resolves it, not Refine: the collision check runs right after, and
	// a shared plain tag left in place would refuse the whole push offline.
	plan := buildPlan(t, root, l, Options{})
	if !plan.Complete() {
		t.Fatalf("a shared name refused the push: %v", plan.Problems)
	}
	if len(plan.Ambiguous) != 1 || plan.Ambiguous[0] != "grch38/genome/gencode49" {
		t.Errorf("ambiguous = %v, want the shared name once", plan.Ambiguous)
	}
	for _, step := range plan.Steps {
		for _, tag := range step.Tags {
			if registry.IsNameTag(tag) {
				t.Errorf("%s kept the ambiguous name tag %q", step.Name, tag)
			}
		}
		if len(step.Tags) != 1 {
			t.Errorf("%s should keep exactly its qualified tag, got %v", step.Name, step.Tags)
		}
	}
}

// A tag whose spelling depended on the rest of the set would be unfindable from
// the artifact alone, which is what lets a re-push skip an upload. So a
// collision refuses rather than lengthening the prefix.
func TestTagCollisionRefuses(t *testing.T) {
	plan := &Plan{Steps: []Step{
		{Artifact: "provenance/a", Tags: []string{"x--1.0__aaaaaaaaaaaa"}},
		{Artifact: "provenance/b", Tags: []string{"x--1.0__aaaaaaaaaaaa"}},
	}}
	if err := checkTagCollisions(plan); err == nil {
		t.Error("two artifacts composing one tag were not refused")
	}
}

// Every frozen environment is named meta.EnvName, so a project pinning two
// shares a name by construction. A frozen environment takes no name tag, so
// that plans as two build tags and no ambiguity, never as a collision that
// refuses the whole push.
func TestTwoFrozenEnvironmentsPublish(t *testing.T) {
	root := projectRoot(t)
	one := vendorEnv(t, root, strings.Repeat("1a", 32))
	two := vendorEnv(t, root, strings.Repeat("2b", 32))

	l := lockWith(t, root, "ghcr.io/lab/p/cnt")
	l.OCI.Audience = "restricted" // A snapshot never reaches a public endpoint.
	l.Pins["path:overlays/analysis.sqf"] = lock.PinEntry{Artifact: one, Manual: true}
	l.Pins["path:overlays/dev.sqf"] = lock.PinEntry{Artifact: two, Manual: true}

	plan := buildPlan(t, root, l, Options{})
	if !plan.Complete() {
		t.Fatalf("two environments refused the push: %v", plan.Problems)
	}
	if len(plan.Ambiguous) != 0 {
		t.Errorf("ambiguous = %v, want none: a frozen environment has no name tag", plan.Ambiguous)
	}
	for _, step := range plan.Steps {
		if step.Disposition != Upload {
			t.Errorf("%s: disposition %q, %s", step.Artifact, step.Disposition, step.Reason)
		}
		if len(step.Tags) != 1 || !strings.HasPrefix(step.Tags[0], meta.EnvName+"__") {
			t.Errorf("%s tags = %v, want one qualified tag", step.Artifact, step.Tags)
		}
	}
}

// A digest the lock already records is proof the destination holds the build,
// so the manifest is not read. An unrecorded one is read and checked.
func TestAlreadyPublishedReadsNoManifestForARecordedDigest(t *testing.T) {
	root := projectRoot(t)
	_, manifest := vendorData(t, root, "grch38/genome/gencode49", "echo index\n")
	annotations := map[string]string{
		registry.AnnTitle:          manifest.Name,
		registry.AnnSchema:         "1",
		registry.AnnArch:           "amd64",
		registry.AnnIdentityScheme: manifest.Keys.Identity.Scheme,
		registry.AnnIdentitySHA:    manifest.Keys.Identity.SHA256,
		registry.AnnEquivScheme:    manifest.Keys.Equiv.Scheme,
		registry.AnnEquivSHA:       manifest.Keys.Equiv.SHA256,
	}
	address, reads := serveCountedManifest(t, annotations)
	repository := address + "/cnt"
	base, repo, err := registry.SplitCoordinate(repository)
	if err != nil {
		t.Fatal(err)
	}
	head, err := registry.ResolveTag(context.Background(), base, repo, "star__amd64")
	if err != nil {
		t.Fatal(err)
	}

	plan := func(recorded []lock.Remote) *Plan {
		return &Plan{Repository: repository, Steps: []Step{{
			Name: manifest.Name, Identity: manifest.Keys.Identity.Digest(),
			Disposition: Upload, Tags: []string{"star__amd64"}, Recorded: recorded,
		}}}
	}

	known := plan([]lock.Remote{{Repository: repository, ManifestDigest: head.Digest.String()}})
	markAlreadyPublished(context.Background(), known)
	if known.Steps[0].Disposition != Present || known.Steps[0].Remote.ManifestDigest != head.Digest.String() {
		t.Errorf("a recorded digest = %+v, want present at that digest", known.Steps[0])
	}
	if reads.Load() != 0 {
		t.Errorf("a recorded digest read %d manifests, want none", reads.Load())
	}

	unknown := plan(nil)
	markAlreadyPublished(context.Background(), unknown)
	if unknown.Steps[0].Disposition != Present {
		t.Errorf("an unrecorded build = %+v, want present after its manifest is checked", unknown.Steps[0])
	}
	if reads.Load() != 1 {
		t.Errorf("an unrecorded build read %d manifests, want one", reads.Load())
	}
}
