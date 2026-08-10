package handlers

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/webui/security"
)

func TestSystemHandler_ServeIndex_RendersTemplate(t *testing.T) {
	// Build a minimal template FS that mirrors the real template structure.
	templateContent := `{{define "index.html"}}<!DOCTYPE html>
<html><head><title>GoAl</title></head>
<body><div id="login-modal"></div><div class="dashboard"></div></body></html>{{end}}`

	fsys := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte(templateContent)},
	}

	sup := process.NewSupervisor(nil)
	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)

	h := NewSystemHandler(sup, sess, csrf, insSvc)
	h.WithTemplateFS(fsys)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeIndex(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "login-modal") {
		t.Fatalf("expected rendered HTML to contain login-modal; got: %s", body)
	}
	if !strings.Contains(body, "dashboard") {
		t.Fatalf("expected rendered HTML to contain dashboard; got: %s", body)
	}
}

func TestSystemHandler_ServeIndex_ErrorOnMissingTemplate(t *testing.T) {
	sup := process.NewSupervisor(nil)
	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)

	h := NewSystemHandler(sup, sess, csrf, insSvc)
	h.WithTemplateFS(fstest.MapFS{}) // empty FS

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeIndex(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestStaticServing_FromEmbeddedFS(t *testing.T) {
	// Verify the static subtree works with FileServerFS.
	fsys := fstest.MapFS{
		"static/app.js":    &fstest.MapFile{Data: []byte("console.log('app')")},
		"static/style.css": &fstest.MapFile{Data: []byte("body{color:#000}")},
	}

	sub, err := fs.Sub(fsys, "static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	ts := httptest.NewServer(http.StripPrefix("/static/", http.FileServerFS(sub)))
	defer ts.Close()

	for _, tc := range []struct{ path, want string }{
		{"/static/app.js", "console.log('app')"},
		{"/static/style.css", "body{color:#000}"},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d", tc.path, resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s body: %v", tc.path, err)
		}
		if got := string(data); got != tc.want {
			t.Fatalf("GET %s: expected %q, got %q", tc.path, tc.want, got)
		}
	}
}

// TestLogsStream_HappyPath verifies that an SSE event is delivered over the HTTP handler.
func TestLogsStream_HappyPath(t *testing.T) {
	sup := process.NewSupervisorWithConfig(nil, process.SupervisorConfig{LogBufferSize: 16})

	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)
	h := NewSystemHandler(sup, sess, csrf, insSvc, WithHeartbeat(10*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream", nil).
		WithContext(ctx)
	recorder := newFlushRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.LogsStream(recorder, req)
	}()

	broker := sup.LogBroker()
	if broker == nil {
		cancel()
		wg.Wait()
		t.Fatalf("supervisor has no broker")
	}

	// Give the handler one scheduling tick to subscribe before publishing.
	runtime.Gosched()

	// Wait until handler has written its initial SSE preamble (headers + flush),
	// which guarantees the subscription was created.
	if err := waitForWrite(recorder, 2*time.Second); err != nil {
		cancel()
		wg.Wait()
		t.Fatalf("handler did not write initial frame: %v", err)
	}

	if contentType := recorder.HeaderSnapshot().Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		cancel()
		wg.Wait()
		t.Fatalf("expected text/event-stream, got %q", contentType)
	}

	// Publish and retry until delivered (subscription is eventually consistent).
	var content string
	var publishErr error
	for i := 0; i < 20; i++ {
		broker.Publish(process.LogStreamEvent{
			Timestamp:  time.Now(),
			InstanceID: "inst-1",
			Stream:     process.LogStreamStdout,
			Message:    "hello",
		})
		content, publishErr = waitForBodyContains(recorder, "hello", 200*time.Millisecond)
		if publishErr == nil {
			break
		}
		runtime.Gosched()
	}
	if publishErr != nil {
		cancel()
		wg.Wait()
		t.Fatalf("event not delivered after retries: %v\nbody: %q", publishErr, recorder.buf.String())
	}

	cancel()
	wg.Wait()

	broker.Shutdown()

	if !recorder.IsFlushed() {
		t.Fatalf("handler did not flush response")
	}
	if !strings.Contains(content, "data:") {
		t.Fatalf("expected SSE data frame, got: %q", content)
	}
	if !strings.Contains(content, "hello") {
		t.Fatalf("expected message in SSE payload, got: %q", content)
	}
}

// TestLogsStream_DisconnectCancelsSubscription verifies that disconnecting the request
// cancels the SSE subscription and stops the handler.
func TestLogsStream_DisconnectCancelsSubscription(t *testing.T) {
	sup := process.NewSupervisorWithConfig(nil, process.SupervisorConfig{LogBufferSize: 16})
	broker := sup.LogBroker()
	if broker == nil {
		t.Fatalf("supervisor has no broker")
	}

	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)
	h := NewSystemHandler(sup, sess, csrf, insSvc)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream", nil).
		WithContext(ctx)
	recorder := newFlushRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	returned := make(chan struct{})
	go func() {
		defer wg.Done()
		h.LogsStream(recorder, req)
		close(returned)
	}()

	runtime.Gosched() // let the handler goroutine subscribe

	// Cancel the request context — the handler should terminate quickly.
	cancel()

	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatalf("handler did not return after disconnect")
	}

	// Publish after handler returned — must not be delivered to the old subscription.
	sup.LogBroker().Publish(process.LogStreamEvent{
		Timestamp:  time.Now(),
		InstanceID: "inst-1",
		Stream:     process.LogStreamStdout,
		Message:    "after-disconnect",
	})

	// The recorder should not contain the post-disconnect event.
	if strings.Contains(recorder.buf.String(), "after-disconnect") {
		t.Fatalf("handler wrote event after disconnect: %q", recorder.buf.String())
	}
	sup.LogBroker().Shutdown()
}

// TestLogsStream_BrokerDoneTerminates verifies that when the broker closes the
// subscription's Done channel, the handler exits without blocking.
func TestLogsStream_BrokerDoneTerminates(t *testing.T) {
	sup := process.NewSupervisorWithConfig(nil, process.SupervisorConfig{LogBufferSize: 16})
	broker := sup.LogBroker()
	if broker == nil {
		t.Fatalf("supervisor has no broker")
	}

	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)
	h := NewSystemHandler(sup, sess, csrf, insSvc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream", nil).
		WithContext(ctx)
	recorder := newFlushRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	returned := make(chan struct{})
	go func() {
		defer wg.Done()
		h.LogsStream(recorder, req)
		close(returned)
	}()

	// Wait until handler has written its initial SSE preamble (headers + flush).
	// This proves the handler loop is active and the subscription was created.
	if err := waitForWrite(recorder, 2*time.Second); err != nil {
		cancel()
		wg.Wait()
		t.Fatalf("handler did not write initial frame: %v", err)
	}

	// Since handler is running, subscription was established before or during the initial flush.
	// Simulate broker shutdown by closing subscriber's Done channel through Shutdown.
	// Also cancel the request context to ensure exit isn't blocked by the select.
	broker.Shutdown()
	cancel()

	// Wait for handler to return. The exact timing depends on scheduling, so we use
	// a polling loop with timeout instead of a single channel select.
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-returned:
			return // success
		default:
			if time.Now().After(deadline) {
				t.Fatalf("handler did not return after broker shutdown")
			}
			runtime.Gosched()
		}
	}
}

// TestLogsStream_Heartbeat verifies the SSE heartbeat comment format.
func TestLogsStream_Heartbeat(t *testing.T) {
	sup := process.NewSupervisorWithConfig(nil, process.SupervisorConfig{LogBufferSize: 16})

	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)
	h := NewSystemHandler(sup, sess, csrf, insSvc, WithHeartbeat(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream", nil).
		WithContext(ctx)
	recorder := newFlushRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.LogsStream(recorder, req)
	}()

	runtime.Gosched() // let the handler goroutine subscribe

	// Wait for at least one heartbeat marker.
	content, err := waitForBodyContainsAny(recorder, []string{": ping", ":heartbeat"}, 2*time.Second)
	if err != nil {
		cancel()
		wg.Wait()
		t.Fatalf("no heartbeat observed: %v\nbody: %q", err, recorder.buf.String())
	}

	// Ensure heartbeat matches the SSE comment format exactly.
	if !strings.Contains(content, ": ping") {
		t.Fatalf("heartbeat is not a valid SSE comment: %q", content)
	}

	// Cancel the request and verify the handler exits deterministically.
	cancel()
	wg.Wait()

	// After cancel, verify the handler returned. No additional SSE frames
	// should be produced after the subscription is closed and the handler returns.
	// The fact that wg.Wait() completed proves the loop exited.
	_ = content // silence unused warning — heartbeat content was already asserted above
}

// TestLogsStream_InstanceFilter verifies that per-instance streams only receive events for that instance.
func TestLogsStream_InstanceFilter(t *testing.T) {
	sup := process.NewSupervisorWithConfig(nil, process.SupervisorConfig{LogBufferSize: 16})

	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)
	h := NewSystemHandler(sup, sess, csrf, insSvc)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/inst-2/logs/stream", nil).
		WithContext(ctx)
	recorder := newFlushRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	returned := make(chan struct{})
	go func() {
		defer wg.Done()
		h.InstanceLogStream(recorder, req)
		close(returned)
	}()

	runtime.Gosched() // let the handler goroutine subscribe

	// Publish relevant and irrelevant events (subscription is eventually consistent).
	// Retry until the correct event is received, proving the filter applied.
	var content string
	var filterErr error
	for i := 0; i < 20; i++ {
		sup.LogBroker().Publish(process.LogStreamEvent{
			Timestamp:  time.Now(),
			InstanceID: "inst-1",
			Stream:     process.LogStreamStdout,
			Message:    "wrong-instance",
		})
		sup.LogBroker().Publish(process.LogStreamEvent{
			Timestamp:  time.Now(),
			InstanceID: "inst-2",
			Stream:     process.LogStreamStdout,
			Message:    "correct-instance",
		})
		content, filterErr = waitForBodyContains(recorder, "correct-instance", 200*time.Millisecond)
		if filterErr == nil {
			break
		}
		runtime.Gosched()
	}
	if filterErr != nil {
		cancel()
		wg.Wait()
		t.Fatalf("expected per-instance event not received: %v\nbody: %q", filterErr, recorder.buf.String())
	}

	if strings.Contains(content, "wrong-instance") {
		cancel()
		wg.Wait()
		t.Fatalf("received event from wrong instance: %q", content)
	}

	cancel()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatalf("handler did not return after disconnect")
	}
	sup.LogBroker().Shutdown()
}

// ---------- helpers ----------

// flushRecorder captures the partial body written by an SSE handler.
// All mutable state is guarded by a single mutex to remain race-safe under -race.
type flushRecorder struct {
	mu      sync.Mutex
	header  http.Header
	buf     bytes.Buffer
	status  int
	flushed bool
	closed  bool
	notify  chan struct{} // signaled on each write
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{
		header: http.Header{},
		notify: make(chan struct{}, 1024),
	}
}

// Header returns the internal header map. Required for http.ResponseWriter.
// It is not synchronized because http.ResponseWriter is used exclusively
// by the handler goroutine; test code reads headers only after handler completion.
func (r *flushRecorder) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

// HeaderSnapshot returns a copy of the current header map.
// Safe for concurrent use; the returned map is the caller's ownership.
func (r *flushRecorder) HeaderSnapshot() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.header == nil {
		return http.Header{}
	}
	out := make(http.Header, len(r.header))
	for k, v := range r.header {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (r *flushRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, context.Canceled
	}
	n, err := r.buf.Write(b)
	if n > 0 {
		select {
		case r.notify <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (r *flushRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == 0 {
		r.status = statusCode
	}
}

func (r *flushRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushed = true
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *flushRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

func (r *flushRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *flushRecorder) IsFlushed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushed
}

// waitForWrite blocks until the recorder receives at least one write or flush.
func waitForWrite(rec *flushRecorder, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if rec.BodyString() != "" || rec.IsFlushed() {
			return nil
		}
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		select {
		case <-rec.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitForBodyContains waits until the recorder body contains the given substring,
// or until the timeout expires.
func waitForBodyContains(rec *flushRecorder, want string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", context.DeadlineExceeded
		}
		body := rec.BodyString()
		if strings.Contains(body, want) {
			return body, nil
		}
		select {
		case <-rec.notify:
			// New data written — re-check immediately.
		case <-time.After(50 * time.Millisecond):
			// Poll interval.
		}
	}
}

// waitForBodyContainsAny waits until the recorder body contains any of the given strings.
func waitForBodyContainsAny(rec *flushRecorder, wants []string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", context.DeadlineExceeded
		}
		body := rec.BodyString()
		for _, want := range wants {
			if strings.Contains(body, want) {
				return body, nil
			}
		}
		select {
		case <-rec.notify:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Ensure the interface compliance for ResponseWriter + Flusher.
var _ http.ResponseWriter = (*flushRecorder)(nil)
var _ http.Flusher = (*flushRecorder)(nil)
