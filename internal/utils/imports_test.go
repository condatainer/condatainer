package utils

import (
	"go/build"
	"strings"
	"testing"
)

func TestImportsNoCondatainerPackage(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.HasPrefix(imp, "github.com/condatainer/condatainer/") {
			t.Errorf("utils imports %s; it must import no other condatainer package", imp)
		}
	}
}
