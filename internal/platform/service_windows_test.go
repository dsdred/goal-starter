//go:build windows

package platform

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// TestServiceBinPath verifies the exact registered service image per ADR 011
// D2 (quotes iff spaces; fixed tokens unquoted).
func TestServiceBinPath(t *testing.T) {
	cases := []struct {
		exe, cfg, want string
	}{
		{
			exe:  `C:\Program Files\GoAl\goal.exe`,
			cfg:  `C:\Program Files\GoAl\goal.json`,
			want: `"C:\Program Files\GoAl\goal.exe" --service run --config "C:\Program Files\GoAl\goal.json"`,
		},
		{
			exe:  `C:\GoAl\goal.exe`,
			cfg:  `C:\GoAl\goal.json`,
			want: `C:\GoAl\goal.exe --service run --config C:\GoAl\goal.json`,
		},
		{
			exe:  `C:\GoAl\goal.exe`,
			cfg:  `C:\Program Files\GoAl\goal.json`,
			want: `C:\GoAl\goal.exe --service run --config "C:\Program Files\GoAl\goal.json"`,
		},
	}
	for _, tc := range cases {
		if got := serviceBinPath(tc.exe, tc.cfg); got != tc.want {
			t.Errorf("serviceBinPath(%q, %q) = %q, want %q", tc.exe, tc.cfg, got, tc.want)
		}
	}
}

// TestServiceRunOutsideSCM verifies the bounded refusal of the run verb
// outside an SCM session (ADR 011 D1.2): no UI, no stdin, non-zero exit by
// the caller.
func TestServiceRunOutsideSCM(t *testing.T) {
	m := NewServiceManager()
	err := m.RunService(ServiceRunOptions{Name: "GoAl"})
	if err == nil {
		t.Fatal("expected a bounded error outside an SCM session")
	}
	if !strings.Contains(err.Error(), "Service Control Manager") {
		t.Fatalf("error %q: missing SCM mention", err)
	}
}

// fakeServiceHandle is the SCM service-handle fake for the management verb
// flow tests (no real SCM required): it records every operation and reports
// the configured state transitions.
type fakeServiceHandle struct {
	state      svc.State
	startErr   error
	controlErr error
	starts     int
	queries    int
	controls   []svc.Cmd
	closed     bool
	events     []string
}

func (f *fakeServiceHandle) Query() (svc.Status, error) {
	f.queries++
	f.events = append(f.events, "query")
	return svc.Status{State: f.state}, nil
}

func (f *fakeServiceHandle) Start(args ...string) error {
	f.starts++
	f.events = append(f.events, "start")
	if f.startErr != nil {
		return f.startErr
	}
	f.state = svc.Running
	return nil
}

func (f *fakeServiceHandle) Control(cmd svc.Cmd) (svc.Status, error) {
	f.controls = append(f.controls, cmd)
	f.events = append(f.events, "control")
	if f.controlErr != nil {
		return svc.Status{}, f.controlErr
	}
	f.state = svc.Stopped
	return svc.Status{State: svc.Stopped}, nil
}

func (f *fakeServiceHandle) Close() error {
	f.closed = true
	return nil
}

func openFake(h *fakeServiceHandle) serviceOpener {
	return func(string) (serviceHandle, error) { return h, nil }
}

// TestServiceProcessUptimeBounded proves the uptime path never panics and is
// bounded: invalid/zero PIDs return 0 (the old implementation panicked here
// on an unresolvable advapi32 symbol), and the real success path (own test
// process) yields a plausible uptime.
func TestServiceProcessUptimeBounded(t *testing.T) {
	if got := serviceProcessUptime(0); got != 0 {
		t.Fatalf("serviceProcessUptime(0) = %s, want 0", got)
	}
	if got := serviceProcessUptime(0xFFFFFFFF); got != 0 {
		t.Fatalf("serviceProcessUptime(invalid pid) = %s, want 0", got)
	}
	own := uint32(os.Getpid())
	up := serviceProcessUptime(own)
	if up <= 0 {
		t.Fatalf("serviceProcessUptime(own pid) = %s, want > 0", up)
	}
	if up > time.Hour {
		t.Fatalf("serviceProcessUptime(own pid) = %s: implausible", up)
	}
}

// TestStartFlow verifies the Start verb flow (ADR 011 D6.5): SCM start is
// invoked, the wait targets Running with the 90 s budget, the handle is
// closed, and success is returned.
func TestStartFlowWaitsRunningAndCloses(t *testing.T) {
	m := &windowsServiceManager{}
	h := &fakeServiceHandle{state: svc.Stopped}
	var waited []string
	wait := func(name string, want svc.State, budget time.Duration, op string) error {
		waited = append(waited, op)
		if op != "start" || want != svc.Running || budget != 90*time.Second {
			t.Fatalf("wait(%q, %d, %s): want start/Running/90s", op, int(want), budget)
		}
		return nil
	}
	if err := m.start("GoAl", openFake(h), wait); err != nil {
		t.Fatalf("start: %v", err)
	}
	if h.starts != 1 || len(waited) != 1 || !h.closed {
		t.Fatalf("starts=%d waited=%v closed=%v", h.starts, waited, h.closed)
	}
}

// TestStartAlreadyRunning is a bounded no-op success (no wait, no error).
func TestStartAlreadyRunningNoop(t *testing.T) {
	m := &windowsServiceManager{}
	h := &fakeServiceHandle{state: svc.Running, startErr: windows.ERROR_SERVICE_ALREADY_RUNNING}
	waited := 0
	wait := func(string, svc.State, time.Duration, string) error { waited++; return nil }
	if err := m.start("GoAl", openFake(h), wait); err != nil {
		t.Fatalf("start: %v", err)
	}
	if h.starts != 1 || waited != 0 {
		t.Fatalf("starts=%d waited=%d: already-running must be a no-op without waiting", h.starts, waited)
	}
}

// TestStartErrorPropagates verifies a failed SCM start returns a bounded error.
func TestStartErrorPropagates(t *testing.T) {
	m := &windowsServiceManager{}
	h := &fakeServiceHandle{startErr: errors.New("scm refused")}
	err := m.start("GoAl", openFake(h), func(string, svc.State, time.Duration, string) error {
		t.Fatal("wait must not run after a failed start")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("err = %v, want bounded start error", err)
	}
}

// TestStopFlow verifies the Stop verb flow (ADR 011 D6.2/D6.3): the stop
// control is requested, the wait targets Stopped with a budget strictly
// greater than the registered 45 s SCM stop timeout, and success is returned.
func TestStopFlowWaitsStopped(t *testing.T) {
	m := &windowsServiceManager{}
	h := &fakeServiceHandle{state: svc.Running}
	wait := func(name string, want svc.State, budget time.Duration, op string) error {
		if op != "stop" || want != svc.Stopped {
			t.Fatalf("wait(%q, %d): want stop/Stopped", op, int(want))
		}
		if budget <= DefaultStopTimeout {
			t.Fatalf("stop wait budget %s must exceed the %s SCM stop timeout", budget, DefaultStopTimeout)
		}
		return nil
	}
	if err := m.stop("GoAl", openFake(h), wait); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if h.starts != 0 || len(h.controls) != 1 || h.controls[0] != svc.Stop || !h.closed {
		t.Fatalf("starts=%d controls=%v closed=%v", h.starts, h.controls, h.closed)
	}
}

// TestStopAlreadyStopped is a bounded no-op success (no control, no wait).
func TestStopAlreadyStoppedNoop(t *testing.T) {
	m := &windowsServiceManager{}
	h := &fakeServiceHandle{state: svc.Stopped}
	waited := 0
	wait := func(string, svc.State, time.Duration, string) error { waited++; return nil }
	if err := m.stop("GoAl", openFake(h), wait); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(h.controls) != 0 || waited != 0 || h.queries != 1 {
		t.Fatalf("controls=%v waited=%d queries=%d: already-stopped must be a no-op", h.controls, waited, h.queries)
	}
}

// TestRestartFlow verifies the Restart verb order (ADR 011 D7): Stop → wait
// Stopped → Start → wait Running, with no parallel or self-reexec mechanism.
func TestRestartFlowOrder(t *testing.T) {
	m := &windowsServiceManager{}
	h := &fakeServiceHandle{state: svc.Running}
	var waited []string
	wait := func(name string, want svc.State, budget time.Duration, op string) error {
		wantName := stateName(want)
		waited = append(waited, wantName)
		return nil
	}
	if err := m.restart("GoAl", openFake(h), wait); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if len(waited) != 2 || waited[0] != "Stopped" || waited[1] != "Running" {
		t.Fatalf("wait order %v: want [Stopped Running]", waited)
	}
	// Event order: query (stop check) → control (stop) → start.
	var ops []string
	for _, e := range h.events {
		if e == "control" {
			ops = append(ops, "stop")
		} else if e == "start" {
			ops = append(ops, "start")
		}
	}
	if len(ops) != 2 || ops[0] != "stop" || ops[1] != "start" {
		t.Fatalf("operation order %v: want [stop start]", ops)
	}
	if len(h.controls) != 1 || h.controls[0] != svc.Stop {
		t.Fatalf("controls=%v: want exactly [Stop]", h.controls)
	}
}

// TestStatusBoundedNeverPanics proves the status path (the former
// QueryServiceStatusExW panic site) returns a bounded error for a missing
// service and never panics, regardless of the machine's SCM access rights.
func TestStatusBoundedNeverPanics(t *testing.T) {
	m := NewServiceManager()
	_, err := m.Status("GoAlNoSuchServiceTest")
	if err == nil {
		t.Fatal("expected a bounded error for a missing service")
	}
	if !strings.Contains(err.Error(), "service") {
		t.Fatalf("error %q: missing bounded service diagnostic", err)
	}
}

// --- slogEventHandler serialization tests (ADR 011 D8.1) ---

func TestSlogEventHandlerFormatRecord_MessageOnly(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	rec := slog.NewRecord(time.Now(), slog.LevelError, "service: stopped", 0)
	got := h.formatRecord(rec)
	if got != "service: stopped" {
		t.Fatalf("got %q, want %q", got, "service: stopped")
	}
}

func TestSlogEventHandlerFormatRecord_ErrorAttr(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	rec := slog.NewRecord(time.Now(), slog.LevelError, "instance start failed", 0)
	rec.AddAttrs(
		slog.String("instance_id", "abc-123"),
		slog.String("model_id", "denied-model"),
		slog.String("error", "executable does not exist: C:\\x\\y.exe: Access is denied"),
	)
	got := h.formatRecord(rec)
	if !strings.HasPrefix(got, "instance start failed") {
		t.Fatalf("missing message prefix: %q", got)
	}
	if !strings.Contains(got, "instance_id=abc-123") {
		t.Fatalf("missing instance_id attr: %q", got)
	}
	if !strings.Contains(got, "model_id=denied-model") {
		t.Fatalf("missing model_id attr: %q", got)
	}
	if !strings.Contains(got, "Access is denied") {
		t.Fatalf("missing error content: %q", got)
	}
}

func TestSlogEventHandlerFormatRecord_MixedAttrTypes(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	rec := slog.NewRecord(time.Now(), slog.LevelWarn, "port in use", 0)
	rec.AddAttrs(
		slog.Int("port", 8080),
		slog.String("addr", "127.0.0.1"),
		slog.Bool("retried", true),
		slog.Duration("wait", 3*time.Second),
	)
	got := h.formatRecord(rec)
	for _, want := range []string{"port=8080", "addr=127.0.0.1", "retried=true", "wait=3s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestSlogEventHandlerFormatRecord_WithAttrs(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	h2 := h.WithAttrs([]slog.Attr{slog.String("component", "supervisor")})
	eh2, ok := h2.(*slogEventHandler)
	if !ok {
		t.Fatal("WithAttrs did not return *slogEventHandler")
	}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "started", 0)
	rec.AddAttrs(slog.String("id", "x"))
	got := eh2.formatRecord(rec)
	if !strings.Contains(got, "component=supervisor") {
		t.Fatalf("WithAttrs not retained: %q", got)
	}
	if !strings.Contains(got, "id=x") {
		t.Fatalf("record attr missing: %q", got)
	}
	// Order: WithAttrs first, then record attrs.
	if idxComp := strings.Index(got, "component="); idxComp != -1 {
		if idxID := strings.Index(got, "id="); idxID != -1 {
			if idxID < idxComp {
				t.Fatalf("WithAttrs should precede record attrs: %q", got)
			}
		}
	}
}

func TestSlogEventHandlerFormatRecord_WithGroup(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	h2 := h.WithGroup("net")
	eh2, ok := h2.(*slogEventHandler)
	if !ok {
		t.Fatal("WithGroup did not return *slogEventHandler")
	}
	rec := slog.NewRecord(time.Now(), slog.LevelError, "bind failed", 0)
	rec.AddAttrs(slog.String("addr", ":8080"))
	got := eh2.formatRecord(rec)
	if !strings.Contains(got, "net.addr=:8080") {
		t.Fatalf("group prefix missing: %q", got)
	}
}

func TestSlogEventHandlerFormatRecord_NestedGroup(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	h2 := h.WithGroup("outer").WithGroup("inner")
	eh2, ok := h2.(*slogEventHandler)
	if !ok {
		t.Fatal("nested WithGroup did not return *slogEventHandler")
	}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.String("k", "v"))
	got := eh2.formatRecord(rec)
	if !strings.Contains(got, "outer.inner.k=v") {
		t.Fatalf("nested group prefix wrong: %q", got)
	}
}

func TestSlogEventHandlerFormatRecord_Bounded(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	long := strings.Repeat("x", 2000)
	rec := slog.NewRecord(time.Now(), slog.LevelError, long, 0)
	got := h.formatRecord(rec)
	if len(got) > eventLogPayloadLimit {
		t.Fatalf("payload len %d exceeds limit %d", len(got), eventLogPayloadLimit)
	}
	if !strings.HasPrefix(got, "xxx") {
		t.Fatalf("truncation corrupted prefix: %q", got[:10])
	}
}

func TestSlogEventHandlerFormatRecord_BoundedWithAttrs(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	rec := slog.NewRecord(time.Now(), slog.LevelError, "msg", 0)
	rec.AddAttrs(slog.String("data", strings.Repeat("y", 2000)))
	got := h.formatRecord(rec)
	if len(got) > eventLogPayloadLimit {
		t.Fatalf("payload len %d exceeds limit %d", len(got), eventLogPayloadLimit)
	}
	if !strings.HasPrefix(got, "msg") {
		t.Fatalf("message prefix lost after truncation: %q", got[:10])
	}
}

func TestSlogEventHandlerSeverityMapping(t *testing.T) {
	h := &slogEventHandler{log: &eventLogger{}}
	cases := []struct {
		lvl  slog.Level
		name string
	}{
		{slog.LevelError, "error"},
		{slog.LevelWarn, "warn"},
		{slog.LevelInfo, "info"},
	}
	for _, tc := range cases {
		rec := slog.NewRecord(time.Now(), tc.lvl, "test-"+tc.name, 0)
		if got := h.formatRecord(rec); !strings.HasPrefix(got, "test-"+tc.name) {
			t.Fatalf("level %s: got %q", tc.name, got)
		}
	}
}

// TestEventLoggerZeroHandleGuard verifies that logf on a zero-value
// eventLogger (source == 0) returns without panicking (ADR 011 D8.1
// handle guard after the D8.1 correction: ReportEvent uses the
// RegisterEventSource handle, not a separate OpenEventLog handle).
func TestEventLoggerZeroHandleGuard(t *testing.T) {
	// Zero-value: source == 0, logf must return early (no syscall).
	var l eventLogger
	l.logf(slog.LevelInfo, "should not reach ReportEvent")
	l.logf(slog.LevelError, "should not reach ReportEvent")

	// Nil pointer: must also return early.
	var nl *eventLogger
	nl.logf(slog.LevelInfo, "nil guard")

	// close on zero-value must not panic.
	l.close()
	nl.close()
}
