package toolpath

import (
	"context"
	"debug/elf"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// fixedFuse2fs is the first e2fsprogs release whose fuse2fs truncates in open.
const fixedFuse2fs = "1.47.3"

var fuse2fsVersion = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// linksLibfuse3 reports whether the executable at path is linked against libfuse 3, replaceable by tests.
var linksLibfuse3 = func(path string) bool {
	f, err := elf.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	libs, err := f.ImportedLibraries()
	if err != nil {
		return false
	}
	for _, lib := range libs {
		if strings.HasPrefix(lib, "libfuse3.so") {
			return true
		}
	}
	return false
}

// Fuse2fsProblem returns why a writable image mounted by the fuse2fs at path would be corrupted, or "" when it would not or the answer cannot be told.
//   - A fuse2fs older than 1.47.3 built on libfuse 3 ignores the truncate in open, so a file rewritten shorter keeps its old size.
//   - The answer is cached per binary, like a version floor.
func Fuse2fsProblem(path string) string {
	const kind = "fuse2fs-truncate"
	if cached, ok := cacheGet(kind, path); ok {
		return cached
	}
	problem := fuse2fsProblem(path)
	cachePut(kind, path, problem)
	return problem
}

func fuse2fsProblem(path string) string {
	if !linksLibfuse3(path) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	version := fuse2fsVersion.FindString(string(out))
	if version == "" || utils.CompareVersions(version, fixedFuse2fs) >= 0 {
		return ""
	}
	return fmt.Sprintf("fuse2fs %s at %s keeps the old size of a file that is rewritten shorter", version, path)
}
