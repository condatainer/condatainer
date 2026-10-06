package settings

import (
	"go/build"
	"strings"
	"testing"
)

func TestImportsOnlyUtils(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.HasPrefix(imp, "github.com/condatainer/condatainer/") && !strings.HasSuffix(imp, "/internal/utils") {
			t.Errorf("settings imports %s; it may import only utils", imp)
		}
	}
}
