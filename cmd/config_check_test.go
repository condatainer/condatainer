package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
)

func writeLayer(t *testing.T, name, typ, content string) *config.Layer {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := config.ReadLayer(path, typ)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func findingTexts(fs []configFinding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(filepath.Base(f.Where) + " " + f.Message + "\n")
	}
	return b.String()
}

func TestCheckReportsUnknownBadAndUnrunnable(t *testing.T) {
	l := writeLayer(t, "config.yaml", "user", `build:
  ncpus: 0
  ncpu: 4
  mem: 8g
nested_run: maybe
host_apptainer: /nonexistent/apptainer
sources:
  - lab: /x
`)
	got := findingTexts(checkLayerFile(l))
	for _, want := range []string{
		`config.yaml:3 unknown key "build.ncpu"`,
		"config.yaml:2 build.ncpus: 0 is below the minimum 1",
		"config.yaml:5 nested_run:",
		"config.yaml:6 host_apptainer /nonexistent/apptainer:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mem") || strings.Contains(got, "sources") {
		t.Errorf("a good key or sources was reported:\n%s", got)
	}
}

func TestCheckReportsAValueAHigherLayerOverrides(t *testing.T) {
	user := writeLayer(t, "user.yaml", "user", "build:\n  ncpus: 8\n")
	site := writeLayer(t, "site.yaml", "app-root", "build:\n  ncpus: 4\n")
	settings.SetLayers([]settings.Layer{user, site})
	t.Cleanup(func() { settings.SetLayers(nil) })

	var info []string
	for _, f := range checkConfig([]*config.Layer{user, site}, nil) {
		if f.Problem {
			t.Errorf("problem: %+v", f)
		} else {
			info = append(info, f.Message)
		}
	}
	if len(info) != 1 || info[0] != "build.ncpus in app-root is overridden by user" {
		t.Errorf("info = %v", info)
	}
}

func TestCheckReportsUnknownConfigVariables(t *testing.T) {
	got := checkEnvironment([]string{"CNT_CONFIG_BUILD_NCPUS=4", "CNT_CONFIG_BUILD_NCPU=4", "CNT_ROOT=/x", "HOME=/h"})
	if len(got) != 1 || got[0].Where != "CNT_CONFIG_BUILD_NCPU" || !got[0].Problem {
		t.Errorf("findings = %+v", got)
	}
}

func TestConfigHelpFindsKeysAndSections(t *testing.T) {
	if _, ok := settings.Lookup("scheduler.slurm.emit_mem"); !ok {
		t.Fatal("key is not registered")
	}
	if got := settings.Section("scheduler.slurm"); len(got) != 1 {
		t.Errorf("section = %d keys", len(got))
	}
	if flags := (func() []string { k, _ := settings.Lookup("build.compress_args"); return k.FlagNames() })(); len(flags) < 2 {
		t.Errorf("compress_args flags = %v", flags)
	}
}

// declareRenamed registers a key with an old name and a removed one for one test.
func declareRenamed(t *testing.T) {
	settingstest.Declare(t, func() {
		settings.Bool("test.renamed.new", settings.Help("renamed"),
			settings.Replaces("test.renamed.old", settings.Since("0.5")))
		settings.Removed("test.renamed.gone", "set test.renamed.new instead", settings.Since("0.5"))
	})
}

func TestCheckReportsRenamedAndRemovedKeys(t *testing.T) {
	declareRenamed(t)
	l := writeLayer(t, "config.yaml", "user", "test:\n  renamed:\n    old: true\n    gone: 1\n")
	findings := checkLayerFile(l)
	if len(findings) != 2 {
		t.Fatalf("findings = %+v", findings)
	}
	old, gone := findings[0], findings[1]
	if old.Problem || !old.Deprecated || old.Rename == nil ||
		!strings.Contains(old.Message, "test.renamed.old is deprecated since 0.5; use test.renamed.new") {
		t.Errorf("renamed = %+v", old)
	}
	if !gone.Problem || !strings.Contains(gone.Message, "was removed since 0.5: set test.renamed.new instead") {
		t.Errorf("removed = %+v", gone)
	}
}

func TestCheckReportsRenamedAndRemovedVariables(t *testing.T) {
	declareRenamed(t)
	got := checkEnvironment([]string{"CNT_CONFIG_TEST_RENAMED_OLD=1", "CNT_CONFIG_TEST_RENAMED_GONE=1"})
	if len(got) != 2 || !got[1].Deprecated && !got[0].Deprecated {
		t.Fatalf("findings = %+v", got)
	}
	var dep, problem configFinding
	for _, f := range got {
		if f.Deprecated {
			dep = f
		} else {
			problem = f
		}
	}
	if !strings.Contains(dep.Message, "use CNT_CONFIG_TEST_RENAMED_NEW") || !problem.Problem {
		t.Errorf("dep = %+v, problem = %+v", dep, problem)
	}
}

func TestFixRenamesRewritesTheKeyAndKeepsComments(t *testing.T) {
	declareRenamed(t)
	l := writeLayer(t, "config.yaml", "user", "# mine\ntest:\n  renamed:\n    old: true # keep\nautoload_gpu: false\n")
	findings := fixRenames([]*config.Layer{l}, checkLayerFile(l))
	if len(findings) != 1 || findings[0].Deprecated || !strings.HasPrefix(findings[0].Message, "renamed ") {
		t.Fatalf("findings = %+v", findings)
	}
	data, _ := os.ReadFile(l.Path)
	for _, want := range []string{"# mine", "autoload_gpu: false", "new: true # keep"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q in:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "old:") {
		t.Errorf("the old key is still there:\n%s", data)
	}
}

func TestFixDropsAnOldKeyWhenTheNewOneIsSet(t *testing.T) {
	declareRenamed(t)
	l := writeLayer(t, "config.yaml", "user", "test:\n  renamed:\n    old: true\n    new: false\n")
	fixRenames([]*config.Layer{l}, checkLayerFile(l))
	got := config.ReadConfigKey(l.Path, "test.renamed.new")
	if got != "false" || config.ReadConfigKey(l.Path, "test.renamed.old") != "" {
		t.Errorf("new = %q", got)
	}
}

func TestOldNameCannotBeSet(t *testing.T) {
	declareRenamed(t)
	if !refuseOldName("test.renamed.old") || !refuseOldName("test.renamed.gone") || refuseOldName("test.renamed.new") {
		t.Error("an old or removed name is refused, a live one is not")
	}
}

// An alias is deleted once the version it names is reached.
func TestNoAliasOutlivesItsRemoveIn(t *testing.T) {
	if config.Version == "" || strings.Contains(config.Version, "dev") {
		t.Skip("a development build")
	}
	for _, a := range settings.AllAliases() {
		if a.RemoveIn != "" && utils.CompareVersions(config.Version, a.RemoveIn) >= 0 {
			t.Errorf("%s should have been deleted in %s", a.Old, a.RemoveIn)
		}
	}
	for _, r := range settings.RemovedKeys() {
		if r.RemoveIn != "" && utils.CompareVersions(config.Version, r.RemoveIn) >= 0 {
			t.Errorf("%s should have been deleted in %s", r.Name, r.RemoveIn)
		}
	}
}
