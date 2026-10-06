package server

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/logging"
)

// configCheckInterval is the least time between looks at the config files.
const configCheckInterval = 3 * time.Second

// reloadConfig re-reads the config files before a request when one changed since the last read.
// A long-lived server would otherwise keep the settings it started with.
func (s *srv) reloadConfig(next http.Handler) http.Handler {
	var last atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixNano()
		prev := last.Load()
		if now-prev >= int64(configCheckInterval) && last.CompareAndSwap(prev, now) {
			logger := logging.FromContext(s.ctx)
			switch reloaded, err := config.ReloadIfChanged(); {
			case err != nil:
				logger.Warn("server: config files changed but were not reloaded", "error", err)
			case reloaded:
				logger.Info("server: config files changed, reloaded")
			}
		}
		next.ServeHTTP(w, r)
	})
}
