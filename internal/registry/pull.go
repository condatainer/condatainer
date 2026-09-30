package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/condatainer/condatainer/internal/artifact/compare"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/producer"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/utils"
)

// Pull downloads the artifact at desc and installs it at destPath.
//
//   - desc and annotations come from [ResolveArtifact]; the caller asks [Check] first.
//   - It verifies the payload is an overlay whose bytes regenerate the advertised keys.
//   - It installs flat, like a local build, with no store entry, and displaces nothing.
func Pull(ctx context.Context, base, repo string, desc ocispec.Descriptor, annotations map[string]string, destPath string) error {
	guard, err := producer.AcquireLocal(destPath)
	if err != nil {
		return err
	}
	defer guard.Release() //nolint:errcheck
	return pull(ctx, base, repo, desc, annotations, destPath)
}

// PullLocked performs Pull while the caller already holds destPath's producer
// lock. Build uses this after deriving the expected equivalence under its lock;
// callers without a lock use Pull.
func PullLocked(ctx context.Context, base, repo string, desc ocispec.Descriptor, annotations map[string]string, destPath string) error {
	return pull(ctx, base, repo, desc, annotations, destPath)
}

func pull(ctx context.Context, base, repo string, desc ocispec.Descriptor, annotations map[string]string, destPath string) error {
	log := logging.FromContext(ctx)

	if err := checkDistributable(destPath); err != nil {
		return err
	}
	repository, err := newRepository(base, repo)
	if err != nil {
		return err
	}
	manifest, err := fetchManifest(ctx, repository, desc)
	if err != nil {
		return classify(err)
	}
	if err := validatePayloadContract(manifest); err != nil {
		return err
	}
	// Protection is a stable fact — the write bit — so it is worth knowing before
	// a multi-gigabyte download rather than after. A conflicting lock is not:
	// it may well be gone by the time the transfer finishes, so it is left to the
	// probe below and not treated as a reason to refuse now.
	destDir := filepath.Dir(destPath)
	if utils.FileExists(destPath) {
		if err := image.CheckAvailable(destPath, true); errors.Is(err, image.ErrProtected) {
			return err
		}
	}
	if err := utils.MkdirAllShared(destDir); err != nil {
		return fmt.Errorf("cannot prepare %s: %w", destDir, err)
	}
	if err := requireFreeSpace(destDir, downloadSize(desc, manifest)); err != nil {
		return err
	}

	// Staged in the destination directory, not a temp root: rename(2) cannot
	// cross filesystems, and an install that is not one rename is an install
	// that can be seen half-done.
	stageDir, err := os.MkdirTemp(destDir, ".cnt-pull-")
	if err != nil {
		return fmt.Errorf("cannot create staging directory in %s: %w", destDir, err)
	}
	defer os.RemoveAll(stageDir) //nolint:errcheck

	staged := filepath.Join(stageDir, filepath.Base(destPath))
	if err := download(ctx, repository, manifest, staged); err != nil {
		return err
	}
	embedded, err := verifyPayload(staged, annotations)
	if err != nil {
		return err
	}
	if name := annotations[AnnTitle]; name != "" && name != embedded.Name {
		// Reported, never fatal: identity already answered whether these are the
		// wanted bytes, and what an image answers to is decided by where its file
		// sits, not by what it calls itself.
		log.Warn("Published name disagrees with the artifact's own",
			"published", name, "embedded", embedded.Name)
	}

	// Immediately before the rename, and not before the download: an exec on
	// another node may hold a shared lock and be reading the old file lazily over NFS,
	// where replacing it stales the handle mid-job. A lock taken earlier would
	// block every exec for the length of the transfer and would then be held on
	// an orphaned inode anyway.
	if utils.FileExists(destPath) {
		if err := image.CheckAvailable(destPath, true); err != nil {
			return fmt.Errorf("cannot replace %s: %w", destPath, err)
		}
	}
	if err := os.Rename(staged, destPath); err != nil {
		return fmt.Errorf("failed to install pulled artifact: %w", err)
	}
	utils.ShareWithParentGroup(destPath)
	log.Info("Installed", "artifact", destPath)
	return nil
}

// Fetch downloads the artifact desc addresses to destPath and verifies it,
// installing nothing.
//
//   - It is Pull without the name handling: no producer lock, name search, protection check or rename.
//   - The caller owns destPath, which must not exist.
//   - It still checks the manifest is an overlay and the payload regenerates its keys.
func Fetch(ctx context.Context, base, repo string, desc ocispec.Descriptor, annotations map[string]string, destPath string) error {
	repository, err := newRepository(base, repo)
	if err != nil {
		return err
	}
	manifest, err := fetchManifest(ctx, repository, desc)
	if err != nil {
		return classify(err)
	}
	if err := validatePayloadContract(manifest); err != nil {
		return err
	}
	if err := requireFreeSpace(filepath.Dir(destPath), downloadSize(desc, manifest)); err != nil {
		return err
	}
	if err := download(ctx, repository, manifest, destPath); err != nil {
		return err
	}
	if _, err := verifyPayload(destPath, annotations); err != nil {
		return err
	}
	return nil
}

// SplitCoordinate separates a repository coordinate such as
// "ghcr.io/org/cnt/grch38/genome" into registry base and repository path.
// It splits at the first slash and does not trim a leading one:
// "/lab/p" names no registry.
func SplitCoordinate(coordinate string) (base, repo string, err error) {
	// Trailing slashes only: TrimBaseScheme drops those. A leading one is not
	// tidied away, because "/lab/p" names no registry and reading it as host
	// "lab" would turn a malformed coordinate into a plausible one.
	host, path, found := strings.Cut(TrimBaseScheme(coordinate), "/")
	if !found || host == "" || path == "" {
		return "", "", fmt.Errorf("%q is not a registry/repository coordinate", coordinate)
	}
	return host, path, nil
}

// checkDistributable rejects a path that names something no registry moves. It
// says what a manifest cannot: a writable overlay has no identity at all, and a
// file that is not an image was never a candidate.
func checkDistributable(path string) error {
	switch {
	case utils.IsSqf(path):
		return nil
	case utils.IsImg(path):
		return fmt.Errorf("%s is a writable overlay, which has no identity and is never distributed", path)
	}
	return fmt.Errorf("%s is not a distributable image (.sqf)", path)
}

// validatePayloadContract rejects a manifest that does not describe an overlay,
// before any of its bytes are fetched.
func validatePayloadContract(manifest ocispec.Manifest) error {
	if manifest.ArtifactType != ArtifactTypeOverlay {
		return fmt.Errorf("%w: it is a %q, not a %q",
			ErrInvalidArtifact, manifest.ArtifactType, ArtifactTypeOverlay)
	}
	if len(manifest.Layers) == 0 {
		return fmt.Errorf("%w: it carries no payload", ErrInvalidArtifact)
	}
	for i, layer := range manifest.Layers {
		if layer.MediaType != MediaTypeOverlayBlob {
			return fmt.Errorf("%w: payload %d is %q, not %q",
				ErrInvalidArtifact, i, layer.MediaType, MediaTypeOverlayBlob)
		}
	}
	return nil
}

// download copies the resolved manifest and its blobs into stageDir. It is
// addressed by digest on both sides, so no tag is resolved a second time and the
// artifact verified above is the one that arrives.
func download(ctx context.Context, repository *remote.Repository, manifest ocispec.Manifest, destPath string) error {
	var total int64
	offsets := make([]int64, len(manifest.Layers))
	for i, layer := range manifest.Layers {
		offsets[i] = total
		total += positiveSize(layer.Size)
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", destPath, err)
	}
	// A half-written artifact must not survive to be installed. The staging
	// directory is removed by the caller either way; closing early is what makes
	// the error path deterministic rather than dependent on the defer order.
	closed := false
	defer func() {
		if !closed {
			f.Close() //nolint:errcheck
		}
	}()
	// Sized up front so every layer writes into a range that already exists,
	// which is what lets them be written in any order.
	if err := f.Truncate(total); err != nil {
		return fmt.Errorf("cannot size %s: %w", destPath, err)
	}

	progress := newDownloadProgress(ctx, total)
	blobs := repository.Blobs()
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(pullConcurrency)
	for i, layer := range manifest.Layers {
		i, layer := i, layer
		group.Go(func() error {
			if err := fetchLayerAt(groupCtx, blobs, layer, f, offsets[i], progress); err != nil {
				return fmt.Errorf("failed to download payload %d of %d: %w",
					i+1, len(manifest.Layers), classify(err))
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		if errors.Is(err, context.Canceled) {
			progress.interrupted()
		}
		return err
	}
	progress.finish()

	closed = true
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot finish writing %s: %w", destPath, err)
	}
	return nil
}

// fetchLayerAt streams one layer into its own byte range of the destination, so
// no staged copy is needed and a retry simply overwrites the range.
func fetchLayerAt(ctx context.Context, blobs registry.BlobStore, layer ocispec.Descriptor, f *os.File, offset int64, progress *downloadProgress) error {
	return retryPolicyFrom(ctx).run(ctx, mutation{
		verb:  verbDownload,
		attrs: []any{"blob", layer.Digest.String()},
		do: func(ctx context.Context) error {
			reader, err := blobs.Fetch(ctx, layer)
			if err != nil {
				return err
			}
			defer reader.Close() //nolint:errcheck

			// ORAS checks Content-Length and the Docker-Content-Digest header on
			// a fetch but never hashes the body — oras.Copy wraps the verifier,
			// and this path does not use it. Without this a registry could serve
			// anything of the right length.
			verifier := content.NewVerifyReader(reader, layer)
			// Rebuilt per attempt: a retried layer rewrites its range from the
			// start, so an abandoned attempt's bytes must leave the total.
			counted := progress.attempt(verifier)
			if _, err := io.Copy(io.NewOffsetWriter(f, offset), counted); err != nil {
				return err
			}
			return verifier.Verify()
		},
	})
}

// pullConcurrency is how many blobs a pull fetches at once.
//
// Stated rather than inherited from ORAS's identical default: a pull runs once
// per node per artifact, so across a cluster it is the larger of the two
// directions and worth deciding on purpose. Three, because a pull is on the path
// of every job needing the artifact and a rate limit now pauses it rather than
// failing it.
//
// It costs nothing in scratch: every layer writes to its own range, so the peak
// is the artifact's size whatever the order.
const pullConcurrency = 3

// verifyPayload holds the downloaded artifact to what its annotations promised
// and returns what the payload says about itself.
//
//   - Keys are regenerated from the payload's files by [compare.Read], never read off its manifest.
//   - That catches a publisher whose annotations and bytes disagree.
//   - It does not prove the bytes are the publisher's; that needs signatures.
func verifyPayload(path string, annotations map[string]string) (compare.Artifact, error) {
	got, err := compare.Read(path)
	if err != nil {
		return got, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	return got, checkRegeneratedKeys(got, Identity(annotations), Equiv(annotations), "published")
}

// checkRegeneratedKeys holds the keys an artifact's own files reproduce against
// the keys something claimed for it. claimant names where that claim came from,
// since both directions use this: push checks the artifact against its own
// recorded manifest, pull checks it against the annotations it was advertised
// under. An absent claim is not checked — only a wrong one is a failure.
func checkRegeneratedKeys(got compare.Artifact, identity, equiv meta.KeyRef, claimant string) error {
	if got.Identity == "" {
		return fmt.Errorf("%w: its payload regenerates no identity, so nothing about it can be verified",
			ErrInvalidArtifact)
	}
	for _, check := range []struct {
		what      string
		want      meta.KeyRef
		gotScheme string
		gotDigest string
	}{
		{"identity", identity, got.IdentityScheme, got.Identity},
		{"equivalence", equiv, got.EquivScheme, got.Equiv},
	} {
		if check.want.Empty() {
			continue
		}
		if check.want.Scheme != check.gotScheme || check.want.Digest() != check.gotDigest {
			return fmt.Errorf("%w: %s %s is %s %s, its payload regenerates %s %s",
				ErrInvalidArtifact, claimant, check.what,
				check.want.Scheme, check.want.Digest(), check.gotScheme, check.gotDigest)
		}
	}
	return nil
}

// downloadSize is what must fit on the destination filesystem: the manifest, the
// config, and every layer once — not twice, because each layer streams into its
// own range of the finished file rather than being staged and joined.
func downloadSize(manifestDesc ocispec.Descriptor, manifest ocispec.Manifest) int64 {
	var payload int64
	for _, layer := range manifest.Layers {
		payload += positiveSize(layer.Size)
	}
	return positiveSize(manifestDesc.Size) + positiveSize(manifest.Config.Size) + payload
}

// positiveSize treats an unset or nonsensical size as zero rather than letting it
// subtract from the total.
func positiveSize(size int64) int64 {
	if size > 0 {
		return size
	}
	return 0
}

// requireFreeSpace refuses before the download when dir cannot hold it. Bavail,
// not Bfree: the blocks reserved for root are not available to this process.
func requireFreeSpace(dir string, required int64) error {
	if required <= 0 {
		return nil
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("cannot check free space in %s: %w", dir, err)
	}
	if available := int64(stat.Bavail) * int64(stat.Bsize); available < required {
		return fmt.Errorf("not enough space in %s: need %s, %s available",
			dir, utils.FormatSize(required), utils.FormatSize(available))
	}
	return nil
}
