package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/registry"
)

// otherArch is an architecture this machine is not.
func otherArch() string {
	if meta.NativeArch() == "amd64" {
		return "arm64"
	}
	return "amd64"
}

// A plan for another architecture looks its prebuilt up for that architecture,
// and a recipe that runs anywhere is looked up as noarch.
func TestFindPrebuiltAsksForThePlannedArchitecture(t *testing.T) {
	oldResolve := resolvePrebuilt
	t.Cleanup(func() { resolvePrebuilt = oldResolve })
	var asked string
	resolvePrebuilt = func(_ context.Context, _, _ string, a registry.Address) (registry.Found, error) {
		asked = a.Arch
		return registry.Found{}, registry.ErrNotFound
	}
	ctx := ForArch(t.Context(), otherArch())

	b := prebuiltObject(t, []byte("echo demo\n"), "origin.invalid/lab")
	if _, _, _, err := b.findPrebuilt(ctx); err != nil {
		t.Fatal(err)
	}
	if asked != otherArch() {
		t.Errorf("asked for %q, want %q", asked, otherArch())
	}

	b.spec.Image.Noarch = true
	if _, _, _, err := b.findPrebuilt(ctx); err != nil {
		t.Fatal(err)
	}
	if asked != registry.ArchNoarch {
		t.Errorf("asked for %q, want noarch", asked)
	}
}

// The resolver checks a recipe's #ARCH: against the planned architecture.
func TestResolveChecksThePlannedArchitecture(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "recipes", "tool", "1.0")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#DESC:tool\n#ARCH:"+otherArch()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(t.Context(), []catalog.Spec{{Name: "local", Base: root}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := resolveDeps(ForArch(t.Context(), otherArch()), cat, []string{"tool/1.0"}, nil); err != nil {
		t.Errorf("a recipe for the planned architecture was refused: %v", err)
	}
	if _, err := resolveDeps(t.Context(), cat, []string{"tool/1.0"}, nil); !errors.Is(err, catalog.ErrUnsupportedArch) {
		t.Errorf("err = %v, want this machine refused", err)
	}
}

func TestRunRefusesAPlanForAnotherArchitecture(t *testing.T) {
	bg := &BuildGraph{graph: map[string]*BuildObject{}}
	if err := bg.Run(ForArch(t.Context(), otherArch())); !errors.Is(err, ErrForeignArch) {
		t.Fatalf("err = %v, want ErrForeignArch", err)
	}
}

// Plan reports each node in order: what is pulled, from where, and which copy,
// what is built by a job, and what waits on what.
func TestPlanReportsEachNode(t *testing.T) {
	identity := meta.KeyRef{Scheme: "script-identity-v1", SHA256: "9f2c"}
	equiv := meta.KeyRef{Scheme: "script-equiv-v1", SHA256: "e90d"}
	tool := planNode("tool/1", artifact.TypeApp, prebuiltPull)
	tool.plannedEquiv = equiv
	tool.prebuilt.candidate = prebuiltCandidate{
		endpoint: "ghcr.io/lab/cnt",
		desc:     ocispec.Descriptor{Digest: digest.Digest("sha256:abcd")},
		annotations: map[string]string{
			registry.AnnIdentityScheme: identity.Scheme, registry.AnnIdentitySHA: identity.SHA256,
		},
	}
	tool.tgt = targetFor(filepath.Join(t.TempDir(), "tool--1.sqf"))
	data := planNode("data/1", artifact.TypeData, prebuiltNone, "tool/1")
	data.spec.Image.Noarch = true
	data.spec.Source.RequiresInput = true

	bg := &BuildGraph{
		ctx:             ForArch(t.Context(), otherArch()),
		graph:           map[string]*BuildObject{"tool/1": tool, "data/1": data},
		order:           []*BuildObject{tool, data},
		localBuilds:     []*BuildObject{tool},
		schedulerBuilds: []*BuildObject{data},
	}
	steps := bg.Plan()
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}

	pull, build := steps[0], steps[1]
	if pull.Name != "tool/1" || !pull.Pull || pull.Endpoint != "ghcr.io/lab/cnt" || pull.Digest != "sha256:abcd" ||
		pull.Identity != identity || pull.Equiv != equiv || pull.Arch != otherArch() || pull.InJob || pull.Path != tool.tgt.Path {
		t.Errorf("pull = %+v", pull)
	}
	if build.Name != "data/1" || build.Pull || !build.InJob || build.Arch != meta.ArchNone || !build.RequiresInput ||
		len(build.DependsOn) != 1 || build.DependsOn[0] != "tool/1" {
		t.Errorf("build = %+v", build)
	}
}

// A plan that is never run asks none of the recipe's questions, and cannot run.
func TestPlanOnlyAsksNoQuestionsAndDoesNotRun(t *testing.T) {
	b := &BuildObject{
		spec:         Spec{Image: ImageSpec{Name: "cellranger/9.0.1"}},
		inputPrompts: []string{"Paste the download link"},
	}
	if err := b.collectInputAnswers(PlanOnly(t.Context())); err != nil || len(b.inputAnswers) != 0 {
		t.Fatalf("answers = %q, err = %v; want none asked", b.inputAnswers, err)
	}
	if err := b.collectInputAnswers(t.Context()); err == nil {
		t.Fatal("a run with no terminal took no answers and did not refuse")
	}

	bg := &BuildGraph{graph: map[string]*BuildObject{}}
	if err := bg.Run(PlanOnly(t.Context())); !errors.Is(err, ErrPlanOnly) {
		t.Fatalf("err = %v, want ErrPlanOnly", err)
	}
}
