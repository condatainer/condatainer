package build

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/logging"
)

// stageMetadata validates this build's metadata, writes both documents into the
// workspace, and returns the directory the packer should add as a second archive
// root. Staging is the last point invalid metadata can still stop the build.
func stageMetadata(ctx context.Context, b *BuildObject) (string, error) {
	manifest := b.Manifest()
	// Stamped here, not in Manifest(), which must stay a projection of Spec.
	// Staging is when the payload is final, so this is the build's finish time.
	created, err := buildTime()
	if err != nil {
		return "", fmt.Errorf("refusing to pack %s: %w", b.spec.Image.Name, err)
	}
	manifest.Build.Created = created
	if err := meta.ValidateManifest(manifest); err != nil {
		return "", fmt.Errorf("refusing to pack %s: %w", b.spec.Image.Name, err)
	}
	rt := b.Runtime()
	if err := meta.ValidateRuntime(rt); err != nil {
		return "", fmt.Errorf("refusing to pack %s: %w", b.spec.Image.Name, err)
	}

	// The build directory may not exist yet if the recipe never ran.
	if err := ensureWorkspaceRoot(b); err != nil {
		return "", err
	}

	dir := b.ws.MetaDir
	if err := meta.StageRuntime(dir, rt); err != nil {
		return "", err
	}
	if err := meta.StageManifest(dir, manifest); err != nil {
		return "", err
	}
	// The sources go in verbatim: a recipe as it was fetched, tokens and all, and
	// a Conda build's exports as captured. manifest.source.files names exactly
	// these, so a reader never has to probe for what is there.
	for _, file := range b.embedded {
		if err := meta.StageBytes(dir, file.Name, file.Data); err != nil {
			return "", err
		}
	}

	logging.FromContext(ctx).Debug("staged metadata",
		"name", b.spec.Image.Name, "dir", dir, "type", manifest.Type, "build_type", manifest.BuildType)
	return dir, nil
}

// EnvSourceDateEpoch sets the build time a manifest records, in Unix seconds.
const EnvSourceDateEpoch = "SOURCE_DATE_EPOCH"

// buildTime is SOURCE_DATE_EPOCH when set, so builds of one artifact on several
// machines share a date tag; otherwise now. Both are UTC.
func buildTime() (time.Time, error) {
	raw := strings.TrimSpace(os.Getenv(EnvSourceDateEpoch))
	if raw == "" {
		return time.Now().UTC(), nil
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || secs < 0 {
		return time.Time{}, fmt.Errorf("%s=%q is not a Unix time in seconds", EnvSourceDateEpoch, raw)
	}
	return time.Unix(secs, 0).UTC(), nil
}
