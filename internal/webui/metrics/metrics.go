package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

// Manager collects and exposes application metrics in Prometheus format.
type Manager struct {
	// Request counters by method and path
	requestCount atomic.Int64

	// Error counters
	errorCount atomic.Int64

	// Uptime start time
	startTime time.Time

	// Process metrics
	modelsStarted atomic.Int64
	modelsStopped atomic.Int64
	modelsFailed  atomic.Int64

	// Session metrics
	sessionsCreated atomic.Int64
	sessionsExpired atomic.Int64
}

// NewManager creates a new metrics manager.
func NewManager() *Manager {
	return &Manager{
		startTime: time.Now(),
	}
}

// RecordRequest records an HTTP request.
func (m *Manager) RecordRequest() {
	m.requestCount.Add(1)
}

// RecordError records an HTTP error.
func (m *Manager) RecordError() {
	m.errorCount.Add(1)
}

// RecordModelStart records a model start.
func (m *Manager) RecordModelStart() {
	m.modelsStarted.Add(1)
}

// RecordModelStop records a model stop.
func (m *Manager) RecordModelStop() {
	m.modelsStopped.Add(1)
}

// RecordModelFailed records a model start/stop failure.
func (m *Manager) RecordModelFailed() {
	m.modelsFailed.Add(1)
}

// RecordSessionCreated records a session creation.
func (m *Manager) RecordSessionCreated() {
	m.sessionsCreated.Add(1)
}

// RecordSessionExpired records a session expiration.
func (m *Manager) RecordSessionExpired() {
	m.sessionsExpired.Add(1)
}

// Handler returns an HTTP handler that serves metrics in Prometheus format.
func (m *Manager) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		uptime := time.Since(m.startTime).Round(time.Second)

		fmt.Fprintln(w, `# HELP goal_uptime_seconds Application uptime in seconds`)
		fmt.Fprintln(w, `# TYPE goal_uptime_seconds counter`)
		fmt.Fprintf(w, "goal_uptime_seconds %d\n", int64(uptime.Seconds()))

		fmt.Fprintln(w, `# HELP goal_http_requests_total Total HTTP requests`)
		fmt.Fprintln(w, `# TYPE goal_http_requests_total counter`)
		fmt.Fprintf(w, "goal_http_requests_total %d\n", m.requestCount.Load())

		fmt.Fprintln(w, `# HELP goal_http_errors_total Total HTTP errors`)
		fmt.Fprintln(w, `# TYPE goal_http_errors_total counter`)
		fmt.Fprintf(w, "goal_http_errors_total %d\n", m.errorCount.Load())

		fmt.Fprintln(w, `# HELP goal_models_started_total Total model starts`)
		fmt.Fprintln(w, `# TYPE goal_models_started_total counter`)
		fmt.Fprintf(w, "goal_models_started_total %d\n", m.modelsStarted.Load())

		fmt.Fprintln(w, `# HELP goal_models_stopped_total Total model stops`)
		fmt.Fprintln(w, `# TYPE goal_models_stopped_total counter`)
		fmt.Fprintf(w, "goal_models_stopped_total %d\n", m.modelsStopped.Load())

		fmt.Fprintln(w, `# HELP goal_models_failed_total Total model failures`)
		fmt.Fprintln(w, `# TYPE goal_models_failed_total counter`)
		fmt.Fprintf(w, "goal_models_failed_total %d\n", m.modelsFailed.Load())

		fmt.Fprintln(w, `# HELP goal_sessions_created_total Total sessions created`)
		fmt.Fprintln(w, `# TYPE goal_sessions_created_total counter`)
		fmt.Fprintf(w, "goal_sessions_created_total %d\n", m.sessionsCreated.Load())

		fmt.Fprintln(w, `# HELP goal_sessions_expired_total Total sessions expired`)
		fmt.Fprintln(w, `# TYPE goal_sessions_expired_total counter`)
		fmt.Fprintf(w, "goal_sessions_expired_total %d\n", m.sessionsExpired.Load())

		// Go runtime metrics
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)

		fmt.Fprintln(w, `# HELP goal_go_memstats_alloc_bytes Current allocated bytes`)
		fmt.Fprintln(w, `# TYPE goal_go_memstats_alloc_bytes gauge`)
		fmt.Fprintf(w, "goal_go_memstats_alloc_bytes %d\n", stats.Alloc)

		fmt.Fprintln(w, `# HELP goal_go_memstats_total_alloc_bytes Total allocated bytes over time`)
		fmt.Fprintln(w, `# TYPE goal_go_memstats_total_alloc_bytes counter`)
		fmt.Fprintf(w, "goal_go_memstats_total_alloc_bytes %d\n", stats.TotalAlloc)

		fmt.Fprintln(w, `# HELP goal_go_memstats_sys_bytes Memory used by runtime`)
		fmt.Fprintln(w, `# TYPE goal_go_memstats_sys_bytes gauge`)
		fmt.Fprintf(w, "goal_go_memstats_sys_bytes %d\n", stats.Sys)

		fmt.Fprintln(w, `# HELP goal_go_goroutines Number of goroutines`)
		fmt.Fprintln(w, `# TYPE goal_go_goroutines gauge`)
		fmt.Fprintf(w, "goal_go_goroutines %d\n", runtime.NumGoroutine())

		fmt.Fprintln(w, `# HELP goal_go_numcpu Number of CPU cores`)
		fmt.Fprintln(w, `# TYPE goal_go_numcpu gauge`)
		fmt.Fprintf(w, "goal_go_numcpu %d\n", runtime.NumCPU())
	}
}
