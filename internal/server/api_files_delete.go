package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// handleFSDelete serves DELETE /api/fs?path=...: it deletes a file, or a directory recursively, in a background task.
//   - It returns the task ID at once. GET /api/tasks/{id}/stream sends one progress event per entry removed.
//   - POST /api/tasks/{id}/cancel stops it between entries.
//   - A symlink is removed itself, not followed.
//   - There is no confirmation or undo at this layer.
func (s *srv) handleFSDelete(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	if _, err := os.Lstat(path); err != nil {
		fsErrorResponse(w, path, err)
		return
	}

	taskID := fmt.Sprintf("delete-%d", time.Now().UnixNano())
	broker := newSSEBroker()
	ctx, cancel := context.WithCancel(s.ctx)
	s.tasks.Store(taskID, &taskEntry{broker: broker, cancel: cancel})
	writeJSON(w, map[string]string{"id": taskID})

	go func() {
		defer cancel()
		defer s.scheduleTaskCleanup(taskID)

		total, err := countTreeEntries(path)
		if err != nil {
			broadcastResult(broker, ctx, fmt.Errorf("scanning %s: %w", path, err))
			return
		}

		removed := 0
		broadcastProgress(broker, 0, total)
		err = removeTree(ctx, path, func() {
			removed++
			broadcastProgress(broker, removed, total)
		})
		broadcastResult(broker, ctx, err)
	}()
}
