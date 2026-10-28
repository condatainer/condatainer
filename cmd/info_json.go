package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/conda"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/condatainer/condatainer/internal/image/sif"
	"github.com/condatainer/condatainer/internal/image/squashfs"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
)

// runInfoJSON prints the inspection document of one overlay file. With
// validate, the checks run after it is built, and a failed one fails the
// command once the document is printed.
func runInfoJSON(cmd *cobra.Command, overlayPath string, validate bool) error {
	doc := &report.Inspection{}
	var err error
	switch {
	case utils.IsSqf(overlayPath), utils.IsSif(overlayPath):
		err = describeSquashFS(overlayPath, doc)
	case utils.IsImg(overlayPath):
		err = describeExt3(overlayPath, doc)
	default:
		err = fmt.Errorf("unsupported file type: %s", filepath.Ext(overlayPath))
	}
	if err != nil {
		return err
	}

	var failed error
	if validate {
		doc.Checks = &report.Checks{}
		var keysErr, payloadErr error
		doc.Checks.Keys, _, keysErr = checkKeys(overlayPath)
		doc.Checks.Payload, _, payloadErr = checkPayload(cmd.Context(), overlayPath)
		for _, e := range []error{keysErr, payloadErr} {
			if e != nil {
				doc.Checks.Errors = append(doc.Checks.Errors, e.Error())
			}
		}
		failed = errors.Join(keysErr, payloadErr)
	}
	if err := report.Write(cmd.OutOrStdout(), doc); err != nil {
		return err
	}
	return failed
}

// describeSquashFS fills doc for a .sqf or .sif. What cannot be read is left
// out and named in the problems.
func describeSquashFS(path string, doc *report.Inspection) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	var offset int64
	if utils.IsSif(path) {
		part, err := sif.PrimarySystemPartition(path)
		if err != nil {
			return fmt.Errorf("not a readable SIF: %w", err)
		}
		offset = part.Offset
	}
	rec := &doc.Overlay
	rec.Location = plannedLocation(path, true)
	problem := func(format string, a ...any) {
		doc.Problems = append(doc.Problems, report.Problem{Message: fmt.Sprintf(format, a...)})
	}

	rt, err := meta.ReadRuntime(path)
	if err == nil {
		rec.Name, rec.Type, rec.Arch, rec.Runtime = rt.Name, rt.Type, rt.Platform.Arch, &rt
	} else if !errors.Is(err, meta.ErrNoRuntime) {
		problem("cannot read the runtime: %v", err)
	}
	if m, err := meta.ReadManifest(path); err == nil {
		rec.Manifest = &m
		if !m.Keys.Identity.Empty() || !m.Keys.Equiv.Empty() || !m.Keys.Payload.Empty() {
			rec.Keys = &m.Keys
		}
	} else if !errors.Is(err, meta.ErrNoManifest) {
		problem("cannot read the manifest: %v", err)
	}

	if st, err := squashfs.GetSquashFSStatsAt(path, offset); err == nil {
		rec.SquashFS = &report.SquashFS{
			Compression: st.Compression, CompressionLevel: st.CompressionLevel, BlockSize: st.BlockSize,
			Inodes: st.NumInodes, Fragments: st.NumFragments, DuplicatesRemoved: st.DuplicatesRemoved,
			Created: isoTime(st.CreatedTime),
		}
	} else {
		problem("cannot read the SquashFS details: %v", err)
	}
	if rec.Type == artifact.TypeOS {
		if osInfo := squashfs.GetOSInfoAt(path, offset); osInfo != nil {
			rec.Distro = osInfo.String()
		}
	}

	if utils.IsSqf(path) {
		if data := image.ReadFile(path, meta.RecipePath); data != nil {
			rec.Recipe = string(data)
		}
		if rt.Prefix != "" {
			rec.Conda = condaOf(container.PairedInfo(path, rt.Prefix))
		}
		if rec.Type == artifact.TypeEnv {
			rec.OtherFiles = otherFiles(path, doc)
		}
	}
	return nil
}

// describeExt3 fills doc for a writable .img, which has no manifest or runtime.
func describeExt3(path string, doc *report.Inspection) error {
	stats, err := ext3.GetStats(path)
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}
	rec := &doc.Overlay
	rec.Location = plannedLocation(path, true)
	rec.Type = artifact.TypeEnv
	rec.Size = stats.FileSizeBytes
	if stats.IsSparse {
		rec.SizeOnDisk = stats.FileBlocksUsed
	}

	fs := &report.Filesystem{Format: stats.FilesystemType, State: stats.FilesystemState, BlockSize: stats.BlockSize,
		Created: isoTime(stats.CreatedTime)}
	if info, err := os.Stat(path); err == nil {
		fs.Modified = info.ModTime().Format(time.RFC3339)
	}
	if stats.LastMounted != "" && stats.LastMounted != "<not available>" {
		fs.LastMounted = isoTime(stats.LastMounted)
	}
	rec.Filesystem = fs

	switch {
	case stats.UpperUID == 0 && stats.UpperGID == 0:
		rec.Owner = &report.Owner{Fakeroot: true}
	case stats.UpperUID >= 0:
		rec.Owner = &report.Owner{UID: &stats.UpperUID, GID: &stats.UpperGID}
	}
	used, _ := stats.Usage()
	rec.Usage = &report.Usage{Used: used, Total: stats.TotalBlocks * stats.BlockSize,
		InodesUsed: stats.TotalInodes - stats.FreeInodes, InodesTotal: stats.TotalInodes}

	rec.Conda = condaOf(container.PairedInfo(path, meta.EnvPrefix))
	rec.OtherFiles = otherFiles(path, doc)
	return nil
}

// condaOf is the Conda block for an image's history, nil when it has none.
func condaOf(info *conda.CondaInfo) *report.Conda {
	if info == nil {
		return nil
	}
	packages := info.Specs
	if packages == nil {
		packages = []string{}
	}
	return &report.Conda{Channels: info.Channels, Packages: packages}
}

// otherFiles reports whether the payload holds an entry other than /cnt_env,
// not counting a .sqf's own metadata directory. It is nil, with a problem,
// when the image cannot be listed.
func otherFiles(path string, doc *report.Inspection) *bool {
	names, err := image.TopLevel(path)
	if err != nil {
		doc.Problems = append(doc.Problems, report.Problem{Message: fmt.Sprintf("cannot list the payload: %v", err)})
		return nil
	}
	other := len(entriesOutsideEnv(names)) > 0
	return &other
}

// entriesOutsideEnv are the names at a payload's root that are not the Conda
// environment, leaving out a .sqf's own metadata directory.
func entriesOutsideEnv(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool {
		return name == "cnt_env" || name == meta.DirName
	})
}

// isoTime writes a time the tools print as ctime() text in RFC 3339, and
// keeps the text when it does not parse. The tools print local time, so the
// clock reading is taken in the local zone.
func isoTime(s string) string {
	if t, ok := squashfs.ParseStatTime(s); ok {
		local := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local)
		return local.Format(time.RFC3339)
	}
	return s
}
