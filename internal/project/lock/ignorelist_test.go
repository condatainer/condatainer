package lock

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestIgnoreLine(t *testing.T) {
	root, cwd := "/p", "/p/src"
	for arg, want := range map[string]string{
		"tmp/*.py":     "/src/tmp/*.py",
		"../data":      "/data",
		"gen/":         "/src/gen/",
		"/notebooks":   "/notebooks",
		"/p/src/a.sh":  "/src/a.sh",
		"./a.sh":       "/src/a.sh",
		"../../escape": "",
		"/":            "",
		"..":           "",
	} {
		got, err := IgnoreLine(root, cwd, arg)
		if (err != nil) != (want == "") || got != want {
			t.Errorf("%q: %q, %v; want %q", arg, got, err, want)
		}
	}
}

func TestScriptsMatchingCountsScriptsInsideADirectory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib/a.sh", "#!/bin/bash\n")
	write(t, root, "lib/deep/b.sh", "#!/bin/bash\n")
	write(t, root, "keep.sh", "#!/bin/bash\n")
	write(t, root, "cnt-lock/ignore", "/lib\n")

	got, err := ScriptsMatching(root, "/lib/")
	if err != nil || !reflect.DeepEqual(got, []string{"lib/a.sh", "lib/deep/b.sh"}) {
		t.Errorf("scripts = %v, %v", got, err)
	}
}

func TestWriteIgnoreListSortsAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	if err := WriteIgnoreList(root, []string{"/b", "# note", "/a", "/b"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadIgnoreList(filepath.Clean(root))
	if err != nil || !reflect.DeepEqual(got, []string{"# note", "/a", "/b"}) {
		t.Errorf("list = %v, %v", got, err)
	}
}
