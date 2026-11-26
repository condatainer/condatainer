package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/scheduler"
)

// historyCacheTTL bounds how long cachedLoadHistory reuses a read of the history.
//   - It saves disk reads across the frequent polling endpoints.
//   - The watcher's own writes go straight to disk, so stale data is bounded by this.
const historyCacheTTL = 3 * time.Second

var (
	historyCacheMu   sync.Mutex
	historyCacheRuns []*helper.HelperRun
	historyCacheAt   time.Time
)

// cachedLoadHistory returns a cached history slice, re-reading from disk at most once per historyCacheTTL.
func cachedLoadHistory() ([]*helper.HelperRun, error) {
	historyCacheMu.Lock()
	defer historyCacheMu.Unlock()
	if historyCacheRuns != nil && time.Since(historyCacheAt) < historyCacheTTL {
		return historyCacheRuns, nil
	}
	runs, err := helper.LoadHistory()
	if err != nil {
		return nil, err
	}
	historyCacheRuns = runs
	historyCacheAt = time.Now()
	return runs, nil
}

// invalidateHistoryCache clears the cached history so the next API request reads fresh data.
// Called by the watcher after each history write so status transitions appear promptly.
func invalidateHistoryCache() {
	historyCacheMu.Lock()
	historyCacheRuns = nil
	historyCacheMu.Unlock()
	notifyJobsChanged()
}

// historyPageSize is how many finished runs one reply of the helper list holds, unless the caller sends limit.
const historyPageSize = 50

// endedOf is when a run ended. A run with no end time sorts last.
func endedOf(r *helper.HelperRun) time.Time {
	if r.EndedAt == nil {
		return time.Time{}
	}
	return *r.EndedAt
}

// endedAfter orders finished runs the way the History table shows them: ended last first, then id.
func endedAfter(a, b *helper.HelperRun) bool {
	if ae, be := endedOf(a), endedOf(b); !ae.Equal(be) {
		return ae.After(be)
	}
	return a.ID > b.ID
}

// handleHelpersList serves GET /api/helpers?q=&status=&limit=&before=&before_id=
//   - The reply holds every active run, newest first, then one page of finished runs, the last ended first.
//   - A page is historyPageSize finished runs, or limit when the caller sends it.
//   - before and before_id are the ended time (RFC 3339) and id of the last finished run the caller has. The reply then holds only the finished runs after it.
//   - X-Total-Count is the number of finished runs that match, before paging.
//   - status=active lists the active runs only.
func (s *srv) handleHelpersList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	q := query.Get("q")
	status := query.Get("status")

	limit := historyPageSize
	if n, err := strconv.Atoi(query.Get("limit")); err == nil && n > 0 {
		limit = n
	}
	var cursor *helper.HelperRun
	if before := query.Get("before"); before != "" {
		ended, err := time.Parse(time.RFC3339Nano, before)
		if err != nil {
			http.Error(w, "before must be an RFC 3339 time", http.StatusBadRequest)
			return
		}
		cursor = &helper.HelperRun{ID: query.Get("before_id"), EndedAt: &ended}
	}

	runs, err := cachedLoadHistory()
	if err != nil {
		http.Error(w, "reading history", http.StatusInternalServerError)
		return
	}

	var active, finished []*helper.HelperRun
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		if status == "active" {
			if !isActiveRunStatus(run.Status) {
				continue
			}
		} else if status != "" && run.Status != status {
			continue
		}
		if q != "" && !strings.Contains(run.Name, q) && !strings.Contains(run.CWD, q) {
			continue
		}
		if isActiveRunStatus(run.Status) {
			active = append(active, run)
		} else {
			finished = append(finished, run)
		}
	}
	sort.SliceStable(finished, func(i, j int) bool { return endedAfter(finished[i], finished[j]) })
	w.Header().Set("X-Total-Count", strconv.Itoa(len(finished)))

	if cursor != nil {
		active = nil
		finished = finished[sort.Search(len(finished), func(i int) bool { return endedAfter(cursor, finished[i]) }):]
	}
	if len(finished) > limit {
		finished = finished[:limit]
	}

	resp := make([]helperListEntry, 0, len(active)+len(finished))
	port := s.requestPort(r)
	for _, run := range append(active, finished...) {
		resp = append(resp, s.enrichRun(run, port))
	}
	writeJSON(w, resp)
}

// handleHelperLogs serves GET /api/helpers/{id}/logs as SSE.
func (s *srv) handleHelperLogs(w http.ResponseWriter, r *http.Request, id string) {
	broker := s.brokers.get(id)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	const maxBackfill = 200
	msgs, _, _ := helper.ReadMessages(id, 0)
	if len(msgs) > maxBackfill {
		msgs = msgs[len(msgs)-maxBackfill:]
	}
	for _, m := range msgs {
		data, _ := json.Marshal(m)
		fmt.Fprintf(w, "data: %s\n\n", data) //nolint:errcheck
	}
	flusher.Flush()

	ch, _, _ := broker.subscribe() // job-log broker has no "finished" concept, unlike task brokers
	defer broker.unsubscribe(ch)
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data) //nolint:errcheck
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n") //nolint:errcheck
			flusher.Flush()
		}
	}
}

// defaultJobLogTail is the default number of lines returned by handleHelperJobLog.
// Prevents loading arbitrarily large log files into the browser.
const defaultJobLogTail = 1000

// handleHelperJobLog serves GET /api/helpers/{id}/joblog — returns the last N lines
// of the job output file (?tail=N, default 1000). Sets X-Log-Truncated: true when
// the file contains more lines than the requested tail.
func (s *srv) handleHelperJobLog(w http.ResponseWriter, r *http.Request, id string) {
	logPath := helper.JobLogFilePath(id)
	tailN := defaultJobLogTail
	if q := r.URL.Query().Get("tail"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			tailN = n
		}
	}
	data, truncated, err := tailFile(logPath, tailN)
	if err != nil {
		http.Error(w, "job log not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if truncated {
		w.Header().Set("X-Log-Truncated", "true")
	}
	w.Write(data) //nolint:errcheck
}

// tailFile returns the last maxLines lines of the file at path, reading backwards
// from EOF in 64 KiB chunks. truncated is true when the file has more lines than maxLines.
func tailFile(path string, maxLines int) (data []byte, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, false, err
	}
	if size == 0 {
		return nil, false, nil
	}

	const chunkSize = 65536
	pos := size
	newlinesSeen := 0

	for pos > 0 {
		n := int64(chunkSize)
		if pos < n {
			n = pos
		}
		pos -= n
		chunk := make([]byte, n)
		if _, err = f.ReadAt(chunk, pos); err != nil {
			return nil, false, err
		}
		for i := int(n) - 1; i >= 0; i-- {
			if chunk[i] == '\n' {
				newlinesSeen++
				if newlinesSeen > maxLines {
					startAt := pos + int64(i) + 1
					out := make([]byte, size-startAt)
					_, err = f.ReadAt(out, startAt)
					return out, true, err
				}
			}
		}
	}

	// File has ≤ maxLines lines — return all of it.
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err = io.ReadAll(f)
	return data, false, err
}

// handleHelperStop serves POST /api/helpers/{id}/stop.
func (s *srv) handleHelperStop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	log := logging.FromContext(s.ctx)
	if run := helper.HistoryEntryForID(id); run == nil {
		log.Warn("server: stop — history entry not found", "id", id)
	} else if err := helper.StopRun(s.ctx, run); errors.Is(err, helper.ErrOtherHost) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if err != nil {
		log.Warn("server: stop failed", "id", id, "jobID", run.JobID, "err", err)
	}
	// closeDone cleans up the proxy, updates history, and moves the ID to the
	// done-map so the watcher skips it on future ticks.
	// This ensures the job counts are correct immediately even if the trap never fires
	// (e.g. SIGKILL, node crash, NFS issue).
	s.watcher.closeDone(id, 130, time.Now()) // the exit code the wrapper records for a stop
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"ok":true}`)
}

// handleHelperDelete serves DELETE /api/helpers/{id}/delete — removes a finished history entry.
// Active entries (pending/starting/running) are rejected with 409 Conflict.
func (s *srv) handleHelperDelete(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE required", http.StatusMethodNotAllowed)
		return
	}
	if run := helper.HistoryEntryForID(id); run != nil && isActiveRunStatus(run.Status) {
		http.Error(w, "cannot delete an active helper; stop it first", http.StatusConflict)
		return
	}
	if err := helper.DeleteHistoryEntry(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	invalidateHistoryCache()
	writeJSON(w, map[string]bool{"ok": true})
}

// handleHelpersClearFinished serves DELETE /api/helpers/finished — removes all
// finished (non-active) history entries and their state directories. Returns
// {"removed": n}.
func (s *srv) handleHelpersClearFinished(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE required", http.StatusMethodNotAllowed)
		return
	}
	n, err := helper.DeleteFinishedHistory()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	invalidateHistoryCache()
	writeJSON(w, map[string]int{"removed": n})
}

// handleGpuOptions serves GET /api/gpu-options: the GPU types of the active scheduler, as gpuOptionsOf gives them.
// No scheduler gives no GPUs; a failed query is a 503 so the client can retry.
func (s *srv) handleGpuOptions(w http.ResponseWriter, r *http.Request) {
	sched := scheduler.ActiveScheduler()
	if sched == nil {
		writeJSON(w, gpuOptions{GPUs: []gpuOption{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	info, err := sched.GetClusterInfo(ctx)
	if err != nil || info == nil {
		http.Error(w, "scheduler query failed", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, gpuOptionsOf(info, sched.GetType()))
}

// gpuOptions is what the start form suggests for its GPU field.
type gpuOptions struct {
	// DefaultPartition is set only on Slurm with a default partition. Each GPU then names its partition.
	DefaultPartition string      `json:"default_partition,omitempty"`
	GPUs             []gpuOption `json:"gpus"`
}

type gpuOption struct {
	Type      string `json:"type"`
	Partition string `json:"partition,omitempty"`
}

// gpuOptionsOf lists the named GPU types of a cluster, sorted.
//   - On Slurm with a default partition, a type is listed once for each partition that has it. A job sent without a partition goes to the default, so the form must show where each type is.
//   - Elsewhere a type is listed once. A site with no default partition routes jobs itself.
//   - A scheduler that names no model, as PBS and HTCondor, reports the generic gpu, which is left out: its request is a count.
func gpuOptionsOf(info *scheduler.ClusterInfo, kind scheduler.SchedulerType) gpuOptions {
	out := gpuOptions{GPUs: []gpuOption{}}
	if kind == scheduler.SchedulerSLURM {
		for _, l := range info.Limits {
			if l.Default {
				out.DefaultPartition = l.Partition
				break
			}
		}
	}
	seen := make(map[gpuOption]bool)
	for _, g := range info.AvailableGpus {
		if g.Type == "" || g.Type == "gpu" {
			continue
		}
		o := gpuOption{Type: g.Type}
		if out.DefaultPartition != "" {
			o.Partition = g.Partition
		}
		if !seen[o] {
			seen[o] = true
			out.GPUs = append(out.GPUs, o)
		}
	}
	sort.Slice(out.GPUs, func(i, j int) bool {
		a, b := out.GPUs[i], out.GPUs[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Partition < b.Partition
	})
	return out
}

// handlePartitions serves GET /api/partitions — returns the partition/queue names of the active scheduler.
// An empty list means no scheduler; a failed query is a 503 so the client can retry.
func (s *srv) handlePartitions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	names := []string{}
	if sched := scheduler.ActiveScheduler(); sched != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		info, err := sched.GetClusterInfo(ctx)
		if err != nil || info == nil {
			http.Error(w, "scheduler query failed", http.StatusServiceUnavailable)
			return
		}
		for _, l := range info.Limits {
			if l.Partition != "" {
				names = append(names, l.Partition)
			}
		}
	}
	sort.Strings(names)
	json.NewEncoder(w).Encode(names) //nolint:errcheck
}
