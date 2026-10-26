package catalog

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/utils"
)

const starRecipe = `#!/usr/bin/env bash
# Build a STAR index. This comment must not end the header block.
#DESC:STAR {star_version} index for GENCODE {gencode_version}
#URL:https://github.com/alexdobin/STAR

#TARGET:grch38/star/{star_version}/gencode{gencode_version}-{read_length}
#PH:star_version:2.7.11b,2.7.11a,2.7.9a
#PH:gencode_version:47-49
#PH:read_length:101|151|*

#DEP:star/{star_version}
#DEP:samtools/1.23.1>=1.10
#ENV:STAR_INDEX={prefix}/index   ## pass to --genomeDir
#INPUT:paste the download link

#SBATCH --cpus-per-task=8
#SBATCH --mem=64G

set -euo pipefail
cat > "$CNT_BUILD_TMPDIR/job.sh" <<'EOF'
#SBATCH --this-is-not-a-directive
#DEP:not/a/dep
EOF
STAR --runThreadN "$NCPU" --genomeDir "$CNT_BUILD_PREFIX/index"
`

func parse(t *testing.T, path, body string) *Recipe {
	t.Helper()
	r, err := ParseRecipe(path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseRecipe(%q): %v", path, err)
	}
	return r
}

func TestParseRecipe(t *testing.T) {
	r := parse(t, "grch38/star-gencode", starRecipe)

	if r.Name != "grch38/star-gencode" || r.Path != "grch38/star-gencode" {
		t.Errorf("name/path = %q/%q", r.Name, r.Path)
	}
	if r.Type != artifact.TypeData {
		t.Errorf("Type = %q, want data (from the target's slash count)", r.Type)
	}
	if !r.IsTemplate {
		t.Error("IsTemplate = false")
	}
	if r.Description != "STAR {star_version} index for GENCODE {gencode_version}" {
		t.Errorf("Description = %q", r.Description)
	}
	if r.URL != "https://github.com/alexdobin/STAR" {
		t.Errorf("URL = %q", r.URL)
	}

	// The heredoc's lines count too: position carries no meaning, so a recipe
	// that writes a job script also declares whatever that script declares.
	wantDeps := []string{"star/{star_version}", "samtools/1.23.1>=1.10", "not/a/dep"}
	if !slices.Equal(r.Deps, wantDeps) {
		t.Errorf("Deps = %v, want %v", r.Deps, wantDeps)
	}
	wantDirectives := []string{"#SBATCH --cpus-per-task=8", "#SBATCH --mem=64G", "#SBATCH --this-is-not-a-directive"}
	if !slices.Equal(r.Directives, wantDirectives) {
		t.Errorf("Directives = %v, want %v", r.Directives, wantDirectives)
	}

	if len(r.Env) != 1 || r.Env[0].Key != "STAR_INDEX" || r.Env[0].Note != "pass to --genomeDir" {
		t.Fatalf("Env = %+v", r.Env)
	}
	if got := r.Env[0].Value(nil); got != "{prefix}/index" {
		t.Errorf("Env value = %q, want the token intact", got)
	}
	if len(r.Inputs) != 1 || r.Inputs[0] != "paste the download link" {
		t.Errorf("Inputs = %+v", r.Inputs)
	}
}

// Position never disqualifies an annotation. A recipe that writes a job script
// therefore adopts that script's declarations as its own — the cost of the rule,
// recorded here so it is a decision rather than a surprise.
func TestParseRecipeReadsAnnotationsAnywhere(t *testing.T) {
	r := parse(t, "grch38/star-gencode", starRecipe)
	if !slices.Contains(r.Deps, "not/a/dep") {
		t.Errorf("Deps = %v, want the heredoc's #DEP: included", r.Deps)
	}
	if !slices.Contains(r.Directives, "#SBATCH --this-is-not-a-directive") {
		t.Errorf("Directives = %v, want the heredoc's #SBATCH included", r.Directives)
	}

	// Nothing ends the annotations, so a key after an executable line is read.
	below := parse(t, "x/y", "#DESC:kept\nBootstrap: docker\n#URL:below\n")
	if below.Description != "kept" || below.URL != "below" {
		t.Errorf("description=%q url=%q, want both read", below.Description, below.URL)
	}

	// A .def has no shebang and needs no exception.
	def := parse(t, "ubuntu24/base.def", "#DESC:base\n\nBootstrap: docker\n")
	if def.Name != "ubuntu24/base" || def.Type != artifact.TypeOS || def.Description != "base" {
		t.Errorf("def = %q/%q/%q", def.Name, def.Type, def.Description)
	}
}

// A directive carries colons in its value, so it is matched by prefix rather
// than cut on the first colon like an annotation.
func TestDirectivesAreNotAnnotations(t *testing.T) {
	recipe, err := ParseRecipe("x/1.0", strings.NewReader("#SBATCH --time=01:00:00\n#DEP: star/2.7\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recipe.Directives) != 1 || recipe.Directives[0] != "#SBATCH --time=01:00:00" {
		t.Errorf("directives = %#v", recipe.Directives)
	}
	if len(recipe.Deps) != 1 || recipe.Deps[0] != "star/2.7" {
		t.Errorf("deps = %#v", recipe.Deps)
	}
}

// Nothing that only describes a recipe may reach a key. Every header item that
// should is carried by its own record line instead, so the preimage has to be
// blind to all of them — including the ones tooling rewrites unprompted.
func TestRecipePreimageIgnoresDescription(t *testing.T) {
	preimage := func(text string) string { return string(utils.StripComments([]byte(text))) }
	base := preimage(starRecipe)

	cosmetic := map[string]string{
		"a #PH: menu autoupdate grew": strings.Replace(starRecipe,
			"#PH:star_version:2.7.11b,2.7.11a,2.7.9a",
			"#PH:star_version:2.7.12,2.7.11b,2.7.11a,2.7.9a", 1),
		"a reworded #DESC:": strings.Replace(starRecipe,
			"#DESC:STAR {star_version} index", "#DESC:STAR {star_version} genome index", 1),
		"a changed directive": strings.Replace(starRecipe,
			"#SBATCH --mem=64G", "#SBATCH --mem=128G", 1),
		"a reworded body comment": strings.Replace(starRecipe,
			"# Build a STAR index. This comment must not end the header block.",
			"# Builds the index.", 1),
	}
	for what, edited := range cosmetic {
		if edited == starRecipe {
			t.Fatalf("%s: the fixture did not change", what)
		}
		if got := preimage(edited); got != base {
			t.Errorf("%s moved the preimage:\n%q\nvs\n%q", what, got, base)
		}
	}

	if preimage(strings.Replace(starRecipe, "--runThreadN", "--runThreadN 1 --sjdbOverhang", 1)) == base {
		t.Error("a code change did not move the preimage")
	}
}

func TestParseRecipeRedistribution(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		license string
		want    *bool
	}{
		{"absent", "#!/bin/bash\n", "", nil},
		{"declared yes", "#!/bin/bash\n#LICENSE: MIT\n#REDISTRIBUTE: yes\n", "MIT", boolp(true)},
		{"declared no", "#!/bin/bash\n#REDISTRIBUTE: no\n", "", boolp(false)},
		{"case is not load-bearing", "#!/bin/bash\n#REDISTRIBUTE: Yes\n", "", boolp(true)},
		// Kept verbatim: a compound expression is documentation, not something
		// anything parses to derive permission from.
		{"compound licence", "#!/bin/bash\n#LICENSE: GPL-3.0-or-later AND LicenseRef-Vendor\n",
			"GPL-3.0-or-later AND LicenseRef-Vendor", nil},
		// A value that is neither is *not* silently read as unanswered; it is
		// kept so Validate can name it.
		{"unrecognized value", "#!/bin/bash\n#REDISTRIBUTE: maybe\n", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := ParseRecipe("recipes/x/1.0", strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("ParseRecipe: %v", err)
			}
			if rec.License != tt.license {
				t.Errorf("License = %q, want %q", rec.License, tt.license)
			}
			got := rec.Redistributable()
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("Redistributable() = %v, want nil", *got)
			case tt.want != nil && got == nil:
				t.Errorf("Redistributable() = nil, want %v", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("Redistributable() = %v, want %v", *got, *tt.want)
			}
		})
	}
}

// A typo must not read as "unanswered", which would fall back to the type
// default and publish the artifact the author was holding back.
func TestValidateRejectsUnknownRedistribute(t *testing.T) {
	rec, err := ParseRecipe("recipes/x/1.0", strings.NewReader("#!/bin/bash\n#REDISTRIBUTE: maybe\n"))
	if err != nil {
		t.Fatalf("ParseRecipe: %v", err)
	}
	err = rec.Validate()
	if err == nil {
		t.Fatal("#REDISTRIBUTE: maybe must not validate")
	}
	if !errors.Is(err, ErrInvalidRecipe) || !strings.Contains(err.Error(), "maybe") {
		t.Errorf("error should be ErrInvalidRecipe naming the value: %v", err)
	}
	for _, ok := range []string{"yes", "no"} {
		rec.Redistribute = ok
		if err := rec.Validate(); err != nil {
			t.Errorf("#REDISTRIBUTE: %s must validate: %v", ok, err)
		}
	}
}

// Both headers are whole-line comments, and RecipeDigest hashes StripComments —
// so annotating an existing recipe must not move a single key.
func TestRedistributionHeadersLeaveTheDigestAlone(t *testing.T) {
	plain := []byte("#!/bin/bash\nmake install\n")
	annotated := []byte("#!/bin/bash\n#LICENSE: MIT\n#REDISTRIBUTE: yes\nmake install\n")
	if a, b := string(utils.StripComments(plain)), string(utils.StripComments(annotated)); a != b {
		t.Errorf("StripComments differs after annotating:\n%q\n%q", a, b)
	}
}

func boolp(b bool) *bool { return &b }
