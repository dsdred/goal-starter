// Package audit implements the durable, append-only, structured security
// audit log defined by ADR 007: one JSON line per event, per-event fsync,
// bounded rotation, and fail-open write semantics (a write failure is
// reported on the operational log and never affects the business operation).
//
// Secret safety is enforced by construction: the logger accepts only typed
// [AuditEvent] values built by named call sites, and failure diagnostics
// contain only the event name and the raw I/O error.
package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// FileName is the audit log file name inside the data directory.
	FileName = "goal_audit.jsonl"

	// DefaultMaxFileSize is the rotation threshold for the active file
	// (10 MiB). Rotation happens before the append that would cross the
	// threshold.
	DefaultMaxFileSize = 10 << 20

	// DefaultMaxGenerations is the total number of files kept: the active
	// file plus DefaultMaxGenerations-1 rotated files (oldest dropped on
	// rotation).
	DefaultMaxGenerations = 3
)

// Package-level variables (defaults from the ADR constants) so tests can
// exercise rotation with small thresholds.
var (
	maxFileSize    int64 = DefaultMaxFileSize
	maxGenerations       = DefaultMaxGenerations
)

// Event names (dotted taxonomy, ADR 007 §2).
const (
	EventLoginSuccess     = "login.success"
	EventLoginFailure     = "login.failure"
	EventLoginRateLimited = "login.rate_limited"
	EventSessionLogout    = "session.logout"
	EventSettingsSaved    = "settings.saved"
	EventInstanceStart    = "instance.start"
	EventInstanceStop     = "instance.stop"
	EventInstanceRestart  = "instance.restart"
	EventInstanceDismiss  = "instance.dismiss"
	EventInstanceKill     = "instance.kill"
	EventInstanceCleanup  = "instance.cleanup"
	EventConfigReload     = "config.reload"
)

// AuditEvent is a single structured security audit record.
// Detail values are identifiers and booleans only, never secrets.
type AuditEvent struct {
	Timestamp time.Time         `json:"ts"`
	Event     string            `json:"event"`
	User      string            `json:"user,omitempty"`
	SourceIP  string            `json:"src_ip"`
	Detail    map[string]string `json:"detail,omitempty"`
}

// AuditLogger is a single mutex-protected append-only audit writer.
// File order equals occurrence order; every write is fsynced before Log
// returns nil. A failed write is reported via slog.Error (event name + raw
// I/O error only) and returned to the caller, which must treat it as
// non-fatal (fail-open, ADR 007 §6). The logger never latches into a failed
// state: each subsequent event independently attempts a new write.
type AuditLogger struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

// New creates an AuditLogger for the file at path. The file is created
// lazily on the first event; a missing file is a valid state (fresh install).
func New(path string) *AuditLogger {
	return &AuditLogger{path: path}
}

// Path returns the audit file path.
func (l *AuditLogger) Path() string {
	return l.path
}

// Log appends one event as a single O_APPEND write followed by fsync.
// It returns an error on I/O failure after emitting the structured
// operational diagnostic (event name + raw error; no event payload).
func (l *AuditLogger) Log(e AuditEvent) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return l.fail(e, fmt.Errorf("marshal audit event: %w", err))
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.openLocked(); err != nil {
		return l.fail(e, err)
	}
	if l.size+int64(len(line)) > maxFileSize {
		if err := l.rotateLocked(); err != nil {
			return l.fail(e, err)
		}
		if err := l.openLocked(); err != nil {
			return l.fail(e, err)
		}
	}
	if _, err := l.file.Write(line); err != nil {
		return l.fail(e, fmt.Errorf("write audit event: %w", err))
	}
	if err := l.file.Sync(); err != nil {
		return l.fail(e, fmt.Errorf("fsync audit event: %w", err))
	}
	l.size += int64(len(line))
	return nil
}

// Close closes the open audit file, if any. It is idempotent.
func (l *AuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Query returns up to limit events (newest first) starting at offset,
// optionally filtered by exact event name, along with the total number of
// matching events in the file. The file is the source of truth and is read
// on every call; a missing file yields an empty result (fresh install).
// Torn or truncated trailing lines (e.g. from a crash between write and
// fsync) are skipped: they cannot corrupt earlier lines.
func (l *AuditLogger) Query(limit, offset int, event string) ([]AuditEvent, int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []AuditEvent{}, 0, nil
		}
		return nil, 0, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	var all []AuditEvent
	reader := bufio.NewReader(f)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) > 0 {
			// A torn trailing line (crash between write and fsync) fails
			// to parse and is skipped: it cannot corrupt earlier lines.
			if ev, ok := parseLine(strings.TrimRight(string(raw), "\r\n")); ok {
				all = append(all, ev)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("read audit log: %w", readErr)
		}
	}

	if event != "" {
		filtered := make([]AuditEvent, 0, len(all))
		for _, ev := range all {
			if ev.Event == event {
				filtered = append(filtered, ev)
			}
		}
		all = filtered
	}

	total := len(all)
	newestFirst := make([]AuditEvent, 0, total)
	for i := len(all) - 1; i >= 0; i-- {
		newestFirst = append(newestFirst, all[i])
	}

	if offset >= total {
		return []AuditEvent{}, total, nil
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return newestFirst[offset:end], total, nil
}

func (l *AuditLogger) openLocked() error {
	if l.file != nil {
		return nil
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat audit log: %w", err)
	}
	l.file = f
	l.size = info.Size()
	return nil
}

// rotateLocked shifts generations: oldest dropped, .1 → .2, active → .1.
// The active file must not be held open by the caller.
func (l *AuditLogger) rotateLocked() error {
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			l.file = nil
			return fmt.Errorf("close audit log before rotation: %w", err)
		}
		l.file = nil
	}
	oldest := l.path + "." + strconv.Itoa(maxGenerations-1)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("drop oldest audit generation: %w", err)
	}
	for i := maxGenerations - 2; i >= 1; i-- {
		src := l.path + "." + strconv.Itoa(i)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat audit generation %d: %w", i, err)
		}
		if err := os.Rename(src, l.path+"."+strconv.Itoa(i+1)); err != nil {
			return fmt.Errorf("rotate audit generation %d: %w", i, err)
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.size = 0
			return nil
		}
		return fmt.Errorf("rotate audit log: %w", err)
	}
	l.size = 0
	return nil
}

// fail records the operational diagnostic for a failed audit write. Only
// the event name and the raw I/O error are eligible (ADR 007 §6): no user,
// no detail map, no payload.
func (l *AuditLogger) fail(e AuditEvent, err error) error {
	slog.Error("audit", "event", e.Event, "error", err.Error())
	return err
}

func parseLine(line string) (AuditEvent, bool) {
	var ev AuditEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return AuditEvent{}, false
	}
	if ev.Event == "" {
		return AuditEvent{}, false
	}
	return ev, true
}
