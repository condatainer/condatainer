package libexec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// fakeFile is one entry of a test archive: a regular file, or a symlink when link is set.
type fakeFile struct {
	body string
	exec bool
	link string
}

// fakeBundle builds a bundle archive for version, with the files the install checks and any extra.
func fakeBundle(t *testing.T, version string, extra map[string]fakeFile) map[string]fakeFile {
	t.Helper()
	files := map[string]fakeFile{
		"bin/apptainer":                {body: "#!/bin/sh\necho apptainer version " + version + "\n", exec: true},
		"bin/mksquashfs":               {body: "#!/bin/sh\necho mksquashfs version 4.7.5\n", exec: true},
		"bin/unsquashfs":               {link: "../lib/apptainer/bin/unsquashfs"},
		"lib/apptainer/bin/unsquashfs": {body: "#!/bin/sh\necho unsquashfs version 4.7.5\n", exec: true},
		"etc/apptainer/apptainer.conf": {body: "# conf\n"},
	}
	for k, v := range extra {
		files[k] = v
	}
	m := manifest{Apptainer: version, Files: map[string]manifestEntry{}}
	for name, f := range files {
		if f.link != "" {
			m.Files[name] = manifestEntry{Link: f.link}
		} else {
			sum := sha256.Sum256([]byte(f.body))
			m.Files[name] = manifestEntry{SHA256: hex.EncodeToString(sum[:])}
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files[manifestName] = fakeFile{body: string(data)}
	return files
}

// tarball writes files as a gzipped tar. An empty directory entry is added for var/apptainer/mnt/session.
func tarball(t *testing.T, files map[string]fakeFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tw.WriteHeader(&tar.Header{Name: "var/apptainer/mnt/session/", Typeflag: tar.TypeDir, Mode: 0o755}))
	for name, f := range files {
		switch {
		case f.link != "":
			must(tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: f.link, Mode: 0o777}))
		default:
			mode := int64(0o644)
			if f.exec {
				mode = 0o755
			}
			must(tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(f.body))}))
			_, err := tw.Write([]byte(f.body))
			must(err)
		}
	}
	must(tw.Close())
	must(gz.Close())
	return buf.Bytes()
}

// fakeRelease is one release the test server publishes.
type fakeRelease struct {
	version string
	archive []byte
	badSum  string // replaces the real checksum when set
}

// serveReleases starts a server that publishes rels, newest first, and points bundleAPI at it.
func serveReleases(t *testing.T, rels ...fakeRelease) {
	t.Helper()
	arch, err := bundleArch()
	if err != nil {
		t.Skip(err)
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	describe := func(r fakeRelease) string {
		name := fmt.Sprintf("libexec-%s-linux-%s.tar.gz", r.version, arch)
		return fmt.Sprintf(`{"tag_name":"v%s","assets":[{"name":%q,"browser_download_url":"%s/a/%s"},{"name":%q,"browser_download_url":"%s/s/%s"}]}`,
			r.version, name, srv.URL, r.version, name+".sha256", srv.URL, r.version)
	}
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, describe(rels[0])) })
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		var all []string
		for _, rel := range rels {
			all = append(all, describe(rel))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(all, ","))
	})
	for _, rel := range rels {
		sum := sha256.Sum256(rel.archive)
		hexSum := hex.EncodeToString(sum[:])
		if rel.badSum != "" {
			hexSum = rel.badSum
		}
		mux.HandleFunc("/a/"+rel.version, func(w http.ResponseWriter, r *http.Request) { w.Write(rel.archive) })
		mux.HandleFunc("/s/"+rel.version, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "%s  archive\n", hexSum) })
	}
	old := bundleAPI
	bundleAPI = srv.URL + "/releases"
	t.Cleanup(func() { bundleAPI = old })
}

// serveRelease publishes one release. A checksum of badSum, when set, replaces the real one.
func serveRelease(t *testing.T, version string, archive []byte, badSum string) {
	t.Helper()
	serveReleases(t, fakeRelease{version: version, archive: archive, badSum: badSum})
}

func TestUpdateInstallsTheBundle(t *testing.T) {
	dir := withLibexecDir(t)
	serveRelease(t, "1.5.4", tarball(t, fakeBundle(t, "1.5.4", nil)), "")

	if err := Update(context.Background(), PackageApptainer); err != nil {
		t.Fatalf("Update(apptainer): %v", err)
	}

	if path, ok := ApptainerPath(); !ok || path != filepath.Join(dir, "bin", "apptainer") {
		t.Errorf("ApptainerPath() = %s, %v", path, ok)
	}
	if info, err := os.Stat(filepath.Join(dir, "bin", "apptainer")); err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("bin/apptainer is not executable: %v, %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(dir, "bin", "unsquashfs")); err != nil || link != "../lib/apptainer/bin/unsquashfs" {
		t.Errorf("bin/unsquashfs link = %q, %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "var", "apptainer", "mnt", "session")); err != nil {
		t.Errorf("the empty session directory was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, stagingName)); !os.IsNotExist(err) {
		t.Errorf("the staging directory was left behind: %v", err)
	}
	if m, err := readManifest(dir); err != nil || m.Apptainer != "1.5.4" {
		t.Errorf("manifest = %+v, %v", m, err)
	}
}

// A later bundle replaces the files of the earlier one, drops those it no longer has,
// and leaves a file that was never part of a bundle.
func TestUpdateReplacesThePreviousBundle(t *testing.T) {
	dir := withLibexecDir(t)
	serveRelease(t, "1.5.4", tarball(t, fakeBundle(t, "1.5.4", map[string]fakeFile{"lib/old-only": {body: "old"}})), "")
	if err := Update(context.Background(), PackageApptainer); err != nil {
		t.Fatalf("first update: %v", err)
	}
	mine := filepath.Join(dir, "bin", "micromamba")
	if err := os.WriteFile(mine, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	serveRelease(t, "1.5.5", tarball(t, fakeBundle(t, "1.5.5", nil)), "")
	if err := Update(context.Background(), PackageApptainer); err != nil {
		t.Fatalf("second update: %v", err)
	}

	out, _ := os.ReadFile(filepath.Join(dir, "bin", "apptainer"))
	if !strings.Contains(string(out), "1.5.5") {
		t.Errorf("bin/apptainer was not replaced: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib", "old-only")); !os.IsNotExist(err) {
		t.Errorf("a file the new bundle lacks was kept: %v", err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Errorf("a file outside the bundle was removed: %v", err)
	}
}

func TestUpdateRefusesACorruptArchive(t *testing.T) {
	dir := withLibexecDir(t)
	serveRelease(t, "1.5.4", tarball(t, fakeBundle(t, "1.5.4", nil)), strings.Repeat("0", 64))

	err := Update(context.Background(), PackageApptainer)
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("Update() = %v, want a corrupt-download error", err)
	}
	if _, ok := Dir(); ok {
		t.Error("a failed install left a provisioned toolchain")
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "apptainer")); !os.IsNotExist(err) {
		t.Errorf("a file was installed from a corrupt archive: %v", err)
	}
}

// An archive that writes outside the tree is refused before anything is installed.
func TestUpdateRefusesAnArchiveThatEscapes(t *testing.T) {
	withLibexecDir(t)
	for name, files := range map[string]map[string]fakeFile{
		"a path with ..":      {"../escape": {body: "x"}},
		"a symlink going out": {"bin/out": {link: "../../outside"}},
		"an absolute symlink": {"bin/abs": {link: "/etc/passwd"}},
	} {
		serveRelease(t, "1.5.4", tarball(t, fakeBundle(t, "1.5.4", files)), "")
		err := Update(context.Background(), PackageApptainer)
		if err == nil {
			t.Errorf("%s: Update() accepted the archive", name)
		}
	}
}

// The bundle is refused when its apptainer is below the floor, with nothing installed.
func TestUpdateRefusesABundleBelowTheFloor(t *testing.T) {
	dir := withLibexecDir(t)
	serveRelease(t, "1.3.0", tarball(t, fakeBundle(t, "1.3.0", nil)), "")

	err := Update(context.Background(), PackageApptainer)
	if err == nil || !strings.Contains(err.Error(), "below the required") {
		t.Fatalf("Update() = %v, want a floor error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "apptainer")); !os.IsNotExist(err) {
		t.Errorf("a bundle below the floor was installed: %v", err)
	}
}

// serveMicromamba publishes releases by tag, each binary printing its tag, and points the micromamba URLs at the server.
// A tag in badSum gets a wrong checksum.
func serveMicromamba(t *testing.T, tags []string, badSum string) {
	t.Helper()
	asset, err := micromambaAssetName()
	if err != nil {
		t.Skip(err)
	}
	body := func(tag string) string { return "#!/bin/sh\necho micromamba " + tag + "\n" }
	sum := func(tag string) string {
		if tag == badSum {
			return strings.Repeat("0", 64)
		}
		h := sha256.Sum256([]byte(body(tag)))
		return hex.EncodeToString(h[:])
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var list []string
	for _, tag := range tags {
		list = append(list, fmt.Sprintf(`{"tag_name":%q}`, tag))
		mux.HandleFunc("/dl/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body(tag)) })
		mux.HandleFunc("/dl/download/"+tag+"/"+asset+".sha256", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, sum(tag)) })
	}
	newest := tags[0]
	mux.HandleFunc("/dl/latest/download/"+asset, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body(newest)) })
	mux.HandleFunc("/dl/latest/download/"+asset+".sha256", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, sum(newest)) })
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "[%s]", strings.Join(list, ",")) })
	oldAPI, oldDL := micromambaAPI, micromambaDownloads
	micromambaAPI, micromambaDownloads = srv.URL+"/api", srv.URL+"/dl"
	t.Cleanup(func() { micromambaAPI, micromambaDownloads = oldAPI, oldDL })
}

func micromambaSays(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "bin", "micromamba"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), "#!/bin/sh\necho micromamba "))
}

func TestUpdateInstallsMicromamba(t *testing.T) {
	dir := withLibexecDir(t)
	serveMicromamba(t, []string{"2.1.0-0", "2.0.5-1", "2.0.5-0"}, "")

	if err := Update(context.Background(), PackageMicromamba); err != nil {
		t.Fatalf("Update(micromamba): %v", err)
	}
	if path, ok := MicromambaPath(); !ok || path != filepath.Join(dir, "bin", "micromamba") {
		t.Errorf("MicromambaPath() = %s, %v", path, ok)
	}
	if got := micromambaSays(t, dir); got != "2.1.0-0" {
		t.Errorf("installed %s, want the newest 2.1.0-0", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "bin")); len(entries) != 1 {
		t.Errorf("bin/ holds %d entries after the update, want only micromamba", len(entries))
	}
}

func TestUpdateChoosesAMicromambaVersion(t *testing.T) {
	dir := withLibexecDir(t)
	serveMicromamba(t, []string{"2.1.0-0", "2.0.5-1", "2.0.5-0", "2.0.4-0"}, "")

	for spec, want := range map[string]string{
		"micromamba@2.0":   "2.0.5-1", // the newest 2.0.x, and its latest build
		"micromamba@2.0.4": "2.0.4-0",
		"micromamba@2":     "2.1.0-0",
	} {
		if err := Update(context.Background(), spec); err != nil {
			t.Fatalf("Update(%s): %v", spec, err)
		}
		if got := micromambaSays(t, dir); got != want {
			t.Errorf("%s installed %s, want %s", spec, got, want)
		}
	}
	if err := Update(context.Background(), "micromamba@3"); err == nil || !strings.Contains(err.Error(), "no release matches") {
		t.Errorf("Update(micromamba@3) = %v, want no release matches", err)
	}
}

// A wrong checksum leaves the installed micromamba as it was.
func TestUpdateKeepsMicromambaWhenTheChecksumFails(t *testing.T) {
	dir := withLibexecDir(t)
	serveMicromamba(t, []string{"2.0.5-0"}, "")
	if err := Update(context.Background(), PackageMicromamba); err != nil {
		t.Fatal(err)
	}
	serveMicromamba(t, []string{"2.1.0-0", "2.0.5-0"}, "2.1.0-0")

	err := Update(context.Background(), PackageMicromamba)
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("Update() = %v, want a corrupt-download error", err)
	}
	if got := micromambaSays(t, dir); got != "2.0.5-0" {
		t.Errorf("micromamba is %s after a failed update, want 2.0.5-0", got)
	}
}

func TestUpdateChoosesABundleVersion(t *testing.T) {
	releases := []fakeRelease{}
	for _, v := range []string{"1.50.0", "1.5.5", "1.5.4", "1.4.9"} {
		releases = append(releases, fakeRelease{version: v, archive: tarball(t, fakeBundle(t, v, nil))})
	}
	for spec, want := range map[string]string{
		"apptainer":       "1.50.0",
		"apptainer@1.5":   "1.5.5", // 1.5 does not match 1.50.0
		"apptainer@1.5.4": "1.5.4",
		"apptainer@1.4":   "1.4.9",
	} {
		dir := withLibexecDir(t)
		serveReleases(t, releases...)
		if err := Update(context.Background(), spec); err != nil {
			t.Fatalf("Update(%s): %v", spec, err)
		}
		if m, err := readManifest(dir); err != nil || m.Apptainer != want {
			t.Errorf("%s installed %q, want %s (%v)", spec, m.Apptainer, want, err)
		}
	}
	withLibexecDir(t)
	serveReleases(t, releases...)
	if err := Update(context.Background(), "apptainer@2"); err == nil || !strings.Contains(err.Error(), "no release matches") {
		t.Errorf("Update(apptainer@2) = %v, want no release matches", err)
	}
}

func TestParseSpecs(t *testing.T) {
	got, err := parseSpecs([]string{"apptainer@1.5", "micromamba"})
	want := []spec{{PackageMicromamba, ""}, {PackageApptainer, "1.5"}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("parseSpecs = %v, %v; want %v", got, err, want)
	}
	if all, _ := parseSpecs(nil); len(all) != 2 {
		t.Errorf("parseSpecs(nil) = %v, want both packages", all)
	}
	for _, bad := range [][]string{{"apptainer@"}, {"apptainer@latest"}, {"apptainer@v1.5"}, {"squashfs-tools"}, {"apptainer", "apptainer@1.5"}} {
		if _, err := parseSpecs(bad); err == nil {
			t.Errorf("parseSpecs(%v) accepted it", bad)
		}
	}
}

// Installed files have fixed modes whatever the umask, and take the group-write bit of the directory they land in.
func TestUpdateSetsModesAndFollowsTheGroupBit(t *testing.T) {
	defer syscall.Umask(syscall.Umask(0o077))

	for name, tc := range map[string]struct {
		dirMode            os.FileMode
		wantFile, wantExec os.FileMode
	}{
		"a private directory":        {0o755, 0o644, 0o755},
		"a group-writable directory": {0o2775, 0o664, 0o775},
	} {
		dir := withLibexecDir(t)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, tc.dirMode); err != nil {
			t.Fatal(err)
		}
		serveRelease(t, "1.5.4", tarball(t, fakeBundle(t, "1.5.4", nil)), "")
		if err := Update(context.Background(), PackageApptainer); err != nil {
			t.Fatalf("%s: Update: %v", name, err)
		}
		for path, want := range map[string]os.FileMode{
			"etc/apptainer/apptainer.conf": tc.wantFile,
			"bin/apptainer":                tc.wantExec,
		} {
			info, err := os.Stat(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Errorf("%s: %s has mode %o, want %o", name, path, got, want)
			}
		}
		if info, err := os.Stat(filepath.Join(dir, "var", "apptainer", "mnt", "session")); err != nil || info.Mode().Perm()&0o055 != 0o055 {
			t.Errorf("%s: the session directory is not readable by others: %v, %v", name, info, err)
		}
	}
}
