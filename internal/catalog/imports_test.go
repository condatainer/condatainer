package catalog

import (
	"go/build"
	"testing"
)

func TestDoesNotImportConfig(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if imp == "github.com/condatainer/condatainer/internal/config" {
			t.Errorf("catalog imports %s, which imports catalog", imp)
		}
	}
}
