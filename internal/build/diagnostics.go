package build

import (
	"bytes"
	"context"
	"errors"
	osexec "os/exec"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
)

const maxRecordedToolVersion = 256

// captureCommonBuildTools records the worker-side implementations shared by every build.
//   - It runs after the skip check, so an existing image is never relabelled.
//   - Apptainer is the one the build's container step ran under, kept in apptainerBin, never resolved again.
func (b *BuildObject) captureCommonBuildTools(ctx context.Context) {
	log := logging.FromContext(ctx)

	version, ok := normalizedToolVersion(config.Version)
	if !ok {
		version = meta.Unrecorded
		log.Warn("Could not record CondaTainer version", "name", b.spec.Image.Name)
	}
	b.buildTools.Condatainer = meta.Tool{Version: version}

	rawVersion, err := b.apptainerVersion()
	if err != nil {
		b.buildTools.Apptainer = meta.Tool{Name: "apptainer", Version: meta.Unrecorded}
		log.Warn("Could not identify Apptainer version", "name", b.spec.Image.Name, "err", err)
		return
	}
	if version, ok = normalizedToolVersion(rawVersion); !ok {
		version = meta.Unrecorded
		log.Warn("Could not record Apptainer version", "name", b.spec.Image.Name)
	}
	b.buildTools.Apptainer = meta.Tool{Name: "apptainer", Version: version}
}

// captureMicromambaVersion runs the self-provisioned Micromamba's own
// --version directly on host.
func (b *BuildObject) captureMicromambaVersion(ctx context.Context) {
	log := logging.FromContext(ctx)
	version := meta.Unrecorded

	mmCmd, err := micromambaCmd()
	if err == nil {
		var out bytes.Buffer
		cmd := osexec.CommandContext(ctx, mmCmd, "--version")
		cmd.Stdout = &out
		if err = cmd.Run(); err == nil {
			if captured, ok := normalizedToolVersion(out.String()); ok {
				version = captured
			} else {
				err = errEmptyToolVersion
			}
		}
	}

	b.buildTools.Micromamba = meta.Tool{Version: version}
	if err != nil {
		log.Warn("Could not record Micromamba version", "name", b.spec.Image.Name, "err", err)
	}
}

// captureMksquashfsVersion runs mksquashfsBin's own -version and records its
// first line. mksquashfsBin is already resolved by the caller (squashfs.go);
// a failure here is only ever the version parse, never the build.
func (b *BuildObject) captureMksquashfsVersion(ctx context.Context, mksquashfsBin string) {
	log := logging.FromContext(ctx)
	version := meta.Unrecorded

	var out bytes.Buffer
	cmd := osexec.CommandContext(ctx, mksquashfsBin, "-version")
	cmd.Stdout = &out
	err := cmd.Run()
	if err == nil {
		firstLine, _, _ := strings.Cut(out.String(), "\n")
		if captured, ok := normalizedToolVersion(firstLine); ok {
			version = captured
		} else {
			err = errEmptyToolVersion
		}
	}

	b.buildTools.Mksquashfs = meta.Tool{Version: version}
	if err != nil {
		log.Warn("Could not record mksquashfs version", "name", b.spec.Image.Name, "err", err)
	}
}

// normalizedToolVersion accepts one short, printable line. Tool output is
// diagnostic data, but it is still embedded metadata and must not become an
// unbounded or multiline log fragment.
func normalizedToolVersion(raw string) (string, bool) {
	version := strings.TrimSpace(raw)
	if version == "" || len(version) > maxRecordedToolVersion || strings.ContainsAny(version, "\r\n") {
		return "", false
	}
	return version, true
}

var errEmptyToolVersion = errors.New("version command returned no usable version")

// apptainerVersion is the version of the apptainer this build ran under, or an error when no container step ran.
func (b *BuildObject) apptainerVersion() (string, error) {
	if b.apptainerBin.Path == "" {
		return "", apptainer.ErrNotResolved
	}
	return b.apptainerBin.Version()
}
