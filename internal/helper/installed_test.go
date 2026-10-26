package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInstalledRecordsAndIgnoresAMissingFile(t *testing.T) {
	dir := t.TempDir()
	if rec, err := ReadInstalled(dir); err != nil || len(rec.Helpers) != 0 {
		t.Fatalf("no record: %+v, %v", rec, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "svc"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := UpdateInstalled(dir, func(r *Installed) error {
		r.Helpers["svc"] = InstalledEntry{Source: "lab", SHA256: "ab"}
		r.Helpers["gone"] = InstalledEntry{Source: "lab", SHA256: "cd"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ReadInstalled(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := rec.Lookup(dir, "svc"); !ok || e.SHA256 != "ab" || e.Source != "lab" {
		t.Errorf("svc = %+v, %v", e, ok)
	}
	if _, ok := rec.Lookup(dir, "gone"); ok {
		t.Error("an entry whose file is gone was returned")
	}
}

func TestInstalledRefusesANewerSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, installedFile), []byte(`{"schema_version": 2, "helpers": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInstalled(dir); !errors.Is(err, ErrInstalledSchema) || !strings.Contains(err.Error(), "update CondaTainer") {
		t.Errorf("err = %v", err)
	}
}

func TestInstalledUpdatesKeepEachOther(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := UpdateInstalled(dir, func(r *Installed) error {
				r.Helpers[fmt.Sprintf("h%d", i)] = InstalledEntry{SHA256: "x"}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if rec, err := ReadInstalled(dir); err != nil || len(rec.Helpers) != 8 {
		t.Errorf("entries = %d, %v, want 8", len(rec.Helpers), err)
	}
}

func TestInstalledFailedChangeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	boom := errors.New("boom")
	if err := UpdateInstalled(dir, func(r *Installed) error {
		r.Helpers["a"] = InstalledEntry{}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, installedFile)); !os.IsNotExist(err) {
		t.Errorf("a record was written: %v", err)
	}
}
