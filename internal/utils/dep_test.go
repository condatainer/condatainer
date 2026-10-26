package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"samtools/1.2", "samtools/1.2"},
		{"samtools=1.2", "samtools/1.2"},
		{"samtools--1.2", "samtools/1.2"},
		{"  samtools=1.2  ", "samtools/1.2"},
		{"samtools", "samtools"},
		{"samtools=1.21>=1.16", "samtools/1.21>=1.16"},
		{"star/2.7.11b@9f2c1ab4d3e8", "star/2.7.11b@9f2c1ab4d3e8"},
		{"grch38/star/2.7.11b/gencode47-101", "grch38/star/2.7.11b/gencode47-101"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeModule(tt.in); got != tt.want {
			t.Errorf("NormalizeModule(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseDep(t *testing.T) {
	tests := []struct {
		raw  string
		want Dep
	}{
		{"samtools/1.23.1>=1.10", Dep{Name: "samtools", Version: "1.23.1", Op: ">=", Min: "1.10"}},
		{"star/2.7.11b", Dep{Name: "star", Version: "2.7.11b"}},
		{"star/2.7.11b>2.7.0", Dep{Name: "star", Version: "2.7.11b", Op: ">", Min: "2.7.0"}},
		{"samtools", Dep{Name: "samtools"}},
		// A data dep: the last component is the version, so the name keeps two.
		{"grch38/genome/gencode", Dep{Name: "grch38/genome", Version: "gencode"}},
		// A template dep is unsubstituted until the parent expands.
		{"grch38/gtf-gencode/{gencode_version}", Dep{Name: "grch38/gtf-gencode", Version: "{gencode_version}"}},
	}
	for _, tt := range tests {
		got, err := ParseDep(tt.raw)
		if err != nil {
			t.Errorf("ParseDep(%q): %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseDep(%q) = %+v, want %+v", tt.raw, got, tt.want)
		}
		if s := got.String(); s != NormalizeModule(tt.raw) {
			t.Errorf("ParseDep(%q).String() = %q, want %q", tt.raw, s, NormalizeModule(tt.raw))
		}
	}
	if _, err := ParseDep("  "); err != ErrEmptyDep {
		t.Errorf("ParseDep(blank) error = %v, want ErrEmptyDep", err)
	}
}

func TestDepSatisfies(t *testing.T) {
	// The preferred version is the implicit upper bound: [1.10, 1.23.1].
	ranged, _ := ParseDep("samtools/1.23.1>=1.10")
	for _, tt := range []struct {
		version string
		want    bool
	}{
		{"1.10", true},
		{"1.21", true},
		{"1.23.1", true},
		{"1.9", false},
		{"1.24", false},
	} {
		if got := ranged.Satisfies(tt.version); got != tt.want {
			t.Errorf("%s.Satisfies(%q) = %v, want %v", ranged, tt.version, got, tt.want)
		}
	}

	// Unconstrained, full version: only the preferred version will do.
	pinned, _ := ParseDep("star/2.7.11b")
	if !pinned.Satisfies("2.7.11b") || pinned.Satisfies("2.7.11a") {
		t.Error("unconstrained dep should admit only its preferred version")
	}

	// Unconstrained, partial version: a dot-component prefix family match.
	partial, _ := ParseDep("openjdk/17")
	for _, tt := range []struct {
		version string
		want    bool
	}{
		{"17.0.18", true},
		{"17", true},
		{"170.0.0", false},
		{"17a.0.0", false},
		{"16.0.0", false},
	} {
		if got := partial.Satisfies(tt.version); got != tt.want {
			t.Errorf("%s.Satisfies(%q) = %v, want %v", partial, tt.version, got, tt.want)
		}
	}
}

// Module lines name the site's own module tree, not CondaTainer artifacts, so
// they contribute nothing however closely they resemble a #DEP: name.
func TestScriptDependenciesIgnoresModuleLines(t *testing.T) {
	tmp, err := os.CreateTemp("", "script-deps-*.sh")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())

	content := `#!/bin/bash
#DEP:foo/1.0
module load alpha/1.0 beta/2.0
ml load gamma/3.0 delta/4.0
ml purge
ml foo/5.0
`
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmp.Close()

	deps, err := ScriptDependencies(tmp.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"foo/1.0"}
	if len(deps) != len(expected) {
		t.Fatalf("expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}
	for i := range expected {
		if deps[i] != expected[i] {
			t.Fatalf("dep[%d] mismatch: expected %s, got %s", i, expected[i], deps[i])
		}
	}
}

func TestScriptDependencies_WithComments(t *testing.T) {
	tmp, err := os.CreateTemp("", "script-deps-comments-*.sh")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())

	content := `#!/bin/bash
#DEP:foo/1.0  ## This is a dependency comment
#DEP:bar/2.0 ## Another comment
module load alpha/1.0 beta/2.0
`
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmp.Close()

	deps, err := ScriptDependencies(tmp.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"foo/1.0", "bar/2.0"}
	if len(deps) != len(expected) {
		t.Fatalf("expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}
	for i := range expected {
		if deps[i] != expected[i] {
			t.Fatalf("dep[%d] mismatch: expected %s, got %s", i, expected[i], deps[i])
		}
	}
}

// Position carries no meaning: a declaration counts wherever it is written,
// including inside a heredoc that writes another script.
func TestScriptDependenciesReadsAnnotationsAnywhere(t *testing.T) {
	tmp, err := os.CreateTemp("", "script-deps-anywhere-*.sh")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())

	content := "#!/bin/bash\n" +
		"#DEP:foo/1.0\n" +
		"echo running\n" +
		"#DEP:below/3.0\n" +
		"cat <<'END' > generated.sh\n" +
		"#DEP:heredoc/4.0\n" +
		"END\n"
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmp.Close()

	deps, err := ScriptDependencies(tmp.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"foo/1.0", "below/3.0", "heredoc/4.0"}
	if len(deps) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, deps)
	}
	for i := range expected {
		if deps[i] != expected[i] {
			t.Fatalf("dep[%d] = %s, want %s", i, deps[i], expected[i])
		}
	}
}

// A running script mounts what it names and records nothing, so an overlay path
// or an external .sqf is a legitimate declaration there. Only a build's #DEP:
// becomes an edge that has to mean the same thing on another machine, and that
// rule lives in catalog.ValidateDeps — never here.
func TestScriptDependenciesKeepsPathDeclarations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.sh")
	content := "#!/bin/bash\n#DEP: samtools/1.21\n#DEP: env.img\n#DEP: env.ext3\n" +
		"#DEP: overlays/tool.sqf\n#DEP: /shared/vendor/tool.sqf\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, err := ScriptDependencies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"samtools/1.21", "env.img", "env.ext3", "overlays/tool.sqf", "/shared/vendor/tool.sqf"}
	if !reflect.DeepEqual(deps, want) {
		t.Fatalf("deps = %v, want %v", deps, want)
	}
}

// IsPathDep decides which grammar a declaration is read with, so its extension
// set has to be utils.IsOverlay's plus .sif — a running script's `#DEP: env.ext3`
// or `#DEP: base.sif` is a path there and must stay one here.
func TestIsPathDepCoversEveryOverlayExtension(t *testing.T) {
	for _, value := range []string{"env.img", "env.ext3", "x.sqf", "x.sqsh", "x.squashfs", "./a/b.SQF", "base.sif"} {
		if !IsPathDep(value) {
			t.Errorf("IsPathDep(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"samtools/1.21", "grch38/genome/gencode", "star/2.7.11b>=2.3"} {
		if IsPathDep(value) {
			t.Errorf("IsPathDep(%q) = true, want false", value)
		}
	}
}
