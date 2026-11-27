package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestSolveCreateNameExactThenBaseThenConda(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		path := filepath.Join(root, "recipes", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#DESC:test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hello/1.0") // exact name must outrank ubuntu24/hello/1.0
	write("ubuntu24/hello/1.0")
	write("ubuntu24/simple-os.def")        // zero-slash shortcut
	write("ubuntu24/versioned-os/1.0.def") // one-slash shortcut

	oldSources := config.Global.Sources
	config.Global.Sources = []catalog.Spec{{Name: "test", Base: root}}
	settingstest.Override(t, "default_distro", "ubuntu24")
	// No channels: the Conda-search gate must not make network calls in a
	// case it can already answer, or when nothing satisfiable to report is
	// possible either way.
	settingstest.OverrideList(t, "channels")
	config.ResetCatalog()
	t.Cleanup(func() {
		config.Global.Sources = oldSources
		config.ResetCatalog()
	})

	for _, tc := range []struct {
		input, want string
		expanded    bool
	}{
		{"hello/1.0", "hello/1.0", false},
		{"simple-os", "ubuntu24/simple-os", true},
		{"versioned-os/1.0", "ubuntu24/versioned-os/1.0", true},
		{"samtools/1.21", "samtools/1.21", false},
		{"bioconda::samtools/1.21", "bioconda::samtools/1.21", false},
		{"already/deep/1.0", "already/deep/1.0", false},
	} {
		got, expanded, err := solveCreateName(context.Background(), tc.input)
		if err != nil {
			t.Errorf("solveCreateName(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want || expanded != tc.expanded {
			t.Errorf("solveCreateName(%q) = (%q, %v), want (%q, %v)",
				tc.input, got, expanded, tc.want, tc.expanded)
		}
	}

	// A bare name with no catalog entry and no channels configured to search
	// can never produce a valid conda spec (setupCondaFields requires
	// name/version) — the solver reports this before a build ever starts.
	if _, _, err := solveCreateName(context.Background(), "nonexistent-bare-pkg"); err == nil {
		t.Error("solveCreateName(bare, no catalog entry, no channels) should error, got nil")
	}
}

// ensure that every compress option declared in config is registered as a
// flag on the create command.  This guards against drift when new options are
// added.
// A flag nobody classified is one a submitted build might silently drop.
func TestEveryCreateFlagHasAJobRule(t *testing.T) {
	createCmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		// Cobra adds help itself, when a command first runs.
		if _, ok := createJobRules[f.Name]; !ok && !compFlagNames[f.Name] && f.Name != "help" {
			t.Errorf("--%s has no entry in createJobRules: say whether a submitted build repeats it", f.Name)
		}
	})
	for name := range createJobRules {
		if createCmd.LocalFlags().Lookup(name) == nil {
			t.Errorf("createJobRules lists --%s, which create does not have", name)
		}
	}
}

func TestCreateFlagsForCompressOptions(t *testing.T) {
	for _, opt := range build.CompressOptions {
		if createCmd.Flags().Lookup(opt.Name) == nil {
			t.Errorf("create command missing flag for compression option %q", opt.Name)
		}
	}
}

func TestRecipeCommandsHaveRepeatableSourceFlag(t *testing.T) {
	for _, cmd := range []*cobra.Command{availCmd, createCmd} {
		flag := cmd.Flags().Lookup("source")
		if flag == nil {
			t.Errorf("%s command has no --source flag", cmd.Name())
			continue
		}
		if flag.Value.Type() != "stringArray" {
			t.Errorf("%s --source type = %q, want stringArray", cmd.Name(), flag.Value.Type())
		}
		if flag.Shorthand != "s" {
			t.Errorf("%s --source shorthand = %q, want %q", cmd.Name(), flag.Shorthand, "s")
		}
	}
}

func TestSourceHandleCompletion(t *testing.T) {
	oldSources := config.Global.Sources
	config.Global.Sources = []catalog.Spec{
		{Name: "site", Base: "/site"},
		{Name: "lab", Base: "/lab"},
		{Name: "cnt", Base: "/cnt"},
	}
	t.Cleanup(func() { config.Global.Sources = oldSources })

	got, directive := sourceHandleCompletion(availCmd, nil, "s")
	if directive != cobra.ShellCompDirectiveNoFileComp || len(got) != 1 || got[0] != "site" {
		t.Fatalf("completion = %v, %v", got, directive)
	}
}

// Two compression flags in one command are refused, and one sets the stored arguments.
func TestCompressionFlagsAreExclusive(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, opt := range build.CompressOptions {
		settings.AddSwitch(fs, "build.compress_args", opt.Name, opt.Name)
	}
	first, second := build.CompressOptions[0], build.CompressOptions[1]
	if err := fs.Parse([]string{"--" + first.Name}); err != nil {
		t.Fatal(err)
	}
	if got := build.CompressArgs(); got != first.Args {
		t.Errorf("CompressArgs = %q, want %q", got, first.Args)
	}
	if err := fs.Parse([]string{"--" + second.Name}); err == nil {
		t.Error("two compression flags were accepted")
	}
}

// --name picks the target; --file must not quietly replace it with a prefix
// derived from the filename. The mode dispatch tests --prefix first, so a
// derived prefix used to make the --name branch unreachable.
func TestDerivePrefixFromFile(t *testing.T) {
	cases := []struct {
		name             string
		file, prefix, nm string
		want             string
	}{
		{"file alone derives a prefix", "environment.yml", "", "", "environment"},
		{"name wins over the filename", "environment.yml", "", "myenv", ""},
		{"explicit prefix is kept", "environment.yml", "/images/x", "", ""},
		{"no file, nothing to derive", "", "", "", ""},
		{"path keeps its directory", "/tmp/envs/build.sh", "", "", "/tmp/envs/build"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := derivePrefixFromFile(tc.file, tc.prefix, tc.nm); got != tc.want {
				t.Errorf("derivePrefixFromFile(%q, %q, %q) = %q, want %q",
					tc.file, tc.prefix, tc.nm, got, tc.want)
			}
		})
	}
}

// Restoring a project from a lockfile recreates the names the catalog uses, and
// a data image is several levels deep. The name has to survive the round trip
// through the filename, which is what the depth limit used to be guarding.
func TestNormalizedTargetNameKeepsDepth(t *testing.T) {
	prev := createName
	t.Cleanup(func() { createName = prev })

	for _, want := range []string{
		"myenv",
		"samtools/1.23.1",
		"grch38/star/2.7.11b/gencode47-101",
	} {
		createName = want
		got := normalizedTargetName()
		if got != want {
			t.Errorf("normalizedTargetName() = %q, want %q", got, want)
		}
		// / becomes -- on the way to a filename, and back on the way in.
		roundTrip := utils.NormalizeModule(strings.ReplaceAll(got, "/", "--"))
		if roundTrip != want {
			t.Errorf("round trip through filename = %q, want %q", roundTrip, want)
		}
	}
}

func TestResolvePrefix(t *testing.T) {
	images := filepath.Join(t.TempDir(), "images")
	prev := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{images}
	t.Cleanup(func() { config.GlobalDataPaths = prev })
	elsewhere := t.TempDir()
	targeted := filepath.Join(elsewhere, "build.sh")
	if err := os.WriteFile(targeted, []byte("#!/usr/bin/env bash\n#TARGET: star/2.7\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	targetedDef := filepath.Join(elsewhere, "tool.def")
	if err := os.WriteFile(targetedDef, []byte("#TARGET: ubuntu24/tool\nBootstrap: docker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		desc        string
		prefix, own string // own is --name
		file        string
		want        string // resolved prefix, "" meaning unchanged
		note, err   bool
	}{
		{"plain basename", filepath.Join(elsewhere, "custom"), "", "", "", false, false},
		{"a trailing .sqf names the same file", filepath.Join(elsewhere, "custom.sqf"), "", "", filepath.Join(elsewhere, "custom"), false, false},
		{"only .sqf names no file", filepath.Join(elsewhere, ".sqf"), "", "", "", false, true},
		{"a trailing .sqf in an images dir", filepath.Join(images, "star--2.7.sqf"), "star/2.7", "", filepath.Join(images, "star--2.7"), false, false},
		{"-- is read as /", filepath.Join(elsewhere, "a--b"), "", "", "", true, false},
		{"= is spelled as the file it names", filepath.Join(elsewhere, "foo=1"), "", "", filepath.Join(elsewhere, "foo--1"), true, false},
		{"a name leaves the path alone", filepath.Join(elsewhere, "foo=1"), "star/2.7", "", "", false, false},
		{"a #TARGET: leaves the path alone", filepath.Join(elsewhere, "foo=1"), "", targeted, "", false, false},
		{"a definition's #TARGET: leaves the path alone", filepath.Join(elsewhere, "foo=1"), "", targetedDef, "", false, false},
		{"images dir needs the encoded name", filepath.Join(images, "other"), "star/2.7", "", "", false, true},
		{"images dir with the encoded name", filepath.Join(images, "star--2.7"), "star/2.7", "", "", false, false},
		{"images dir needs the #TARGET: name", filepath.Join(images, "other"), "", targeted, "", false, true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got, note, err := resolvePrefix(tc.prefix, tc.own, tc.file)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			want := tc.want
			if want == "" {
				want = tc.prefix
			}
			if got != want {
				t.Errorf("prefix = %q, want %q", got, want)
			}
			if (note != "") != tc.note {
				t.Errorf("note = %q, want a note: %v", note, tc.note)
			}
		})
	}
}

// A bare or partial name takes what is installed, and --latest takes the newest.
// A template named as its target resolves to a version; any other template key stays as typed, for the variant prompt.
func TestSolveCreateTakesAnInstalledVersionUnlessLatest(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"tool/1.0":        "#DESC:test\n",
		"tool/1.2":        "#DESC:test\n",
		"other/2.0":       "#DESC:test\n",
		"vendor":          "#DESC:test\n#TARGET:vendor/{version}\n#PH:version:1.0,2.0\n",
		"vendor-template": "#DESC:test\n#TARGET:vendor/{version}\n#PH:version:1.0,2.0\n",
	} {
		path := filepath.Join(root, "recipes", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	images := t.TempDir()
	if err := os.WriteFile(filepath.Join(images, "tool--1.0.sqf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	oldSources, oldPaths := config.Global.Sources, config.GlobalDataPaths
	config.Global.Sources = []catalog.Spec{{Name: "test", Base: root}}
	config.GlobalDataPaths.ImagesDirs = []string{images}
	settingstest.Override(t, "default_distro", "ubuntu24")
	settingstest.OverrideList(t, "channels")
	config.ResetCatalog()
	build.InvalidateInstalledOverlays()
	t.Cleanup(func() {
		config.Global.Sources, config.GlobalDataPaths = oldSources, oldPaths
		config.ResetCatalog()
		build.InvalidateInstalledOverlays()
	})

	for _, tc := range []struct {
		input   string
		latest  bool
		want    string
		newer   string
		changed bool
	}{
		{"tool", false, "tool/1.0", "1.2", true},
		{"tool/1", false, "tool/1.0", "1.2", true},
		{"tool", true, "tool/1.2", "", true},
		{"tool/1.0", false, "tool/1.0", "", false},
		{"tool/1.0", true, "tool/1.0", "", false},
		{"other", false, "other/2.0", "", true},
		{"vendor", false, "vendor/2.0", "", true},
		{"vendor-template", false, "vendor-template", "", false},
	} {
		got, err := solveCreate(context.Background(), tc.input, tc.latest)
		if err != nil {
			t.Errorf("solveCreate(%q, latest=%v): %v", tc.input, tc.latest, err)
			continue
		}
		if got.Name != tc.want || got.Newer != tc.newer || got.Expanded != tc.changed {
			t.Errorf("solveCreate(%q, latest=%v) = %+v, want name %q newer %q expanded %v",
				tc.input, tc.latest, got, tc.want, tc.newer, tc.changed)
		}
	}
}

// A submitted build carries --usage when it was typed or record_usage names create.
func TestSubmittedBuildCarriesTheUsageFlag(t *testing.T) {
	t.Cleanup(func() { createUsage = false })
	if got := strings.Join(createJobFlags(), " "); strings.Contains(got, "--usage") {
		t.Errorf("nothing asked for = %q", got)
	}
	settingstest.OverrideList(t, "record_usage", "create")
	if got := strings.Join(createJobFlags(), " "); !strings.Contains(got, "--usage") {
		t.Errorf("record_usage names create = %q", got)
	}
	settingstest.OverrideList(t, "record_usage", "run")
	createUsage = true
	if got := strings.Join(createJobFlags(), " "); !strings.Contains(got, "--usage") {
		t.Errorf("a typed flag = %q", got)
	}
}
