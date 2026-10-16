package libexec

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// bundleAPI is where the bundle releases are described, replaceable by tests.
var bundleAPI = "https://api.github.com/repos/condatainer/libexec/releases"

// stagingName is the directory inside the toolchain where an update unpacks before installing.
// It is inside so that renaming a staged file into place never crosses a filesystem.
const stagingName = ".staging"

// release is the part of a GitHub release this package reads.
type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// manifest lists every file of an installed bundle, so a later update replaces exactly those.
type manifest struct {
	Apptainer string                   `json:"apptainer"`
	Files     map[string]manifestEntry `json:"files"`
}

type manifestEntry struct {
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
}

// bundleArch is the architecture name in the bundle's archive names.
func bundleArch() (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("the self-provisioned toolchain is only available on linux (GOOS=%s)", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	}
	return "", fmt.Errorf("no apptainer bundle for linux/%s", runtime.GOARCH)
}

// installBundle downloads the bundle for this architecture, checks it and
// installs it into target. version is empty for the newest release, or a full or partial version.
//   - The archive is checked against its published sha256 before anything is unpacked.
//   - It is unpacked into target/.staging and checked against the version floors before any installed file changes.
//   - Files of the previous bundle that the new one lacks are removed, and every other file in target is left alone.
func installBundle(ctx context.Context, target, version string) error {
	arch, err := bundleArch()
	if err != nil {
		return err
	}
	rel, err := fetchRelease(ctx, version)
	if err != nil {
		return err
	}
	archive := fmt.Sprintf("libexec-%s-linux-%s.tar.gz", strings.TrimPrefix(rel.Tag, "v"), arch)
	archiveURL, sumURL := rel.assetURL(archive), rel.assetURL(archive+".sha256")
	if archiveURL == "" || sumURL == "" {
		return fmt.Errorf("release %s of the apptainer bundle has no %s", rel.Tag, archive)
	}

	staging := filepath.Join(target, stagingName)
	if err := utils.RemoveAllWritable(staging); err != nil {
		return fmt.Errorf("failed to clear %s: %w", staging, err)
	}
	defer utils.RemoveAllWritable(staging) //nolint:errcheck
	if err := utils.MkdirAllShared(staging); err != nil {
		return notWritable(target, err)
	}

	archivePath := filepath.Join(staging, archive)
	sumPath := archivePath + ".sha256"
	if err := utils.DownloadFile(ctx, archiveURL, archivePath); err != nil {
		return fmt.Errorf("failed to download %s: %w", archive, err)
	}
	if err := utils.DownloadFile(ctx, sumURL, sumPath); err != nil {
		return fmt.Errorf("failed to download %s.sha256: %w", archive, err)
	}
	if err := verifyChecksum(archivePath, sumPath); err != nil {
		return err
	}

	tree := filepath.Join(staging, "tree")
	if err := extractArchive(archivePath, tree); err != nil {
		return fmt.Errorf("failed to unpack %s: %w", archive, err)
	}
	next, err := readManifest(tree)
	if err != nil {
		return fmt.Errorf("the bundle has no usable %s: %w", manifestName, err)
	}
	for _, name := range []string{"apptainer", "mksquashfs", "unsquashfs"} {
		if err := CheckFloor(ctx, filepath.Join(tree, "bin", name), name); err != nil {
			return fmt.Errorf("the bundle %s failed its check: %w", rel.Tag, err)
		}
	}

	previous, _ := readManifest(target)
	return replaceFiles(target, tree, previous, next)
}

// assetURL returns the download URL of the release asset called name, or "".
func (r release) assetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// fetchRelease reads the description of the bundle release: the newest for an empty version,
// else the newest whose version matches.
func fetchRelease(ctx context.Context, version string) (release, error) {
	if version == "" {
		var rel release
		if err := getJSON(ctx, bundleAPI+"/latest", &rel); err != nil {
			return release{}, err
		}
		if rel.Tag == "" {
			return release{}, errors.New("the release description has no tag")
		}
		return rel, nil
	}
	rels, err := listReleases(ctx, bundleAPI)
	if err != nil {
		return release{}, err
	}
	tags := make([]string, len(rels))
	for i, r := range rels {
		tags[i] = r.Tag
	}
	tag, err := pickTag(tags, version)
	if err != nil {
		return release{}, fmt.Errorf("apptainer bundle: %w", err)
	}
	return rels[slices.Index(tags, tag)], nil
}

// verifyChecksum checks archive against the first field of the sha256 file beside it.
func verifyChecksum(archive, sumFile string) error {
	data, err := os.ReadFile(sumFile)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return fmt.Errorf("%s is empty", filepath.Base(sumFile))
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, fields[0]) {
		return fmt.Errorf("the download of %s is corrupt: sha256 %s, expected %s", filepath.Base(archive), got, fields[0])
	}
	return nil
}

// extractArchive unpacks a gzipped tar into dest.
//   - Only directories, regular files and relative symlinks that stay inside dest are accepted.
//   - Files get mode 755 when the archive marks them executable and 644 otherwise, whatever the umask.
func extractArchive(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	if err := utils.MkdirAllShared(dest); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if name == "." {
			continue
		}
		if !inside(name) {
			return fmt.Errorf("unsafe path %q in the archive", hdr.Name)
		}
		out := filepath.Join(dest, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = utils.MkdirAllShared(out)
		case tar.TypeReg:
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			err = writeFile(out, tr, mode)
		case tar.TypeSymlink:
			if path.IsAbs(hdr.Linkname) || !inside(path.Join(path.Dir(name), hdr.Linkname)) {
				return fmt.Errorf("symlink %q points outside the archive", hdr.Name)
			}
			if err = utils.MkdirAllShared(filepath.Dir(out)); err == nil {
				err = os.Symlink(hdr.Linkname, out)
			}
		default:
			return fmt.Errorf("unsupported entry %q in the archive", hdr.Name)
		}
		if err != nil {
			return err
		}
	}
}

// inside reports whether the cleaned slash path stays below its root.
func inside(p string) bool {
	return p != ".." && !strings.HasPrefix(p, "../") && !path.IsAbs(p)
}

// writeFile creates out from r with exactly mode.
func writeFile(out string, r io.Reader, mode os.FileMode) error {
	if err := utils.MkdirAllShared(filepath.Dir(out)); err != nil {
		return err
	}
	f, err := utils.CreateFileWritable(out)
	if err != nil {
		return err
	}
	// A shared filesystem reports a failed write at close, so its error counts.
	_, err = io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Chmod(out, mode)
}

// makeDir creates dir with mode 755, whatever the umask, and the group bits of its parent. An existing directory is left as it is.
func makeDir(dir string) error {
	if utils.DirExists(dir) {
		return nil
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	utils.ShareWithParentGroup(dir)
	return nil
}

// readManifest reads dir's MANIFEST.json.
func readManifest(dir string) (manifest, error) {
	var m manifest
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	return m, nil
}

// replaceFiles moves the unpacked tree into target.
//   - Each file and new directory also gets the group bits of the directory it lands in, as every other install does.
//   - Files that the previous bundle had and the new one lacks are removed first.
//   - Each file is renamed over its old one, so a running process keeps the old inode.
//   - The manifest goes last: a failure before it leaves the previous manifest, and running the update again repairs the install.
func replaceFiles(target, tree string, previous, next manifest) error {
	for name := range previous.Files {
		if _, kept := next.Files[name]; !kept {
			os.Remove(filepath.Join(target, filepath.FromSlash(name))) //nolint:errcheck // already gone is fine
		}
	}
	err := filepath.WalkDir(tree, func(src string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tree, src)
		if err != nil || rel == "." || rel == manifestName {
			return err
		}
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return makeDir(dst)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(src)
			if err != nil {
				return err
			}
			tmp := dst + ".new"
			os.Remove(tmp) //nolint:errcheck
			if err := os.Symlink(link, tmp); err != nil {
				return err
			}
			return os.Rename(tmp, dst)
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		utils.ShareWithParentGroup(dst)
		return nil
	})
	if err != nil {
		return err
	}
	return os.Rename(filepath.Join(tree, manifestName), filepath.Join(target, manifestName))
}
