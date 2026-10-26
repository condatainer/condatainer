package helper

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/utils"
)

// offersFrom reads the helpers a directory source offers, with its hashes.
func offersFrom(t *testing.T, src string) map[string]RemoteScriptEntry {
	t.Helper()
	cat, err := catalog.Open(t.Context(), []catalog.Spec{{Name: "lab", Base: src}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}
	offers, err := readHelperIndex(t.Context(), cat, cat[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range offers {
		e.Source = cat[0]
		offers[name] = e
	}
	return offers
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAddRecordsAndRefusesWhatItDidNotInstall(t *testing.T) {
	src, dir := t.TempDir(), t.TempDir()
	writeScript(t, filepath.Join(src, "helpers", "svc"), "echo v1\n")
	entry := offersFrom(t, src)["svc"]

	if err := addOne(t.Context(), "svc", entry, dir, false); err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadInstalled(dir)
	if e, ok := rec.Lookup(dir, "svc"); !ok || e.SHA256 != entry.SHA256 || e.Source != "lab" {
		t.Errorf("record = %+v, %v", e, ok)
	}
	if err := addOne(t.Context(), "svc", entry, dir, false); !errors.Is(err, ErrAlreadyAdded) {
		t.Errorf("a second add: %v", err)
	}

	handmade := t.TempDir()
	writeScript(t, filepath.Join(handmade, "svc"), "echo mine\n")
	if err := addOne(t.Context(), "svc", entry, handmade, false); !errors.Is(err, ErrUnrecorded) {
		t.Errorf("a hand-copied file: %v", err)
	}
	if err := addOne(t.Context(), "svc", entry, handmade, true); err != nil {
		t.Errorf("forced: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(handmade, "svc")); hashHex(got) != entry.SHA256 {
		t.Error("force did not replace the file")
	}

	same := t.TempDir()
	writeScript(t, filepath.Join(same, "svc"), "echo v1\n")
	if err := addOne(t.Context(), "svc", entry, same, false); err != nil {
		t.Errorf("a copy equal to the source's: %v", err)
	}
	if rec, _ := ReadInstalled(same); len(rec.Helpers) != 1 {
		t.Error("an equal copy was not recorded")
	}
}

func TestUpgradePlanAndApply(t *testing.T) {
	src, dir := t.TempDir(), t.TempDir()
	for _, name := range []string{"cur", "old", "edited", "gone"} {
		writeScript(t, filepath.Join(src, "helpers", name), "echo v1\n")
	}
	for name, entry := range offersFrom(t, src) {
		if err := addOne(t.Context(), name, entry, dir, false); err != nil {
			t.Fatal(err)
		}
	}
	writeScript(t, filepath.Join(src, "helpers", "old"), "echo v2\n")
	writeScript(t, filepath.Join(src, "helpers", "edited"), "echo v2\n")
	writeScript(t, filepath.Join(dir, "edited"), "echo local\n")
	if err := os.Remove(filepath.Join(src, "helpers", "gone")); err != nil {
		t.Fatal(err)
	}

	offers := offersFrom(t, src)
	items, err := planUpgrade(offers, []string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]UpgradeAction{}
	for _, it := range items {
		got[it.Name] = it.Action
	}
	want := map[string]UpgradeAction{"cur": UpgradeCurrent, "old": UpgradeReplace, "edited": UpgradeEdited, "gone": UpgradeDropped}
	for name, action := range want {
		if got[name] != action {
			t.Errorf("%s: %s, want %s", name, got[name], action)
		}
	}

	var replace UpgradeItem
	for _, it := range items {
		if it.Action == UpgradeReplace {
			replace = it
		}
	}
	if err := upgradeOne(t.Context(), replace); err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadInstalled(dir)
	if e, _ := rec.Lookup(dir, "old"); e.SHA256 != offers["old"].SHA256 {
		t.Error("the new hash was not recorded")
	}
	if _, err := planUpgrade(offers, []string{dir}, []string{"never"}); !errors.Is(err, ErrNotAdded) {
		t.Errorf("an unknown name: %v", err)
	}
}

func TestRemoveDeletesOnlyWhatWasAdded(t *testing.T) {
	src, near, far := t.TempDir(), t.TempDir(), t.TempDir()
	writeScript(t, filepath.Join(src, "helpers", "svc"), "echo v1\n")
	entry := offersFrom(t, src)["svc"]
	if err := addOne(t.Context(), "svc", entry, near, false); err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(far, "svc"), "echo far\n")
	writeScript(t, filepath.Join(far, "mine"), "echo mine\n")

	dirs := []string{near, far}
	if shadowedBy(dirs, far, "svc") != filepath.Join(near, "svc") {
		t.Error("the nearer copy is not reported as shadowing")
	}
	path, uncovered, err := removeOne("svc", dirs)
	if err != nil || path != filepath.Join(near, "svc") || uncovered != filepath.Join(far, "svc") {
		t.Fatalf("removed %q, uncovered %q, %v", path, uncovered, err)
	}
	if rec, _ := ReadInstalled(near); len(rec.Helpers) != 0 {
		t.Error("the record still names it")
	}
	if _, _, err := removeOne("mine", dirs); !errors.Is(err, ErrUnrecorded) {
		t.Errorf("an unrecorded file: %v", err)
	}
	if !utils.FileExists(filepath.Join(far, "mine")) {
		t.Error("an unrecorded file was deleted")
	}
	if _, _, err := removeOne("absent", dirs); !errors.Is(err, ErrNotAdded) {
		t.Errorf("a missing name: %v", err)
	}
}

func TestRemoveRefusesAHelperWithARunInProgress(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := AppendHistory(&HelperRun{ID: "r1", Name: "svc", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := Remove([]string{"svc"}, "", nil); !errors.Is(err, ErrInUse) {
		t.Errorf("err = %v", err)
	}
}
