package registry

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

func TestAudienceAccepts(t *testing.T) {
	yes, no := true, false
	manifest := func(typ artifact.Type, buildType meta.BuildType) meta.Manifest {
		return meta.Manifest{Name: "x/1.0", Type: typ, BuildType: buildType}
	}
	declaring := func(typ artifact.Type, buildType meta.BuildType, answer *bool) meta.Manifest {
		m := manifest(typ, buildType)
		m.Redistribute = answer
		return m
	}
	tests := []struct {
		why      string
		audience Audience
		m        meta.Manifest
		wantErr  bool
	}{
		// Undeclared: the type default stands in for an unanswered question.
		{"apt packages from a public distribution", Public, manifest(artifact.TypeOS, meta.BuildTypeScript), false},
		{"public reference data", Public, manifest(artifact.TypeData, meta.BuildTypeScript), false},
		{"an app redistributes someone else's binaries", Public, manifest(artifact.TypeApp, meta.BuildTypeScript), true},

		// A Conda build embeds no recipe, so it can never carry the declaration
		// the app default would demand. Gating it would refuse it forever.
		{"a Conda app cannot declare, so it is not asked", Public, manifest(artifact.TypeApp, meta.BuildTypeConda), false},
		{"a Conda build of a permitted type", Public, manifest(artifact.TypeData, meta.BuildTypeConda), false},

		// Declared: the recipe answers and the type default does not apply.
		{"a declared app publishes", Public, declaring(artifact.TypeApp, meta.BuildTypeScript, &yes), false},
		{"a refusal beats a permissive type", Public, declaring(artifact.TypeData, meta.BuildTypeScript, &no), true},
		{"a refusal beats the Conda exemption", Public, declaring(artifact.TypeApp, meta.BuildTypeConda, &no), true},

		// A snapshot embeds no recipe, so it can carry no declaration — the same
		// mechanical reason a Conda build is exempt.
		{"a snapshot publishes with nothing to declare in", Public, manifest(artifact.TypeEnv, meta.BuildTypeSnapshot), false},

		{"a restricted endpoint takes an app", Restricted, manifest(artifact.TypeApp, meta.BuildTypeScript), false},
		{"a restricted endpoint takes a Conda build", Restricted, manifest(artifact.TypeApp, meta.BuildTypeConda), false},
		{"a restricted endpoint takes a refused artifact", Restricted, declaring(artifact.TypeApp, meta.BuildTypeScript, &no), false},
	}
	for _, tt := range tests {
		if err := tt.audience.Accepts(tt.m); (err != nil) != tt.wantErr {
			t.Errorf("%s: %s.Accepts(%s/%s) = %v, wantErr %v",
				tt.why, tt.audience, tt.m.Type, tt.m.BuildType, err, tt.wantErr)
		}
	}
}

// The refusal must name both ways out, or a user reads it as "CondaTainer will
// not publish apps" and stops.
func TestAudienceRefusalNamesBothRemedies(t *testing.T) {
	err := Public.Accepts(meta.Manifest{Name: "star/2.7.11b", Type: artifact.TypeApp, BuildType: meta.BuildTypeScript})
	if err == nil {
		t.Fatal("an undeclared app must be refused at a public endpoint")
	}
	for _, want := range []string{"#REDISTRIBUTE: yes", "restricted"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// catalogTags is what a source's repository takes: every tag.
var catalogTags = TagRequest{Name: true, Equiv: true}

// repo is a repository coordinate on the fake registry.
func (f *fakeRegistry) repo() string { return f.base() + "/cnt" }

func (f *fakeRegistry) request(path string) PublishRequest {
	return PublishRequest{Path: path, Repository: f.repo(), Tags: catalogTags}
}

// digestAt reports what a tag points at in the fake registry.
func (f *fakeRegistry) digestAt(t *testing.T, tag string) digest.Digest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	desc, ok := f.content["cnt/"+tag]
	if !ok {
		t.Fatalf("nothing published at %s", tag)
	}
	return desc.Digest
}

// dropTag removes a tag the way a registry console does.
func (f *fakeRegistry) dropTag(tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.content, "cnt/"+tag)
	f.tags["cnt"] = slices.DeleteFunc(f.tags["cnt"], func(have string) bool { return have == tag })
}

func tagsOf(t *testing.T, m meta.Manifest) Tags {
	t.Helper()
	tags, err := TagsFor(m, catalogTags)
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

// The default is the restrictive one: an endpoint that never declared its
// audience must not be treated as private.
func TestPublishDefaultsToPublic(t *testing.T) {
	requireSquashfsTools(t)
	source, _ := packImage(t, imageSpec{name: "hello/1.0", typ: artifact.TypeApp, recipe: "#!/bin/bash\n"})
	f := newFakeRegistry(t)

	_, err := Publish(context.Background(), f.request(source))
	if err == nil {
		t.Fatal("an app was published to an endpoint with no declared audience")
	}
	if !strings.Contains(err.Error(), string(Public)) {
		t.Errorf("the refusal does not say the endpoint was treated as public: %v", err)
	}
}

// The end-to-end contract: what push writes is what pull installs, byte for byte,
// through the real distribution protocol on both sides.
func TestPublishAndPullRoundTrip(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "grch38/genome/gencode49", typ: artifact.TypeData, recipe: "#!/bin/bash\nbuild index\n",
	})
	f := newFakeRegistry(t)
	ctx := context.Background()

	if _, err := Publish(ctx, f.request(source)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	found, err := Lookup(ctx, f.base(), "cnt", Address{Name: m.Name})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if err := Check(found.Annotations, Want{Name: m.Name, Identity: m.Keys.Identity}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "grch38-genome--gencode49.sqf")
	if err := Pull(ctx, f.base(), "cnt", found.Desc, found.Annotations, dest); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !sameBytes(t, source, dest) {
		t.Error("the pulled artifact is not the one that was published")
	}
}

// Nothing can be pinned to an artifact with no scheme-backed keys, so publishing
// one would put something in a registry that no lock could ever name.
func TestPublishRefusesAnArtifactWithNoKeys(t *testing.T) {
	requireSquashfsTools(t)
	source, _ := packImage(t, imageSpec{
		name: "ubuntu24/build-essential", typ: artifact.TypeOS, recipe: "#!/bin/bash\n", noKeys: true,
	})
	f := newFakeRegistry(t)

	_, err := Publish(context.Background(), f.request(source))
	if err == nil {
		t.Fatal("an artifact with no keys was published")
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Errorf("the refusal does not name the missing identity: %v", err)
	}
}

// An artifact is held to its own claim before anyone downstream has to trust it.
func TestPublishRefusesKeysThatDoNotRegenerate(t *testing.T) {
	requireSquashfsTools(t)
	source, _ := packImage(t, imageSpec{
		name: "ubuntu24/build-essential", typ: artifact.TypeOS, recipe: "#!/bin/bash\n",
		tamper: func(m *meta.Manifest) { m.Keys.Identity.SHA256 = strings.Repeat("f", 64) },
	})
	f := newFakeRegistry(t)

	_, err := Publish(context.Background(), f.request(source))
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("err = %v, want ErrInvalidArtifact", err)
	}
	if len(f.tags) != 0 {
		t.Errorf("a refused artifact was published anyway: %v", f.tags)
	}
}

// Nothing is published for an architecture no puller can name in a tag.
func TestPublishRefusesAnArchitectureNothingIsPublishedFor(t *testing.T) {
	requireSquashfsTools(t)
	source, _ := packImage(t, imageSpec{
		name: "hello/1.0", typ: artifact.TypeData, arch: "s390x", recipe: "#!/bin/bash\n",
	})
	f := newFakeRegistry(t)

	_, err := Publish(context.Background(), f.request(source))
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
	}
	if len(f.tags) != 0 {
		t.Errorf("a refused artifact was published anyway: %v", f.tags)
	}
}

// Every tag points at the one manifest, so any of them finds the build.
func TestPublishWritesEveryTag(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "grch38/genome/gencode49", typ: artifact.TypeData, recipe: "#!/bin/bash\n",
	})
	f := newFakeRegistry(t)
	ctx := context.Background()

	desc, err := Publish(ctx, f.request(source))
	if err != nil {
		t.Fatal(err)
	}
	tags := tagsOf(t, m)
	if len(tags.List()) != 3 {
		t.Fatalf("a script build takes three tags, got %v", tags.List())
	}
	for _, tag := range tags.List() {
		if got := f.digestAt(t, tag); got != desc.Digest {
			t.Errorf("%s points at %s, want the manifest %s", tag, got, desc.Digest)
		}
	}
	listed, err := ListTags(ctx, f.base(), "cnt")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Errorf("tags = %v, want exactly the three written", listed)
	}
	// A manifest read counts against a pull limit, and a tag is written from the
	// bytes the push already holds.
	if f.manifestGets != 0 {
		t.Errorf("a fresh push read %d manifests back, want none", f.manifestGets)
	}
}

// A build already published uploads nothing. Pushing it again points whichever
// tags are missing at it, which finishes a push that stopped part way.
func TestPublishOfAPublishedBuildUploadsNothing(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "grch38/genome/gencode49", typ: artifact.TypeData, recipe: "#!/bin/bash\n",
	})
	f := newFakeRegistry(t)
	ctx := context.Background()
	tags := tagsOf(t, m)

	first, err := Publish(ctx, f.request(source))
	if err != nil {
		t.Fatal(err)
	}
	blobs := len(f.blobs)
	again, err := Publish(ctx, f.request(source))
	if err != nil || again.Digest != first.Digest {
		t.Fatalf("second publish = %v, %v; want the first manifest", again.Digest, err)
	}

	f.dropTag(tags.Name)
	f.dropTag(tags.Equiv)
	// A new process has read nothing: the published build is read once and both
	// missing tags are written from that read.
	manifests.Lock()
	clear(manifests.byDigest)
	manifests.Unlock()
	f.manifestGets = 0
	var shown PublishPlan
	req := f.request(source)
	req.Confirm = func(plan PublishPlan) bool { shown = plan; return true }
	if _, err := Publish(ctx, req); err != nil {
		t.Fatalf("finishing publish: %v", err)
	}
	for _, tag := range []string{tags.Name, tags.Equiv} {
		if got := f.digestAt(t, tag); got != first.Digest {
			t.Errorf("%s points at %s, want %s", tag, got, first.Digest)
		}
	}
	if !shown.Existing || !slices.Equal(shown.Tags, []string{tags.Equiv, tags.Name}) {
		t.Errorf("plan = %+v, want the two missing tags of a published build", shown)
	}
	if len(f.blobs) != blobs {
		t.Errorf("blobs went from %d to %d: a published build was uploaded again", blobs, len(f.blobs))
	}
	if f.manifestGets != 1 {
		t.Errorf("finishing the tags read %d manifests, want the published build once", f.manifestGets)
	}
}

// A different build of the same name takes the name tag and leaves the old build
// under its own, so nothing a lock pins loses its tag.
func TestPublishAnotherBuildMovesTheNameTag(t *testing.T) {
	requireSquashfsTools(t)
	first, mFirst := packImage(t, imageSpec{name: "star/2.7.11b", recipe: "#!/bin/bash\none\n"})
	second, mSecond := packImage(t, imageSpec{name: "star/2.7.11b", recipe: "#!/bin/bash\ntwo\n"})
	f := newFakeRegistry(t)
	ctx := context.Background()
	restricted := func(path string) PublishRequest {
		req := f.request(path)
		req.Audience = Restricted
		return req
	}

	oldDesc, err := Publish(ctx, restricted(first))
	if err != nil {
		t.Fatal(err)
	}
	var shown PublishPlan
	req := restricted(second)
	req.Confirm = func(plan PublishPlan) bool { shown = plan; return true }
	newDesc, err := Publish(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	oldTags, newTags := tagsOf(t, mFirst), tagsOf(t, mSecond)
	if got := f.digestAt(t, newTags.Name); got != newDesc.Digest {
		t.Errorf("the name tag serves %s, want the new build %s", got, newDesc.Digest)
	}
	if got := f.digestAt(t, oldTags.Build); got != oldDesc.Digest {
		t.Errorf("the replaced build's tag points at %s, want %s", got, oldDesc.Digest)
	}
	if shown.MovedTag != newTags.Name || !strings.HasPrefix(mFirst.Keys.Identity.SHA256, shown.MovedFrom) || shown.MovedFrom == "" {
		t.Errorf("plan = %+v, want the name tag moving off %s", shown, mFirst.Keys.Identity.SHA256[:keyChars])
	}
	if shown.Existing {
		t.Error("a new build was reported as already published")
	}
}

// Each architecture is its own manifest under its own tags, so pushing one
// never reads or rewrites another.
func TestPublishLeavesAnotherArchitectureAlone(t *testing.T) {
	requireSquashfsTools(t)
	amd, mAMD := packImage(t, imageSpec{name: "star/2.7.11b", arch: "amd64", recipe: "#!/bin/bash\nbuild\n"})
	arm, mARM := packImage(t, imageSpec{name: "star/2.7.11b", arch: "arm64", recipe: "#!/bin/bash\nbuild\n"})
	f := newFakeRegistry(t)
	ctx := context.Background()
	restricted := func(path string) PublishRequest {
		req := f.request(path)
		req.Audience = Restricted
		return req
	}

	amdDesc, err := Publish(ctx, restricted(amd))
	if err != nil {
		t.Fatal(err)
	}
	armDesc, err := Publish(ctx, restricted(arm))
	if err != nil {
		t.Fatal(err)
	}
	if amdDesc.Digest == armDesc.Digest {
		t.Fatal("two architectures share a manifest")
	}
	for _, tag := range tagsOf(t, mAMD).List() {
		if got := f.digestAt(t, tag); got != amdDesc.Digest || !strings.HasSuffix(tag, "__amd64") {
			t.Errorf("%s points at %s after the arm64 push, want the amd64 manifest", tag, got)
		}
	}
	for _, tag := range tagsOf(t, mARM).List() {
		if got := f.digestAt(t, tag); got != armDesc.Digest || !strings.HasSuffix(tag, "__arm64") {
			t.Errorf("%s points at %s, want the arm64 manifest", tag, got)
		}
	}
}

// #ARCH:noarch publishes under noarch tags, and the annotation says so.
func TestPublishTagsANoarchArtifact(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "grch38/genome/gencode49", typ: artifact.TypeData,
		arch: meta.ArchNone, recipe: "#!/bin/bash\n",
	})
	f := newFakeRegistry(t)
	ctx := context.Background()

	if _, err := Publish(ctx, f.request(source)); err != nil {
		t.Fatal(err)
	}
	found, err := Lookup(ctx, f.base(), "cnt", Address{Name: m.Name, Arch: ArchNoarch})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(found.Tag, "__noarch") || found.Annotations[AnnArch] != "noarch" {
		t.Errorf("found %s with annotations %v, want the noarch tag and mark", found.Tag, found.Annotations)
	}
}

// A tag carries only a key prefix, so a build tag that holds another identity is
// never overwritten.
func TestPublishRefusesABuildTagHoldingAnotherIdentity(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{name: "star/2.7.11b", recipe: "#!/bin/bash\nbuild\n"})
	f := newFakeRegistry(t)
	other := m
	other.Keys.Identity.SHA256 = m.Keys.Identity.SHA256[:keyChars] + strings.Repeat("0", 64-keyChars)
	publishArtifact(t, f, "cnt", Annotations(other, ""), tagsOf(t, m).Build)

	req := f.request(source)
	req.Audience = Restricted
	if _, err := Publish(context.Background(), req); !errors.Is(err, ErrMismatch) {
		t.Fatalf("err = %v, want ErrMismatch", err)
	}
}

// A source's repository takes an artifact recording another collection, or none,
// when the publisher agrees: the package links to one code repository, but the
// keys are sound. The plan tells the publisher before anything moves.
func TestPublishReportsAnotherOrNoCollection(t *testing.T) {
	requireSquashfsTools(t)
	recorded, _ := packImage(t, imageSpec{
		name: "star/2.7.11b", recipe: "#!/bin/bash\nbuild\n",
		tamper: func(m *meta.Manifest) { m.Build.Source = "https://example.org/recipes" },
	})
	unrecorded, _ := packImage(t, imageSpec{name: "hello/1.0", recipe: "#!/bin/bash\nbuild\n"})
	f := newFakeRegistry(t)
	push := func(path, collection string, agree bool) (*CollectionMismatch, error) {
		var shown *CollectionMismatch
		req := f.request(path)
		req.Audience, req.Collection = Restricted, collection
		req.Confirm = func(plan PublishPlan) bool { shown = plan.Mismatch; return agree }
		_, err := Publish(context.Background(), req)
		return shown, err
	}

	got, err := push(recorded, "https://example.org/other", false)
	if !errors.Is(err, ErrDeclined) || got == nil || got.Recorded != "https://example.org/recipes" || got.Expected != "https://example.org/other" {
		t.Fatalf("another collection: mismatch = %+v, err = %v; want it shown and declined", got, err)
	}
	if len(f.tags) != 0 {
		t.Errorf("a declined push published anyway: %v", f.tags)
	}
	got, err = push(unrecorded, "https://example.org/other", true)
	if err != nil || got == nil || got.Recorded != "" {
		t.Errorf("no collection: mismatch = %+v, err = %v; want it shown and published", got, err)
	}
	if got, err := push(recorded, "https://example.org/recipes/", true); err != nil || got != nil {
		t.Errorf("the artifact's own collection: mismatch = %+v, err = %v; want none", got, err)
	}
	other, _ := packImage(t, imageSpec{name: "bare/1.0", recipe: "#!/bin/bash\nbare\n"})
	if got, err := push(other, "", true); err != nil || got != nil {
		t.Errorf("a repository with no collection: mismatch = %+v, err = %v; want none", got, err)
	}
}

func TestPushRefusesAWritableOverlay(t *testing.T) {
	f := newFakeRegistry(t)
	_, err := Push(context.Background(), "/images/dev.img", f.base(), "cnt", []string{"dev__amd64"}, nil)
	if err == nil {
		t.Fatal("a writable overlay was pushed")
	}
	if !strings.Contains(err.Error(), "never distributed") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestPushRefusesNoTags(t *testing.T) {
	f := newFakeRegistry(t)
	if _, err := Push(context.Background(), "/images/x.sqf", f.base(), "cnt", nil, nil); err == nil {
		t.Fatal("a push with no tags was accepted")
	}
}

// A project keeps artifacts from several collections in one repository, so that
// package's source is the project rather than whichever collection built each
// artifact. The publisher supplies it; without one the artifact's own answer
// stands.
func TestPublishSourceOverridesTheRecordedCollection(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "star/2.7.11b", typ: artifact.TypeApp, recipe: "#!/bin/bash\nbuild\n",
	})
	ctx := context.Background()
	nameTag, err := NameTag(m.Name, m.Platform.Arch)
	if err != nil {
		t.Fatal(err)
	}

	f := newFakeRegistry(t)
	project := "https://github.com/my-lab/rnaseq-2026"
	req := f.request(source)
	req.Audience, req.Tags, req.Source = Restricted, TagRequest{Name: true}, project
	if _, err := Publish(ctx, req); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_, ann, err := ResolveArtifact(ctx, f.base(), "cnt", nameTag)
	if err != nil {
		t.Fatalf("ResolveArtifact: %v", err)
	}
	if got := ann[AnnSource]; got != project {
		t.Errorf("%s = %q, want %q", AnnSource, got, project)
	}

	// No override: whatever the artifact recorded, which for this fixture is
	// nothing, so the annotation is absent rather than empty.
	g := newFakeRegistry(t)
	req = g.request(source)
	req.Audience = Restricted
	if _, err := Publish(ctx, req); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_, ann, err = ResolveArtifact(ctx, g.base(), "cnt", nameTag)
	if err != nil {
		t.Fatalf("ResolveArtifact: %v", err)
	}
	if got, ok := ann[AnnSource]; ok && got != m.Build.Source {
		t.Errorf("%s = %q, want the artifact's own %q", AnnSource, got, m.Build.Source)
	}
}

// The annotation exists so a registry can link a package to a repository, which
// it does by exact URL match. Anything that is not an http(s) URL links nothing,
// so it never reaches a manifest.
func TestPublishKeepsTheSourceAnnotationUsable(t *testing.T) {
	requireSquashfsTools(t)
	ctx := context.Background()

	// A caller that names one and gets it wrong is told.
	clean, _ := packImage(t, imageSpec{
		name: "star/2.7.11b", typ: artifact.TypeApp, recipe: "#!/bin/bash\nbuild\n",
	})
	f := newFakeRegistry(t)
	req := f.request(clean)
	req.Audience, req.Source = Restricted, "ftp://example.invalid/p"
	_, err := Publish(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "http(s)") {
		t.Fatalf("error = %v, want a refusal naming the scheme", err)
	}

	// An artifact whose own recorded source is unusable still publishes, because
	// a descriptor that predates the check is not a reason to block distribution.
	// The annotation is simply absent.
	dirty, m := packImage(t, imageSpec{
		name: "star/2.7.11b", typ: artifact.TypeApp, recipe: "#!/bin/bash\nbuild\n",
		tamper: func(m *meta.Manifest) { m.Build.Source = "not a url" },
	})
	g := newFakeRegistry(t)
	req = g.request(dirty)
	req.Audience = Restricted
	if _, err := Publish(ctx, req); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	nameTag, _ := NameTag(m.Name, m.Platform.Arch)
	_, ann, err := ResolveArtifact(ctx, g.base(), "cnt", nameTag)
	if err != nil {
		t.Fatalf("ResolveArtifact: %v", err)
	}
	if got, ok := ann[AnnSource]; ok {
		t.Errorf("%s = %q, want it dropped", AnnSource, got)
	}
}

// A declined confirmation ends the push before anything is uploaded, and the
// plan it was shown names the destination.
func TestPublishStopsWhenTheConfirmationIsDeclined(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "grch38/genome/gencode49", typ: artifact.TypeData, recipe: "#!/bin/bash\nbuild index\n",
	})
	f := newFakeRegistry(t)
	ctx := context.Background()

	var shown PublishPlan
	req := f.request(source)
	req.Confirm = func(plan PublishPlan) bool { shown = plan; return false }
	_, err := Publish(ctx, req)
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v, want ErrDeclined", err)
	}
	if shown.Layers < 1 || shown.LayerSize <= 0 {
		t.Errorf("plan has no layer plan: %+v", shown)
	}
	if shown.Name != m.Name || shown.Size == 0 || !strings.HasPrefix(shown.Reference, f.base()) {
		t.Errorf("plan = %+v", shown)
	}
	if _, err := Lookup(ctx, f.base(), "cnt", Address{Name: m.Name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a declined push uploaded the artifact: %v", err)
	}
}

// Prepare reads and checks and moves no bytes: the plan names the artifact, its
// keys and its destination, and publishing it afterwards makes what was planned.
// A build already there is complete, with the manifest it is published as.
func TestPrepareMovesNothingAndPublishesWhatItPlanned(t *testing.T) {
	requireSquashfsTools(t)
	source, m := packImage(t, imageSpec{
		name: "grch38/genome/gencode49", typ: artifact.TypeData, recipe: "#!/bin/bash\nbuild index\n",
	})
	f := newFakeRegistry(t)
	ctx := context.Background()

	prepared, err := Prepare(ctx, f.request(source))
	if err != nil {
		t.Fatal(err)
	}
	plan := prepared.Plan
	if plan.Name != m.Name || plan.Type != artifact.TypeData || plan.Arch != m.Platform.Arch ||
		plan.Identity != m.Keys.Identity || plan.Equiv != m.Keys.Equiv ||
		plan.Repository != f.base()+"/cnt" || plan.Existing || plan.Digest != "" || len(plan.Tags) == 0 {
		t.Errorf("plan = %+v", plan)
	}
	if prepared.Complete() || len(f.blobs) != 0 {
		t.Fatalf("Prepare uploaded something: complete=%v blobs=%d", prepared.Complete(), len(f.blobs))
	}
	if _, err := Lookup(ctx, f.base(), "cnt", Address{Name: m.Name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Prepare published the artifact: %v", err)
	}

	desc, err := prepared.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Prepare(ctx, f.request(source))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Complete() || !again.Plan.Existing || again.Plan.Digest != desc.Digest.String() || len(again.Plan.Tags) != 0 {
		t.Errorf("published build = %+v", again.Plan)
	}
}
