//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// localSystemAccount is the explicit MVP service account (ADR 011 D4.1).
const localSystemAccount = "LocalSystem"

const serviceEventSource = "GoAl"

// serviceBinPath assembles the exact registered service image per ADR 011 D2:
// "<EXE>" --service run --config "<CONFIG>" — each path part is wrapped in
// double quotes by the SCM command-line escaping rules iff it contains a
// space or a quote; the fixed tokens carry no spaces and stay unquoted. It
// mirrors the escaping mgr.CreateService applies, so the requested image and
// the re-install comparison agree byte-for-byte.
func serviceBinPath(exePath, configPath string) string {
	parts := []string{exePath, "--service", "run", "--config", configPath}
	for i, p := range parts {
		parts[i] = syscall.EscapeArg(p)
	}
	return strings.Join(parts, " ")
}

type windowsServiceManager struct{}

func newServiceManager() ServiceManager {
	return &windowsServiceManager{}
}

// IsService reports whether the current process was launched by the SCM.
func (m *windowsServiceManager) IsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// --- run verb: in-process SCM entrypoint (ADR 011 D1.2, D6) ---

type serviceHandler struct {
	opts ServiceRunOptions
	log  *eventLogger
}

func (h *serviceHandler) logf(level slog.Level, msg string) {
	h.log.logf(level, msg)
}

// Execute runs the one shared application lifecycle under an appCtx cancelled
// by the SCM stop request (ADR 011 D6.1/D6.2). Running is reported only after
// the HTTP server bind; a pre-bind failure reports Stopped and exits. Stop
// returns SERVICE_STOP_PENDING with the 30 s wait hint and reports
// SERVICE_STOPPED only when the unchanged foreground shutdown path has
// completed (there is no second stop path).
func (h *serviceHandler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	s <- svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 30000}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	appErr := make(chan error, 1)
	appDone := make(chan struct{})
	go func() {
		appErr <- h.opts.RunApp(ctx)
		close(appDone)
	}()

	// D6.1: Running is reported to the SCM only after the successful
	// application startup and the HTTP server bind. A pre-bind failure (bad
	// config, recovery error, port in use) reports Stopped and exits — no
	// partial Running claim, no false success.
	bound := make(chan struct{}, 1)
	go h.watchBind(ctx, appDone, bound)

	running := false
	stopping := false
	for {
		select {
		case <-bound:
			running = true
			h.logf(slog.LevelInfo, "service: HTTP server bound, reporting Running")
			s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop, CheckPoint: 0}
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				// D6.4: real state derived from the Supervisor snapshot.
				detail := ""
				if h.opts.StatusText != nil {
					detail = h.opts.StatusText()
				}
				state := svc.StartPending
				if running {
					state = svc.Running
				}
				if stopping {
					state = svc.StopPending
				}
				s <- svc.Status{State: state, Accepts: svc.AcceptStop, CheckPoint: 0}
				h.logf(slog.LevelInfo, "service: interrogate: "+detail)
			case svc.Stop, svc.Shutdown:
				if stopping {
					continue
				}
				stopping = true
				if running {
					// D6.2: SERVICE_STOP_PENDING + dwWaitHint 30 s. The same
					// unchanged 30 s application shutdown path (appCtx cancel
					// → ShutdownWithPersistence → audit close) then runs.
					s <- svc.Status{State: svc.StopPending, CheckPoint: 2, WaitHint: 30000}
					h.logf(slog.LevelInfo, "service: stop requested, executing application shutdown")
				} else {
					h.logf(slog.LevelInfo, "service: stop requested before HTTP bind")
				}
				cancel()
			}
		case err := <-appErr:
			// The application exited: a pre-bind failure, a clean shutdown
			// after a stop request, or a server failure on its own.
			switch {
			case !running && stopping:
				h.logf(slog.LevelInfo, "service: stopped before HTTP bind")
			case !running:
				h.logf(slog.LevelError, "service: startup failed before HTTP bind: "+describeErr(err))
			case !stopping:
				h.logf(slog.LevelError, "service: application exited: "+describeErr(err))
			}
			s <- svc.Status{State: svc.Stopped, Win32ExitCode: h.exitCodeOf(err)}
			h.logf(slog.LevelInfo, "service: stopped")
			return false, h.exitCodeOf(err)
		}
	}
}

func (h *serviceHandler) exitCodeOf(err error) uint32 {
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	return 1
}

// watchBind polls the listen address until it accepts connections (the HTTP
// server has bound) or the application context is cancelled / the application
// has exited. It sends on bound exactly once, on a successful bind.
func (h *serviceHandler) watchBind(ctx context.Context, appDone <-chan struct{}, bound chan<- struct{}) {
	d := net.Dialer{Timeout: 250 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c, err := d.DialContext(ctx, "tcp", h.opts.ServeAddr); err == nil {
			_ = c.Close()
			bound <- struct{}{}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-appDone:
			return
		case <-ticker.C:
		}
	}
}

func describeErr(err error) string {
	if err == nil {
		return "clean shutdown"
	}
	return err.Error()
}

// RunService blocks as the SCM service (goal --service run, ADR 011 D1.2).
// It is valid only under an SCM session; outside one it returns a bounded
// error and never starts a UI, never reads stdin, never installs itself.
func (m *windowsServiceManager) RunService(opts ServiceRunOptions) error {
	if !m.IsService() {
		return errors.New("service: not running under the Service Control Manager; --service run is an internal SCM entrypoint only")
	}
	logger, err := newEventLogger(serviceEventSource)
	if err != nil {
		return fmt.Errorf("service: open event log source %q: %w", serviceEventSource, err)
	}
	defer logger.close()

	// D8.1: in service mode operational slog output goes to the Event Log
	// (Application, source GoAl). ADR 007 audit is untouched and never
	// mirrored (D8.2).
	slog.SetDefault(slog.New(newSlogEventHandler(logger)))

	h := &serviceHandler{opts: opts, log: logger}
	return svc.Run(opts.Name, h)
}

// --- management verbs (ADR 011 D5/D6/D7/D9) ---

// serviceHandle is the SCM service-handle surface used by the management
// verbs. The production implementation is *mgr.Service (it satisfies the
// interface); tests inject a fake to verify the verb flow without an SCM.
type serviceHandle interface {
	Query() (svc.Status, error)
	Start(args ...string) error
	Control(cmd svc.Cmd) (svc.Status, error)
	Close() error
}

// serviceOpener opens a service handle for a management verb.
type serviceOpener func(name string) (serviceHandle, error)

// stateWaiter polls the SCM until the service reaches the wanted state or the
// budget is missed (bounded error either way).
type stateWaiter func(name string, want svc.State, budget time.Duration, op string) error

// openSCMService connects to the SCM and opens the named service with bounded
// diagnostics (not-found is a distinct, bounded error).
func openSCMService(name string) (serviceHandle, error) {
	mgrConn, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("service: connect to SCM: %w", err)
	}
	defer mgrConn.Disconnect()
	svcHandle, err := mgrConn.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, fmt.Errorf("service: %q not found", name)
		}
		return nil, fmt.Errorf("service: open %q: %w", name, err)
	}
	return svcHandle, nil
}

// Install registers the service without starting it (ADR 011 D5). An existing
// registration with an identical image is an idempotent no-op success; a
// different image (exe, config, or arguments) is refused with the bounded
// diff (acceptance item 2).
func (m *windowsServiceManager) Install(req InstallRequest) error {
	timeout := req.StopTimeout
	if timeout == 0 {
		timeout = DefaultStopTimeout
	}
	mgrConn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect to SCM: %w", err)
	}
	defer mgrConn.Disconnect()

	startType := uint32(mgr.StartAutomatic)
	if req.StartType == StartTypeManual {
		startType = mgr.StartManual
	}
	cfg := mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        startType,
		ErrorControl:     mgr.ErrorNormal,
		DisplayName:      req.DisplayName,
		ServiceStartName: localSystemAccount,
		Description:      req.Description,
	}

	want := serviceBinPath(req.ExePath, req.ConfigPath)
	svcHandle, err := mgrConn.OpenService(req.Name)
	if err != nil {
		if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return fmt.Errorf("service: open %q: %w", req.Name, err)
		}
		svcHandle, err = mgrConn.CreateService(req.Name, req.ExePath, cfg, "--service", "run", "--config", req.ConfigPath)
		if err != nil {
			return fmt.Errorf("service: create %q: %w", req.Name, err)
		}
	} else {
		existing, err := svcHandle.Config()
		if err != nil {
			svcHandle.Close()
			return fmt.Errorf("service: query existing config for %q: %w", req.Name, err)
		}
		if existing.BinaryPathName == want {
			svcHandle.Close()
			return nil // identical image: idempotent no-op success
		}
		svcHandle.Close()
		return fmt.Errorf("service: %q is already registered with a different image: existing %q, requested %q; run --service uninstall first", req.Name, existing.BinaryPathName, want)
	}
	defer svcHandle.Close()

	if err := setServiceStopTimeout(req.Name, timeout); err != nil {
		return fmt.Errorf("service: set stop timeout for %q: %w", req.Name, err)
	}
	return nil
}

// Uninstall stops a running service via the graceful D6.2 path before
// deleting the registration (ADR 011 D9). It never touches user data.
func (m *windowsServiceManager) Uninstall(name string) error {
	if err := m.Stop(name); err != nil {
		return err
	}
	mgrConn, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect to SCM: %w", err)
	}
	defer mgrConn.Disconnect()
	svcHandle, err := mgrConn.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return fmt.Errorf("service: %q not found", name)
		}
		return fmt.Errorf("service: open %q: %w", name, err)
	}
	defer svcHandle.Close()
	if err := svcHandle.Delete(); err != nil {
		return fmt.Errorf("service: delete %q: %w", name, err)
	}
	return nil
}

func (m *windowsServiceManager) Start(name string) error {
	return m.start(name, openSCMService, m.waitForState)
}

// start requests the SCM start and waits for Running (ADR 011 D6.5). An
// already-running service is a bounded no-op success.
func (m *windowsServiceManager) start(name string, open serviceOpener, wait stateWaiter) error {
	h, err := open(name)
	if err != nil {
		return err
	}
	defer h.Close()
	if err := h.Start(); err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return nil // already running: bounded no-op
		}
		return fmt.Errorf("service: start %q: %w", name, err)
	}
	return wait(name, svc.Running, 90*time.Second, "start")
}

func (m *windowsServiceManager) Stop(name string) error {
	return m.stop(name, openSCMService, m.waitForState)
}

// stop requests a graceful SCM stop and waits for Stopped (ADR 011 D6.2). The
// wait budget (60 s) stays strictly greater than the registered 45 s SCM stop
// timeout (D6.3): the SCM hard-kills at the registered timeout, so the
// observer must outlive it to see the final Stopped state (D7's bounded
// stop-wait, realized with that margin). An already-stopped service is a
// bounded no-op success.
func (m *windowsServiceManager) stop(name string, open serviceOpener, wait stateWaiter) error {
	h, err := open(name)
	if err != nil {
		return err
	}
	defer h.Close()
	st, err := h.Query()
	if err != nil {
		return fmt.Errorf("service: query %q: %w", name, err)
	}
	if st.State == svc.Stopped {
		return nil // already stopped: bounded no-op
	}
	if _, err := h.Control(svc.Stop); err != nil {
		return fmt.Errorf("service: stop %q: %w", name, err)
	}
	return wait(name, svc.Stopped, DefaultStopTimeout+15*time.Second, "stop")
}

// Restart performs Stop → wait Stopped → Start → wait Running (ADR 011 D7).
// No self-reexec, no second process.
func (m *windowsServiceManager) Restart(name string) error {
	return m.restart(name, openSCMService, m.waitForState)
}

func (m *windowsServiceManager) restart(name string, open serviceOpener, wait stateWaiter) error {
	if err := m.stop(name, open, wait); err != nil {
		return err
	}
	return m.start(name, open, wait)
}

// Status returns the SCM state, PID and uptime (ADR 011 D6.5). It reports no
// secrets and no instance detail — instance state is the Web UI's job.
func (m *windowsServiceManager) Status(name string) (ServiceStatus, error) {
	mgrConn, err := mgr.Connect()
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("service: connect to SCM: %w", err)
	}
	defer mgrConn.Disconnect()
	svcHandle, err := mgrConn.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return ServiceStatus{}, fmt.Errorf("service: %q not found", name)
		}
		return ServiceStatus{}, fmt.Errorf("service: open %q: %w", name, err)
	}
	defer svcHandle.Close()
	st, err := svcHandle.Query()
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("service: query %q: %w", name, err)
	}
	out := ServiceStatus{
		State:     stateName(st.State),
		PID:       st.ProcessId,
		Win32Exit: st.Win32ExitCode,
	}
	if st.State == svc.Running && st.ProcessId != 0 {
		out.Uptime = serviceProcessUptime(st.ProcessId)
	}
	return out, nil
}

// serviceProcessUptime derives the service uptime from the service process
// creation time. The supported x/sys status contract (svc.Status, filled from
// SERVICE_STATUS / SERVICE_STATUS_PROCESS via windows.QueryServiceStatusEx)
// carries the PID but no start time, so the creation time is read from the
// process itself.
// Bounded: it returns 0 — never an error or a panic — when the process cannot
// be opened (access rights, or the process exited between the status query
// and the open) or the times cannot be read.
func serviceProcessUptime(pid uint32) time.Duration {
	if pid == 0 {
		return 0
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	start := time.Unix(0, creation.Nanoseconds())
	uptime := time.Since(start)
	if uptime < 0 {
		return 0
	}
	return uptime
}

func (m *windowsServiceManager) waitForState(name string, want svc.State, budget time.Duration, op string) error {
	deadline := time.Now().Add(budget)
	var last string
	for time.Now().Before(deadline) {
		st, err := m.Status(name)
		if err != nil {
			return err
		}
		last = st.State
		if stateName(want) == st.State {
			return nil
		}
		if op == "start" && st.State == stateName(svc.Stopped) {
			// The service reached a terminal Stopped state: a start failure.
			return fmt.Errorf("service: %q did not reach Running; SCM reports Stopped (win32 exit %d)", name, st.Win32Exit)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("service: %q did not reach %s within %s (last observed: %s)", name, stateName(want), budget, last)
}

var stateNames = map[svc.State]string{
	svc.Stopped:         "Stopped",
	svc.StartPending:    "StartPending",
	svc.StopPending:     "StopPending",
	svc.Running:         "Running",
	svc.ContinuePending: "ContinuePending",
	svc.PausePending:    "PausePending",
	svc.Paused:          "Paused",
}

func stateName(st svc.State) string {
	if n, ok := stateNames[st]; ok {
		return n
	}
	return fmt.Sprintf("State(%d)", uint32(st))
}

// setServiceStopTimeout registers the SCM stop timeout (ADR 011 D6.3) via the
// per-service registry value; the SCM config API has no field for it.
func setServiceStopTimeout(name string, timeout time.Duration) error {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetDWordValue("StopTimeout", uint32(timeout.Seconds()))
}

// --- Event Log (ADR 011 D8) ---

type eventLogger struct {
	source windows.Handle
}

func newEventLogger(sourceName string) (*eventLogger, error) {
	sourcePtr, err := windows.UTF16PtrFromString(sourceName)
	if err != nil {
		return nil, err
	}
	source, err := windows.RegisterEventSource(nil, sourcePtr)
	if err != nil {
		return nil, err
	}
	return &eventLogger{source: source}, nil
}

func (l *eventLogger) logf(level slog.Level, msg string) {
	if l == nil || l.source == 0 {
		return
	}
	var etype uint16
	switch {
	case level >= slog.LevelError:
		etype = windows.EVENTLOG_ERROR_TYPE
	case level >= slog.LevelWarn:
		etype = windows.EVENTLOG_WARNING_TYPE
	default:
		etype = windows.EVENTLOG_INFORMATION_TYPE
	}
	str, err := windows.UTF16PtrFromString(msg)
	if err != nil {
		return
	}
	_ = windows.ReportEvent(l.source, etype, 0, 0, 0, 1, 0, &str, nil)
}

func (l *eventLogger) close() {
	if l == nil {
		return
	}
	if l.source != 0 {
		windows.DeregisterEventSource(l.source)
		l.source = 0
	}
}

// slogEventHandler routes operational slog output to the Event Log in service
// mode (ADR 011 D8.1). ADR 007 audit events are never mirrored (D8.2).
//
// The traditional ReportEvent API carries a single string payload. This
// handler serializes the slog message and all structured attributes into that
// payload in a deterministic, bounded form: "message key1=val1 key2=val2".
// Group attrs are prefixed ("group.key=value"). The payload is truncated to
// eventLogPayloadLimit bytes to stay within the bounded diagnostic contract.
type slogEventHandler struct {
	log   *eventLogger
	mu    sync.Mutex
	attrs []slog.Attr
	group string
}

func newSlogEventHandler(l *eventLogger) *slogEventHandler {
	return &slogEventHandler{log: l}
}

func (h *slogEventHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

const eventLogPayloadLimit = 1000

func (h *slogEventHandler) Handle(_ context.Context, r slog.Record) error {
	msg := h.formatRecord(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log.logf(r.Level, msg)
	return nil
}

// formatRecord serializes a slog.Record into the single bounded Event Log
// payload: "message key1=val1 key2=val2".
func (h *slogEventHandler) formatRecord(r slog.Record) string {
	var sb strings.Builder
	sb.WriteString(r.Message)

	appendAttr := func(a slog.Attr) {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		sb.WriteByte(' ')
		sb.WriteString(key)
		sb.WriteByte('=')
		writeSlogValue(&sb, a.Value)
	}

	for _, a := range h.attrs {
		appendAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(a)
		return true
	})

	msg := sb.String()
	if len(msg) > eventLogPayloadLimit {
		msg = msg[:eventLogPayloadLimit]
	}
	return msg
}

func (h *slogEventHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &slogEventHandler{log: h.log, attrs: merged, group: h.group}
}

func (h *slogEventHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &slogEventHandler{log: h.log, attrs: h.attrs, group: g}
}

// writeSlogValue writes a slog.Value in a deterministic text form.
func writeSlogValue(sb *strings.Builder, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		sb.WriteString(v.String())
	case slog.KindBool:
		if v.Bool() {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case slog.KindInt64:
		sb.WriteString(v.String())
	case slog.KindUint64:
		sb.WriteString(v.String())
	case slog.KindFloat64:
		sb.WriteString(v.String())
	case slog.KindDuration:
		sb.WriteString(v.Duration().String())
	case slog.KindTime:
		sb.WriteString(v.Time().UTC().Format(time.RFC3339))
	case slog.KindLogValuer:
		sb.WriteString(v.Resolve().String())
	default:
		sb.WriteString(v.String())
	}
}
