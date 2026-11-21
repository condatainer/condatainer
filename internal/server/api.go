package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// helperListEntry wraps HelperRun with server-computed fields for the API response.
type helperListEntry struct {
	helper.HelperRun
	AccessURL   string `json:"access_url,omitempty"`
	EndsAtMs    int64  `json:"ends_at_ms,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
	WalltimeStr string `json:"walltime_str,omitempty"`
}

// fmtDuration formats a time.Duration as "HH:MM:SS".
func fmtDuration(d time.Duration) string {
	if d == 0 {
		return ""
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// fmtUptime formats the duration since t as a human-readable string.
func fmtUptime(since time.Time) string {
	d := time.Since(since)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if h < 24 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	days := h / 24
	h = h % 24
	return fmt.Sprintf("%dd %dh", days, h)
}

// requestPort is the port the client used to reach the server, read from the Host header.
//   - It differs from the listening port when a forward maps another local port to it.
//   - A Host without a port means the HTTP default, 80. An empty or unreadable Host gives the listening port.
func (s *srv) requestPort(r *http.Request) int {
	if r.Host == "" {
		return s.port
	}
	_, portText, err := net.SplitHostPort(r.Host)
	if err != nil {
		if strings.Contains(r.Host, ":") {
			return s.port
		}
		return 80
	}
	if n, err := strconv.Atoi(portText); err == nil && n > 0 && n < 65536 {
		return n
	}
	return s.port
}

// enrichRun wraps a HelperRun with computed fields. The access URL uses port, the one the client reached the server on.
func (s *srv) enrichRun(run *helper.HelperRun, port int) helperListEntry {
	e := helperListEntry{HelperRun: *run}
	e.AccessURL = run.AccessURL(port)
	if est := run.EstimatedEnd(); !est.IsZero() {
		e.EndsAtMs = est.UnixMilli()
	}
	e.LastMessage = s.watcher.LastMsg(run.ID)
	e.WalltimeStr = fmtDuration(run.Walltime())
	return e
}

func isActiveRunStatus(status string) bool {
	return status == "pending" || status == "starting" || status == "running"
}

// handleStatus serves GET /api/status — server health.
func (s *srv) handleStatus(w http.ResponseWriter, r *http.Request) {
	home, _ := utils.RealHome()
	scratch := os.Getenv("SCRATCH")
	hostname, _ := os.Hostname()
	var schedType string
	if sched := scheduler.ActiveScheduler(); sched != nil {
		schedType = string(sched.GetType())
	}
	writeJSON(w, map[string]interface{}{
		"port":         s.port,
		"hostname":     hostname,
		"pid":          os.Getpid(),
		"uptime":       fmtUptime(s.startTime),
		"version":      config.Global.Version,
		"home":         home,
		"scratch":      scratch,
		"notification": helper.Notification(),
		"scheduler":    schedType,
		"will_submit":  schedType != "" && scheduler.Enabled(),
		"debug":        utils.DebugMode,
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
