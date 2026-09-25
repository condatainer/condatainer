package freeze

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// node is one archive entry as `unsquashfs -ll` shows it.
type node struct {
	kind   byte // '-' file, 'd' directory, 'l' symlink, 'c' char device (a whiteout is c 0:0)
	size   int64
	target string // for a symlink
}

// archiveNodes lists an archive with each entry's type and size.
func archiveNodes(t *testing.T, sqf string) map[string]node {
	t.Helper()
	out, err := exec.Command("unsquashfs", "-ll", sqf).CombinedOutput()
	if err != nil {
		t.Fatalf("unsquashfs -ll: %v\n%s", err, out)
	}
	nodes := map[string]node{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		name, target := f[len(f)-1], ""
		if i := slices.Index(f, "->"); i > 0 {
			name, target = f[i-1], f[i+1]
		}
		p, ok := strings.CutPrefix(name, "squashfs-root/")
		if !ok {
			continue
		}
		n := node{kind: f[0][0], target: target}
		n.size, _ = strconv.ParseInt(strings.TrimSuffix(f[2], ","), 10, 64)
		nodes[p] = n
	}
	return nodes
}

// requireStacking skips when the tools a stacked pack mounts are missing.
func requireStacking(t *testing.T) {
	t.Helper()
	if _, err := FindFuseOverlayfs(); err != nil {
		t.Skipf("fuse-overlayfs not available: %v", err)
	}
	if _, err := FindSquashfuse(); err != nil {
		t.Skipf("squashfuse not available: %v", err)
	}
}

// bareFreeze freezes thin and renames the result over snapshot, which is what a
// bare `overlay freeze` does for a .img that continues from that snapshot.
func bareFreeze(t *testing.T, snapshot, thin string) {
	t.Helper()
	prepared := filepath.Join(filepath.Dir(snapshot), "next.sqf")
	if _, err := Freeze(context.Background(), Options{
		Image: thin, Target: prepared, CompressArgs: "-comp zstd", Snapshot: snapshot,
	}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := os.Rename(prepared, snapshot); err != nil {
		t.Fatal(err)
	}
}

// The routine snapshot loop: a thin .img holds only what changed after the
// snapshot beneath it, and a bare freeze packs it back over that snapshot. The
// result must be the two layered, exactly as they mount: snapshot underneath,
// .img on top.
func TestFreezingAThinImageOverItsSnapshotKeepsTheLayering(t *testing.T) {
	requireSquashfs(t)
	requireStacking(t)
	t.Parallel()

	// The snapshot: files to keep, edit and delete, directories to delete and to
	// replace, and two whiteouts of base files.
	first := newImage(t, `cd upper
mkdir cnt_env
mkdir usr
cd cnt_env
mkdir bin
mkdir lib
mkdir old
mkdir opq
mkdir swap
cd /upper/usr
mkdir bin
cd /upper/cnt_env/bin
symlink lnkeep keep
symlink lnedit keep`)
	for dir, files := range map[string]map[string]string{
		"/upper/cnt_env/bin":  {"keep": "v1\n", "edit": "v1\n", "gone": "v1\n"},
		"/upper/cnt_env/swap": {"s1": "s\n"},
		"/upper/cnt_env":      {"swap2": "was a file\n"},
		"/upper/cnt_env/lib":  {"a": "a\n", "b": "b\n"},
		"/upper/cnt_env/old":  {"x": "x\n"},
		"/upper/cnt_env/opq":  {"old1": "1\n", "old2": "2\n"},
		"/upper/usr/bin":      {".wh.basefile1": "", ".wh.basefile2": ""},
	} {
		for name, content := range files {
			writeInto(t, first, dir, name, content)
		}
	}
	snapshot := freezeImage(t, first).Path

	// The edits, all in the .img's own layer.
	thin := newImage(t, `cd upper
mkdir cnt_env
mkdir usr
cd cnt_env
mkdir bin
mkdir opq
mkdir swap2
cd /upper/usr
mkdir bin
cd /upper/cnt_env/bin
symlink lnedit edit`)
	for dir, files := range map[string]map[string]string{
		"/upper/cnt_env/bin":   {"new": "new\n", "edit": "version-two\n", ".wh.gone": ""},
		"/upper/cnt_env":       {".wh.old": "", "swap": "now a file\n"},
		"/upper/cnt_env/swap2": {".wh..wh..opq": "", "n": "n\n"},
		"/upper/cnt_env/opq":   {".wh..wh..opq": "", "new1": "n\n"},
		"/upper/usr/bin":       {"basefile2": "back\n", ".wh.basefile3": ""},
	} {
		for name, content := range files {
			writeInto(t, thin, dir, name, content)
		}
	}

	// The fixture itself: the snapshot really holds what the cases rely on.
	before := archiveNodes(t, snapshot)
	for path, kind := range map[string]byte{
		"cnt_env/bin/keep": '-', "cnt_env/lib/a": '-', "cnt_env/swap/s1": '-',
		"cnt_env/swap2": '-', "cnt_env/bin/lnkeep": 'l', "usr/bin/basefile1": 'c',
	} {
		if n, ok := before[path]; !ok || n.kind != kind {
			t.Fatalf("fixture: snapshot %s = %+v, want kind %q", path, n, kind)
		}
	}

	bareFreeze(t, snapshot, thin)
	got := archiveNodes(t, snapshot)

	// The manifest records the tool that layered the two.
	if v := readManifest(t, snapshot).Build.Tools.FuseOverlayfs.Version; v == "" || v == "unrecorded" {
		t.Errorf("fuse-overlayfs version = %q, want the tool's banner line", v)
	}

	// Layered result: what each path must be after the freeze. 'x' means not
	// visible: absent, or a whiteout. A deletion stays a whiteout, since the base
	// may hold the same path and the snapshot alone cannot say.
	type want struct {
		kind   byte   // '-', 'd', 'c', 'l', or 'x'
		size   int64  // checked for regular files when > 0
		target string // checked for symlinks
	}
	cases := map[string]want{
		"cnt_env/bin/keep":   {'-', 3, ""},  // only in the snapshot: kept
		"cnt_env/bin/new":    {'-', 4, ""},  // only in the .img: added
		"cnt_env/bin/edit":   {'-', 12, ""}, // in both: the .img's copy wins
		"cnt_env/bin/gone":   {'c', 0, ""},  // deleted in the .img
		"cnt_env/lib/a":      {'-', 2, ""},  // untouched directory keeps its files
		"cnt_env/lib/b":      {'-', 2, ""},
		"cnt_env/old":        {'c', 0, ""}, // directory deleted in the .img
		"cnt_env/old/x":      {'x', 0, ""},
		"cnt_env/opq/new1":   {'-', 2, ""}, // opaque in the .img: only its own files
		"cnt_env/opq/old1":   {'x', 0, ""},
		"cnt_env/opq/old2":   {'x', 0, ""},
		"cnt_env/swap":       {'-', 11, ""}, // snapshot directory replaced by a file
		"cnt_env/swap/s1":    {'x', 0, ""},
		"cnt_env/swap2":      {'d', 0, ""}, // snapshot file replaced by a directory
		"cnt_env/swap2/n":    {'-', 2, ""},
		"cnt_env/bin/lnkeep": {'l', 0, "keep"}, // symlink only in the snapshot: kept
		"cnt_env/bin/lnedit": {'l', 0, "edit"}, // symlink in both: the .img's target wins
		"usr/bin/basefile1":  {'c', 0, ""},     // the snapshot's whiteout of a base file stays
		"usr/bin/basefile2":  {'-', 5, ""},     // recreated in the .img: a file, not a whiteout
		"usr/bin/basefile3":  {'c', 0, ""},     // a new whiteout of a base file
	}
	for path, w := range cases {
		n, present := got[path]
		switch {
		case w.kind == 'x':
			if present && n.kind != 'c' {
				t.Errorf("%s: still visible as kind %q", path, n.kind)
			}
		case !present:
			t.Errorf("%s: missing, want kind %q", path, w.kind)
		case n.kind != w.kind:
			t.Errorf("%s: kind %q, want %q", path, n.kind, w.kind)
		case w.kind == '-' && w.size > 0 && n.size != w.size:
			t.Errorf("%s: size %d, want %d", path, n.size, w.size)
		case w.kind == 'l' && n.target != w.target:
			t.Errorf("%s: links to %q, want %q", path, n.target, w.target)
		}
	}
}
