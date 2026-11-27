package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// downloadIdle is how long a download may receive no data before it fails.
var downloadIdle = time.Minute

// idleReader restarts timer on every read, so only a stalled transfer lets it fire.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
}

func (i idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(downloadIdle)
	}
	return n, err
}

// DownloadFile downloads a file from url to destPath.
//   - The download is cancelled if ctx is cancelled.
//   - There is no limit on its total time. It fails when the server sends no data for downloadIdle.
func DownloadFile(ctx context.Context, url, destPath string) error {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := time.AfterFunc(downloadIdle, cancel)
	defer idle.Stop()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = downloadIdle
	client := &http.Client{Transport: transport}
	defer transport.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return downloadError(parent, ctx, fmt.Errorf("failed to fetch %s: %w", url, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download: HTTP %d", resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	file, err := CreateFileWritable(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	// A shared filesystem reports a failed write at close, so its error counts.
	_, err = io.Copy(file, idleReader{r: resp.Body, timer: idle})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		return downloadError(parent, ctx, fmt.Errorf("failed to write file: %w", err))
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	ShareWithParentGroup(destPath)

	return nil
}

// downloadError names a stall when the idle timer, not the caller, ended the download.
func downloadError(parent, ctx context.Context, err error) error {
	if parent.Err() == nil && ctx.Err() != nil {
		return fmt.Errorf("no data received for %s: %w", downloadIdle, err)
	}
	return err
}

// DownloadExecutable downloads a file and sets it as executable (PermExec).
// The download is cancelled if ctx is cancelled.
func DownloadExecutable(ctx context.Context, url, destPath string) error {
	if err := DownloadFile(ctx, url, destPath); err != nil {
		return err
	}

	if err := MakeExecutable(destPath); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	return nil
}
