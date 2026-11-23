package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
)

// A frozen environment is always called env, so position is the only question
// left: where the artifact lands. These are the forms a user can type.
func TestResolveFreezeTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "env.img")
	existingDir := filepath.Join(dir, "overlays")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		label    string
		dest     string
		wantFile string
	}{
		{"nothing given: beside the source, extension changed",
			"", filepath.Join(dir, "env.sqf")},
		{"a destination places it",
			filepath.Join(dir, "frozen.sqf"), filepath.Join(dir, "frozen.sqf")},
		{"a destination without the extension",
			filepath.Join(dir, "frozen"), filepath.Join(dir, "frozen.sqf")},
		{"an existing directory is a place, not the artifact",
			existingDir, filepath.Join(existingDir, "env.sqf")},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			target, err := resolveFreezeTarget(source, tc.dest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if target != tc.wantFile {
				t.Errorf("target = %q, want %q", target, tc.wantFile)
			}
		})
	}
}

// A bare freeze targets the snapshot slot, which every .img of a name shares,
// whether or not a snapshot is there yet.
func TestResolveFreezeTargetIsTheSharedSnapshotSlot(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	source := filepath.Join(dir, "env-alice.img")
	want := filepath.Join(dir, "env.sqf")

	got, err := resolveFreezeTarget(source, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}

// Something occupying the derived path that isn't a snapshot blocks a bare
// freeze outright rather than silently replacing it.
func TestResolveFreezeTargetRefusesNonSnapshotOccupant(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	source := filepath.Join(dir, "env-alice.img")
	occupied := filepath.Join(dir, "env.sqf")
	if err := os.WriteFile(occupied, []byte("not a snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveFreezeTarget(source, ""); err == nil {
		t.Fatal("a non-snapshot occupant was silently accepted as a bare freeze target")
	}
}

// A trailing separator says "put it in this directory", and it means that
// whether or not the directory is there yet. Without that reading, a missing one
// fell through to the extension branch and produced a file named ".sqf".
func TestResolveFreezeTargetTrailingSlashIsADirectory(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "env.img")
	missing := filepath.Join(dir, "nodir") + string(filepath.Separator)

	if _, err := resolveFreezeTarget(source, missing); err == nil {
		t.Fatal("a destination in a directory that does not exist was accepted")
	}
	if err := os.MkdirAll(filepath.Join(dir, "nodir"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveFreezeTarget(source, missing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(dir, "nodir", "env.sqf"); got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

// A destination whose directory is missing is refused here, with a sentence,
// rather than by Apptainer failing to bind it after the payload has been walked.
func TestResolveFreezeTargetRefusesAMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "env.img")
	if _, err := resolveFreezeTarget(source, filepath.Join(dir, "nope", "env.sqf")); err == nil {
		t.Fatal("a destination under a missing directory was accepted")
	}
}

func TestFreezeFlagsAreBackupNotKeep(t *testing.T) {
	flags := overlayFreezeCmd.Flags()
	f := flags.Lookup("backup")
	if f == nil {
		t.Fatal("--backup is not a flag")
	}
	if f.NoOptDefVal != "" {
		t.Errorf("a bare --backup is accepted, giving %q", f.NoOptDefVal)
	}
	if flags.Lookup("keep") != nil {
		t.Error("--keep is still a flag")
	}
}

// A bare .img and a per-user .ext3, with and without a snapshot, against every
// kind of path. {dir} is the directory holding the overlay, and backup/ is a
// directory in it. A path is read from the working directory, so every value
// here names {dir}. A path with no extension takes the .img's own.
func TestPlanFreezeBackup(t *testing.T) {
	for _, lower := range []bool{false, true} {
		for _, name := range []string{"env.img", "env-alice.ext3"} {
			ext := filepath.Ext(name)
			for _, dest := range []string{"", "x.sqf"} {
				for _, tc := range []struct {
					label   string
					value   string
					given   bool
					wantImg string // relative to {dir}; "" when no backup is made
					wantSqf string // relative to {dir}, when a snapshot is kept
					refused bool
				}{
					{"no flag", "", false, "", "", false},
					{"a name", "{dir}/old", true, "old" + ext, "old.sqf", false},
					{"a name with the user's suffix", "{dir}/old-alice", true, "old-alice" + ext, "old.sqf", false},
					{"a name in a directory", "{dir}/backup/old", true, "backup/old" + ext, "backup/old.sqf", false},
					{"a name that is a directory", "{dir}/backup", true, "backup" + ext, "backup.sqf", false},
					{"the live name in a directory", "{dir}/backup/env", true, "backup/env" + ext, "backup/env.sqf", false},
					{"a folder not there yet", "{dir}/missing/old", true, "missing/old" + ext, "missing/old.sqf", false},
					{"an .img name", "{dir}/old.img", true, "old.img", "old.sqf", false},
					{"an .ext3 name", "{dir}/old.ext3", true, "old.ext3", "old.sqf", false},
					{"an extension in a directory", "{dir}/backup/old.img", true, "backup/old.img", "backup/old.sqf", false},
					{"the snapshot's own name", "{dir}/env", true, "", "", true},
					{"the user's own name for it", "{dir}/env-alice", true, "", "", true},
					{"a directory alone", "{dir}/backup/", true, "", "", true},
					{"an empty name", "", true, "", "", true},
					{"a snapshot extension", "{dir}/old.sqf", true, "", "", true},
				} {
					t.Run(strconv.FormatBool(lower)+"/"+name+"/"+dest+"/"+tc.label, func(t *testing.T) {
						dir, _, _ := envDir(t, lower)
						if err := os.Mkdir(filepath.Join(dir, "backup"), 0o755); err != nil {
							t.Fatal(err)
						}
						source := filepath.Join(dir, name)
						value := strings.ReplaceAll(tc.value, "{dir}", dir)
						plan, err := planFreezeBackup(source, dest, value, tc.given)
						if (err != nil) != tc.refused {
							t.Fatalf("err = %v, refused = %v", err, tc.refused)
						}
						if tc.refused {
							return
						}
						wantImg, wantSqf := "", ""
						if tc.wantImg != "" {
							wantImg = filepath.Join(dir, tc.wantImg)
						}
						if tc.given && lower && dest == "" {
							wantSqf = filepath.Join(dir, tc.wantSqf)
						}
						if plan.img != wantImg {
							t.Errorf("img = %q, want %q", plan.img, wantImg)
						}
						if plan.sqf != wantSqf {
							t.Errorf("sqf = %q, want %q", plan.sqf, wantSqf)
						}
						if plan.img != "" && container.PairsWith(plan.img, container.SnapshotPath(source)) {
							t.Errorf("%s pairs with the live snapshot", plan.img)
						}
						if plan.sqf != "" && !container.PairsWith(plan.img, plan.sqf) {
							t.Errorf("%s does not pair with %s", plan.img, plan.sqf)
						}
					})
				}
			}
		}
	}
}

// otherFilesystem is a directory on a different device than dir, or a skip.
func otherFilesystem(t *testing.T, dir string) string {
	t.Helper()
	shm, err := os.MkdirTemp("/dev/shm", "freeze-test-")
	if err != nil {
		t.Skip("no second filesystem to test with")
	}
	t.Cleanup(func() { os.RemoveAll(shm) })
	a, errA := os.Stat(dir)
	b, errB := os.Stat(shm)
	if errA != nil || errB != nil {
		t.Skip("cannot stat the test directories")
	}
	sa, okA := a.Sys().(*syscall.Stat_t)
	sb, okB := b.Sys().(*syscall.Stat_t)
	if !okA || !okB || sa.Dev == sb.Dev {
		t.Skip("/dev/shm is on the same filesystem")
	}
	return shm
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Keeping the old snapshot: a link on one filesystem, a copy across two, and
// never a replacement of what is there.
func TestLinkOrCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "env.sqf")
	writeFile(t, src, "snapshot")

	same := filepath.Join(dir, "old.sqf")
	if err := linkOrCopy(src, same); err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(mustStat(t, src), mustStat(t, same)) {
		t.Error("on one filesystem the backup is a copy, want a link")
	}

	shm := otherFilesystem(t, dir)
	across := filepath.Join(shm, "old.sqf")
	if err := linkOrCopy(src, across); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(across); string(got) != "snapshot" {
		t.Errorf("copy holds %q", got)
	}
	for _, dst := range []string{same, across} {
		if err := linkOrCopy(src, dst); !errors.Is(err, os.ErrExist) {
			t.Errorf("onto existing %s: err = %v, want ErrExist", dst, err)
		}
	}
}

// Moving a backup aside: a rename on one filesystem, a copy then removal across two,
// and the source stays when the copy cannot be made.
func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "env.img")
	writeFile(t, src, "image")
	near := filepath.Join(dir, "old.img")
	if err := moveFile(src, near); err != nil {
		t.Fatal(err)
	}
	if utils.FileExists(src) || !utils.FileExists(near) {
		t.Error("the rename did not move the file")
	}

	shm := otherFilesystem(t, dir)
	far := filepath.Join(shm, "old.img")
	if err := moveFile(near, far); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(far); string(got) != "image" {
		t.Errorf("moved file holds %q", got)
	}
	if utils.FileExists(near) {
		t.Error("the source was left after a cross-filesystem move")
	}

	// A copy that fails leaves the source and no partial file.
	writeFile(t, src, "image")
	missing := filepath.Join(shm, "missing", "old.img")
	if err := moveFile(src, missing); err == nil {
		t.Error("a move into a missing directory succeeded")
	}
	if !utils.FileExists(src) {
		t.Error("the source is gone after a failed move")
	}
	writeFile(t, filepath.Join(shm, "taken.img"), "other")
	if err := moveFile(src, filepath.Join(shm, "taken.img")); err == nil {
		t.Error("a move over an existing file succeeded")
	}
	if got, _ := os.ReadFile(filepath.Join(shm, "taken.img")); string(got) != "other" {
		t.Errorf("an existing file was replaced: %q", got)
	}
	if entries, _ := os.ReadDir(shm); len(entries) != 2 {
		t.Errorf("%d entries in the backup directory, want only the two backups", len(entries))
	}
}

// A backup in a directory on another filesystem is made by copying, for the
// .img with its sidecar.
func TestRetireFreezeSourceAcrossFilesystems(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "env.img")
	writeFile(t, source, "image")
	writeFile(t, source+".env", "VAR=1")
	shm := otherFilesystem(t, dir)
	plan := freezeBackupPlan{img: filepath.Join(shm, "old.img")}

	if _, err := retireFreezeSource(source, "", plan); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		source: false, source + ".env": false, plan.img: true, plan.img + ".env": true,
	} {
		if utils.FileExists(path) != want {
			t.Errorf("%s exists = %v, want %v", path, !want, want)
		}
	}
}

// Every destination and backup state against the pre-pack refusals.
func TestCheckFreezeSource(t *testing.T) {
	t.Setenv("USER", "alice")
	for _, name := range []string{"env.img", "env-alice.ext3"} {
		for _, tc := range []struct {
			label   string
			dest    string // relative to the directory; "" is a bare freeze
			backup  bool
			held    []string // files already there, relative to the directory
			sqf     bool     // the plan keeps a snapshot
			refused bool
		}{
			{"bare", "", false, nil, false, false},
			{"bare, --backup", "", true, nil, false, false},
			{"bare, no flag, a backup is there", "", false, []string{"old"}, false, false},
			{"bare, --backup, the .img backup is there", "", true, []string{"IMG"}, false, true},
			{"bare, --backup, its .env is there", "", true, []string{"IMG.env"}, false, true},
			{"bare, --backup, the snapshot backup is there", "", true, []string{"old.sqf"}, true, true},
			{"bare, --backup, snapshot kept, free", "", true, nil, true, false},
			{"its own slot", "env.sqf", false, nil, false, true},
			{"its own slot, --backup", "env.sqf", true, nil, false, false},
			{"another name", "frozen.sqf", false, nil, false, false},
			{"another directory", "sub/env.sqf", false, nil, false, false},
			{"another directory, --backup", "sub/env.sqf", true, nil, false, false},
		} {
			t.Run(name+"/"+tc.label, func(t *testing.T) {
				dir := t.TempDir()
				ext := filepath.Ext(name)
				source := filepath.Join(dir, name)
				target := container.SnapshotPath(source)
				if tc.dest != "" {
					target = filepath.Join(dir, tc.dest)
				}
				var plan freezeBackupPlan
				if tc.backup {
					plan.img = filepath.Join(dir, "old"+ext)
					if tc.sqf {
						plan.sqf = filepath.Join(dir, "old.sqf")
					}
				}
				for _, held := range tc.held {
					held = strings.Replace(held, "IMG", "old"+ext, 1)
					if err := os.WriteFile(filepath.Join(dir, held), nil, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				err := checkFreezeSource(source, target, tc.dest, plan)
				if (err != nil) != tc.refused {
					t.Errorf("err = %v, refused = %v", err, tc.refused)
				}
			})
		}
	}
}

// What is left beside the snapshot once the pack is installed, for every
// destination and flag, with and without the .env sidecar.
func TestRetireFreezeSource(t *testing.T) {
	t.Setenv("USER", "alice")
	for _, name := range []string{"env.img", "env-alice.ext3"} {
		for _, sidecar := range []bool{false, true} {
			for _, tc := range []struct {
				label       string
				dest        string
				backup      bool
				wantRemoved bool
				wantRenamed bool
			}{
				{"bare freeze removes it", "", false, true, false},
				{"bare freeze with --backup renames it", "", true, false, true},
				{"a destination leaves it", "x.sqf", false, false, false},
				{"a destination with --backup renames it", "x.sqf", true, false, true},
			} {
				t.Run(name+"/"+strconv.FormatBool(sidecar)+"/"+tc.label, func(t *testing.T) {
					dir := t.TempDir()
					source := filepath.Join(dir, name)
					write := func(path string) {
						if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					write(source)
					if sidecar {
						write(source + ".env")
					}
					var plan freezeBackupPlan
					if tc.backup {
						plan.img = filepath.Join(dir, "old"+filepath.Ext(name))
					}

					removed, err := retireFreezeSource(source, tc.dest, plan)
					if err != nil {
						t.Fatal(err)
					}
					if removed != tc.wantRemoved {
						t.Errorf("removed = %v, want %v", removed, tc.wantRemoved)
					}

					wantSource := !tc.wantRemoved && !tc.wantRenamed
					for path, want := range map[string]bool{
						source:            wantSource,
						source + ".env":   wantSource && sidecar,
						plan.img:          tc.wantRenamed,
						plan.img + ".env": tc.wantRenamed && sidecar,
					} {
						if path == "" || path == ".env" {
							continue
						}
						if utils.FileExists(path) != want {
							t.Errorf("%s exists = %v, want %v", filepath.Base(path), !want, want)
						}
					}
				})
			}
		}
	}
}
