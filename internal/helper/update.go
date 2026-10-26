package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

// helperIndexPath is where a collection publishes its helper listing.
const helperIndexPath = "index/helpers.json"

// ErrIndexEntry reports a helper index entry a build cannot use.
var ErrIndexEntry = errors.New("helper: unusable index entry")

// RemoteScriptEntry is one helper a source offers.
type RemoteScriptEntry struct {
	Path        string          `json:"path"`
	Description string          `json:"description,omitempty"`
	SHA256      string          `json:"sha256"` // of the script bytes, hex
	Source      *catalog.Source `json:"-"`      // the source it came from
}

// RefreshRemoteMetadata reads the helper index from every configured source, merged with earlier sources winning.
//   - Fetching and caching belong to the catalog, which already bounds staleness and serves a cached copy when a source is unreachable.
//   - A source with no helper index simply contributes nothing.
func RefreshRemoteMetadata(ctx context.Context, force bool, w io.Writer) (map[string]RemoteScriptEntry, error) {
	if force {
		if err := config.RefreshCatalogCache(); err != nil && w != nil {
			fmt.Fprintf(w, "WARN: failed to clear the source cache: %v\n", err)
		}
	}

	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return nil, err
	}

	merged := map[string]RemoteScriptEntry{}
	var firstErr error
	for _, src := range cat {
		meta, err := readHelperIndex(ctx, cat, src, w)
		if err != nil {
			if helperIndexNotFound(err) {
				continue
			}
			if w != nil {
				fmt.Fprintf(w, "WARN: no helper index in %s: %v\n", src.Name, err)
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if src.Stale && w != nil {
			fmt.Fprintf(w, "WARN: %s was unreachable; using its cached helper index\n", src.Name)
		}
		for name, entry := range meta {
			if _, exists := merged[name]; !exists {
				entry.Source = src
				merged[name] = entry
			}
		}
	}

	if len(merged) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return merged, nil
}

// helperIndexNotFound identifies an optional index that an HTTP source does not
// publish. The catalog's HTTP errors include the response status in their
// stable message.
func helperIndexNotFound(err error) bool {
	return strings.HasSuffix(err.Error(), ": 404 Not Found")
}

// readHelperIndex lists the helpers a source offers.
//   - A directory source is scanned for helpers/, and has no index to read.
//   - An HTTP source lists them in index/helpers.json.gz, falling back to the plain file.
func readHelperIndex(ctx context.Context, cat catalog.Catalog, src *catalog.Source, w io.Writer) (map[string]RemoteScriptEntry, error) {
	if files, local, err := src.HelperFiles(); local {
		if err != nil {
			return nil, err
		}
		scripts := make(map[string]RemoteScriptEntry, len(files))
		for _, file := range files {
			data, err := src.ReadFresh(ctx, file)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(data)
			scripts[filepath.Base(file)] = RemoteScriptEntry{Path: file, Description: descriptionOf(data), SHA256: hex.EncodeToString(sum[:])}
		}
		return scripts, nil
	}
	if data, err := cat.ReadPath(ctx, src, helperIndexPath+".gz"); err == nil {
		if plain, err := gunzip(data); err == nil {
			return decodeHelperIndex(plain)
		}
	}
	data, err := cat.ReadPath(ctx, src, helperIndexPath)
	if err != nil {
		return nil, err
	}
	return decodeHelperIndex(data)
}

func decodeHelperIndex(data []byte) (map[string]RemoteScriptEntry, error) {
	var meta map[string]RemoteScriptEntry
	if err := catalog.DecodeIndex(data, "helpers", &meta); err != nil {
		return nil, fmt.Errorf("failed to parse helper index: %w", err)
	}
	for name, entry := range meta {
		if entry.SHA256 == "" {
			return nil, fmt.Errorf("%w: %q has no sha256", ErrIndexEntry, name)
		}
	}
	return meta, nil
}

func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// ErrNotScript reports a downloaded helper whose first line is not an allowed interpreter line.
var ErrNotScript = errors.New("helper: not a script")

// writeExecutable replaces the file at destPath with data and makes it executable.
func writeExecutable(destPath string, data []byte) error {
	out, err := utils.CreateFileWritable(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write(data); err != nil {
		return err
	}
	return utils.MakeExecutable(destPath)
}
