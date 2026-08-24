package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger(t *testing.T) (*AuditLogger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	return New(path), path
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	return string(data)
}

func TestLogAndQueryRoundtrip(t *testing.T) {
	logger, path := testLogger(t)
	defer logger.Close()

	ts := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if err := logger.Log(AuditEvent{
		Timestamp: ts,
		Event:     EventLoginSuccess,
		User:      "admin",
		SourceIP:  "127.0.0.1",
		Detail:    map[string]string{"user": "admin"},
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	events, total, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || len(events) != 1 {
		t.Fatalf("total=%d len=%d, want 1/1", total, len(events))
	}
	ev := events[0]
	if ev.Event != EventLoginSuccess || ev.User != "admin" || ev.SourceIP != "127.0.0.1" || !ev.Timestamp.Equal(ts) {
		t.Fatalf("event mismatch: %+v", ev)
	}
	if ev.Detail["user"] != "admin" {
		t.Fatalf("detail mismatch: %+v", ev.Detail)
	}

	raw := readAll(t, path)
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("file must end with newline: %q", raw)
	}
	if strings.Count(raw, "\n") != 1 {
		t.Fatalf("want exactly one line, got: %q", raw)
	}
}

func TestLogSetsTimestampWhenZero(t *testing.T) {
	logger, _ := testLogger(t)
	defer logger.Close()

	before := time.Now().UTC().Add(-time.Second)
	if err := logger.Log(AuditEvent{Event: EventSessionLogout, SourceIP: "10.0.0.1"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	events, _, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Timestamp.Before(before) || events[0].Timestamp.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("timestamp not set to now: %v", events[0].Timestamp)
	}
}

func TestQueryMissingFileIsEmpty(t *testing.T) {
	logger := New(filepath.Join(t.TempDir(), FileName))
	events, total, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 0 || len(events) != 0 {
		t.Fatalf("want empty result, got total=%d len=%d", total, len(events))
	}
}

func TestQueryNewestFirstWithLimitOffsetAndFilter(t *testing.T) {
	logger, _ := testLogger(t)
	defer logger.Close()

	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		ev := AuditEvent{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Event:     EventInstanceStop,
			SourceIP:  "127.0.0.1",
			Detail:    map[string]string{"instance_id": "inst_" + string(rune('a'+i))},
		}
		if i == 3 {
			ev.Event = EventInstanceStart
		}
		if err := logger.Log(ev); err != nil {
			t.Fatalf("Log %d: %v", i, err)
		}
	}

	events, total, err := logger.Query(4, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 10 || len(events) != 4 {
		t.Fatalf("total=%d len=%d, want 10/4", total, len(events))
	}
	if events[0].Detail["instance_id"] != "inst_j" {
		t.Fatalf("newest first violated: %q", events[0].Detail["instance_id"])
	}
	if events[3].Detail["instance_id"] != "inst_g" {
		t.Fatalf("limit window wrong: %q", events[3].Detail["instance_id"])
	}

	events, total, err = logger.Query(4, 6, "")
	if err != nil {
		t.Fatalf("Query offset: %v", err)
	}
	if total != 10 || len(events) != 4 {
		t.Fatalf("offset: total=%d len=%d, want 10/4", total, len(events))
	}
	if events[0].Detail["instance_id"] != "inst_d" {
		t.Fatalf("offset window wrong: %q", events[0].Detail["instance_id"])
	}

	events, total, err = logger.Query(100, 0, EventInstanceStart)
	if err != nil {
		t.Fatalf("Query filter: %v", err)
	}
	if total != 1 || len(events) != 1 || events[0].Detail["instance_id"] != "inst_d" {
		t.Fatalf("filter: total=%d, events=%+v", total, events)
	}

	events, total, err = logger.Query(100, 0, "does.not.exist")
	if err != nil {
		t.Fatalf("Query unknown filter: %v", err)
	}
	if total != 0 || len(events) != 0 {
		t.Fatalf("unknown filter must match nothing: total=%d len=%d", total, len(events))
	}

	if _, total, err = logger.Query(5, 999, ""); err != nil || total != 10 {
		t.Fatalf("offset beyond end: total=%d err=%v", total, err)
	}
}

func TestQuerySkipsTornTrailingLine(t *testing.T) {
	logger, path := testLogger(t)
	defer logger.Close()

	if err := logger.Log(AuditEvent{Event: EventLoginSuccess, User: "admin", SourceIP: "1.1.1.1"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	// Simulate a crash between write and fsync: partial trailing line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("append torn line: %v", err)
	}
	if _, err := f.Write([]byte(`{"ts":"2026-08-24T12:00:00Z","event":"login`)); err != nil {
		t.Fatalf("write torn line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	events, total, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || len(events) != 1 || events[0].Event != EventLoginSuccess {
		t.Fatalf("torn line must be skipped: total=%d events=%+v", total, events)
	}
}

func TestRotationDropsOldestBeyondGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	logger := New(path)
	defer logger.Close()

	origMax := maxFileSize
	maxFileSize = 450
	defer func() { maxFileSize = origMax }()

	// Each line is 103 bytes with a fixed timestamp (102 + '\n'), so 4
	// lines (412 B) fit one generation and the 5th line triggers rotation.
	ts := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	mkLine := func(marker string) AuditEvent {
		return AuditEvent{Timestamp: ts, Event: EventLoginFailure, User: "u", SourceIP: "9.9.9.9", Detail: map[string]string{"m": marker}}
	}
	for g := 0; g < 5; g++ {
		marker := string(rune('A' + g)) // A, B, C, D, E
		for i := 0; i < 4; i++ {
			if err := logger.Log(mkLine(marker)); err != nil {
				t.Fatalf("Log gen %d line %d: %v", g, i, err)
			}
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file .1: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("expected rotated file .2: %v", err)
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("generation .3 must not exist (max %d files)", maxGenerations)
	}

	active := readAll(t, path)
	first := readAll(t, path+".1")
	second := readAll(t, path+".2")
	if !strings.Contains(active, `"m":"E"`) || strings.Contains(active, `"m":"D"`) {
		t.Fatalf("active file must contain generation E only: %q", active)
	}
	if !strings.Contains(first, `"m":"D"`) || strings.Contains(first, `"m":"C"`) {
		t.Fatalf(".1 must contain generation D only: %q", first)
	}
	if !strings.Contains(second, `"m":"C"`) {
		t.Fatalf(".2 must contain generation C: %q", second)
	}
	for _, dropped := range []string{`"m":"A"`, `"m":"B"`} {
		if strings.Contains(active+first+second, dropped) {
			t.Fatalf("dropped generation %s found in surviving files", dropped)
		}
	}
}

func TestConcurrentLogAllLinesPresent(t *testing.T) {
	logger, path := testLogger(t)
	defer logger.Close()

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				err := logger.Log(AuditEvent{
					Event:    EventInstanceCleanup,
					SourceIP: "127.0.0.1",
					Detail:   map[string]string{"w": string(rune('a' + w)), "i": string(rune('0' + i))},
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Log: %v", err)
	}

	data := readAll(t, path)
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	if len(lines) != workers*perWorker {
		t.Fatalf("want %d lines, got %d", workers*perWorker, len(lines))
	}
	seen := make(map[string]int)
	for _, line := range lines {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("torn or invalid line: %q (%v)", line, err)
		}
		seen[ev.Detail["w"]+ev.Detail["i"]]++
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("want %d unique lines, got %d", workers*perWorker, len(seen))
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("line %s appears %d times", k, n)
		}
	}
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestLogFailureEmitsStructuredDiagnosticWithoutPayload(t *testing.T) {
	buf := captureSlog(t)
	logger := New(filepath.Join(t.TempDir(), "no", "such", "dir", FileName))
	defer logger.Close()

	secretUser := "admin"
	secretDetail := "instance_secret_value"
	err := logger.Log(AuditEvent{
		Event:    EventInstanceStart,
		User:     secretUser,
		SourceIP: "7.7.7.7",
		Detail:   map[string]string{"error": secretDetail},
	})
	if err == nil {
		t.Fatal("want write failure for missing directory")
	}

	diag := buf.String()
	if !strings.Contains(diag, "level=ERROR") {
		t.Fatalf("want structured ERROR diagnostic, got: %q", diag)
	}
	if !strings.Contains(diag, "event=instance.start") {
		t.Fatalf("diagnostic must contain the event name: %q", diag)
	}
	for _, secret := range []string{secretDetail, "7.7.7.7"} {
		if strings.Contains(diag, secret) {
			t.Fatalf("diagnostic must not contain payload data %q: %q", secret, diag)
		}
	}
}

func TestLogRecoveryAfterFailureNoLatch(t *testing.T) {
	buf := captureSlog(t)
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	logger := New(path)
	defer logger.Close()

	// Induce a failure: file exists as a directory, so O_APPEND|O_CREATE fails.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := logger.Log(AuditEvent{Event: EventLoginFailure, User: "u", SourceIP: "1.2.3.4"}); err == nil {
		t.Fatal("want first write to fail")
	}

	// Clear the fault: remove the directory, logger must recover without a latch.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := logger.Log(AuditEvent{Event: EventLoginSuccess, User: "u", SourceIP: "1.2.3.4"}); err != nil {
		t.Fatalf("recovery write must succeed: %v", err)
	}

	events, total, err := logger.Query(100, 0, "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || events[0].Event != EventLoginSuccess {
		t.Fatalf("want exactly the recovered event: total=%d %+v", total, events)
	}
	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("want ERROR diagnostic for the failed write: %q", buf.String())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	logger, _ := testLogger(t)
	if err := logger.Log(AuditEvent{Event: EventSessionLogout, SourceIP: "1.1.1.1"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
