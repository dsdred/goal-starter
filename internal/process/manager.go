package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/dsdred/goal/internal/platform"
)

// State represents the lifecycle state of a managed process.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateExited   State = "exited"
	StateFailed   State = "failed"
)

// ExitClass describes why the process ended.
type ExitClass string

const (
	ExitNormal   ExitClass = "normal"   // clean exit (exit code 0)
	ExitFailure  ExitClass = "failure"  // non-zero exit code
	ExitKilled   ExitClass = "killed"   // force-killed by manager or OS
	ExitTimeout  ExitClass = "timeout"  // did not stop within timeout
	ExitContext  ExitClass = "context"  // parent context cancelled
	ExitError    ExitClass = "error"    // exec or OS error during start/wait
	ExitSignaled ExitClass = "signaled" // terminated by signal
)

// CommandSpec defines how to launch a managed process.
// Platform-specific setup (SysProcAttr, Job Objects, process groups) is done
// via platform.Prepare(cmd) — callers do not need to set SysProcAttr here.
type CommandSpec struct {
	Executable       string
	Args             []string
	WorkingDirectory string
	Environment      []string
}

// Status holds the current status of the managed process.
type Status struct {
	State     State     `json:"state"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	ExitCode  *int      `json:"exitCode,omitempty"`
	ExitClass ExitClass `json:"exitClass,omitempty"`
	LastError string    `json:"lastError,omitempty"`
}

// LogEvent carries a line from a process stream.
type LogEvent struct {
	Sequence uint64    `json:"sequence,omitempty"`
	Time     time.Time `json:"time"`
	Stream   string    `json:"stream"`
	Message  string    `json:"message"`
}

// Manager owns the lifecycle of exactly one managed process.
type Manager struct {
	mu       sync.RWMutex
	status   Status
	cmd      *exec.Cmd
	control  platform.ProcessControl
	done     chan struct{}
	logSubs  map[chan LogEvent]struct{}
	logStore *LogStore
}

// NewManager creates a Manager already in the stopped state.
func NewManager() *Manager {
	return &Manager{
		status:   Status{State: StateStopped},
		logSubs:  make(map[chan LogEvent]struct{}),
		logStore: NewLogStore(10000),
	}
}

// NewManagerWithLogStore creates a Manager with a custom LogStore.
func NewManagerWithLogStore(logStore *LogStore) *Manager {
	return &Manager{
		status:   Status{State: StateStopped},
		logSubs:  make(map[chan LogEvent]struct{}),
		logStore: logStore,
	}
}

// Start launches the process described by spec.
// It merges spec.Environment with the parent process environment so that
// user-provided variables never silently overwrite system variables.
func (m *Manager) Start(ctx context.Context, spec CommandSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return errors.New("a process is already running")
	}
	if spec.Executable == "" {
		return errors.New("executable is required")
	}

	// Validate executable exists.
	exePath := spec.Executable
	if !filepath.IsAbs(exePath) && spec.WorkingDirectory != "" {
		exePath = filepath.Join(spec.WorkingDirectory, exePath)
	}
	if _, err := os.Stat(exePath); err != nil {
		return fmt.Errorf("executable does not exist: %s: %w", exePath, err)
	}

	// Validate working directory.
	if spec.WorkingDirectory != "" {
		if info, err := os.Stat(spec.WorkingDirectory); err != nil || !info.IsDir() {
			return fmt.Errorf("working directory does not exist: %s: %w", spec.WorkingDirectory, err)
		}
	}

	// Merge environment: parent env first, then user vars (user vars override).
	env := mergeEnvironment(spec.Environment)

	// Use exec.Command (not CommandContext) — the manager owns process lifecycle
	// via platform.ProcessControl (Job Objects on Windows, process groups on Linux).
	// Parent context is used only for Start() operation timeout.
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Dir = spec.WorkingDirectory
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// platform.Prepare sets platform-specific attributes (Job Object on Windows,
	// process group on Linux) and returns a ProcessControl handle.
	control, err := platform.Prepare(cmd)
	if err != nil {
		return err
	}

	m.status = Status{State: StateStarting}

	if err := cmd.Start(); err != nil {
		m.status = Status{
			State:     StateFailed,
			ExitClass: ExitError,
			LastError: err.Error(),
		}
		return err
	}

	if err := control.AfterStart(cmd.Process.Pid); err != nil {
		// Kill the process and wait to ensure resources are released.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = control.Close()
		m.status = Status{
			State:     StateFailed,
			ExitClass: ExitError,
			LastError: err.Error(),
		}
		return err
	}

	m.cmd = cmd
	m.control = control
	m.done = make(chan struct{})
	m.status = Status{
		State:     StateRunning,
		PID:       cmd.Process.Pid,
		StartedAt: time.Now(),
	}

	go m.readPipe("stdout", stdout)
	go m.readPipe("stderr", stderr)
	go m.wait(cmd, control)
	return nil
}

// Stop requests a graceful shutdown and waits until the process exits
// or the deadline passes (then force-kills).
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd == nil {
		m.mu.Unlock()
		return nil
	}
	done := m.done
	control := m.control
	m.status.State = StateStopping
	m.mu.Unlock()

	// Send SIGTERM to process group / job.
	gracefulErr := control.GracefulStop()
	if gracefulErr != nil {
		m.publish(LogEvent{
			Time:    time.Now(),
			Stream:  "system",
			Message: fmt.Sprintf("graceful stop signal failed: %v", gracefulErr),
		})
	}

	// Wait for process to exit or force-kill after timeout.
	var forceErr error
	select {
	case <-done:
		// Process exited gracefully. Return graceful error if present.
		return gracefulErr
	case <-time.After(5 * time.Second):
		// 5-second hard timeout (independent of ctx) to ensure process is killed.
		forceErr = control.ForceKill()
		if forceErr != nil && forceErr != syscall.ESRCH {
			m.publish(LogEvent{
				Time:    time.Now(),
				Stream:  "system",
				Message: fmt.Sprintf("force kill failed: %v", forceErr),
			})
		}

		// Wait for process to fully terminate after force kill.
		select {
		case <-done:
			// Return the more informative error.
			if gracefulErr != nil {
				return gracefulErr
			}
			return forceErr
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status returns a snapshot of the current managed process status.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Subscribe creates a channel for live log events.
// The caller MUST close the returned cleanup function.
func (m *Manager) Subscribe() (<-chan LogEvent, func()) {
	ch := make(chan LogEvent, 64)
	m.mu.Lock()
	m.logSubs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.logSubs, ch)
		close(ch)
		m.mu.Unlock()
	}
}

// GetDoneChannel returns the manager's done channel for monitoring.
func (m *Manager) GetDoneChannel() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.done
}

// Control returns the platform.ProcessControl for this manager.
// Returns nil if manager is not started.
func (m *Manager) Control() platform.ProcessControl {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.control
}

// wait is the SINGLE owner of cmd.Wait(). It runs in its own goroutine.
func (m *Manager) wait(cmd *exec.Cmd, control platform.ProcessControl) {
	err := cmd.Wait()

	// Read exit code BEFORE closing platform resources (Job Object on Windows,
	// process group on Linux). On Windows, closing the Job Object can invalidate
	// ProcessState._ExitCode, causing GetExitCodeProcess() to return stale/garbage values.
	var exitCodePtr *int
	if cmd.ProcessState != nil {
		exitCodePtr = exitCodeFromProcessState(cmd.ProcessState)
	}

	// Close platform resources (job object, process group).
	_ = control.Close()

	exitClass := ExitNormal

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			exitClass = ExitContext
		} else if err.Error() == "signal: killed" || err == exec.ErrNotFound {
			exitClass = ExitKilled
		} else {
			exitClass = ExitError
		}
		m.publish(LogEvent{
			Time:    time.Now(),
			Stream:  "system",
			Message: fmt.Sprintf("wait error: %v", err),
		})
	}

	// Signal-based termination takes priority over exit code classification.
	// On Windows, Job Object kill-on-close may return exit code 0.
	if control.WasSignaled() {
		exitClass = ExitSignaled
	} else if exitCodePtr != nil && *exitCodePtr == 0 {
		exitClass = ExitNormal
	} else if exitCodePtr != nil && *exitCodePtr != 0 {
		exitClass = ExitFailure
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Only update status if we're still the active process.
	if m.cmd == cmd {
		m.status.State = StateExited
		m.status.ExitCode = exitCodePtr
		m.status.ExitClass = exitClass
		if err != nil && m.status.LastError == "" {
			m.status.LastError = err.Error()
		}
		m.cmd = nil
		m.control = nil
	}

	close(m.done)
}

// exitCodeFromProcessState extracts the exit code from *os.ProcessState
// and returns a pointer to it, allocated on the heap.
//
// Heap allocation is required because the returned pointer is stored in
// m.status.ExitCode and outlives the wait() goroutine's stack frame.
func exitCodeFromProcessState(state *os.ProcessState) *int {
	if state == nil {
		return nil
	}
	code := state.ExitCode()
	p := new(int)
	*p = code
	return p
}

// mergeEnvironment merges user-provided environment variables with the
// parent process environment. User variables take precedence (overwrite).
func mergeEnvironment(userEnv []string) []string {
	// Start with parent environment.
	parentEnv := os.Environ()

	if len(userEnv) == 0 {
		return parentEnv
	}

	// Build a map of user-provided variables.
	overrides := make(map[string]string)
	for _, ev := range userEnv {
		idx := -1
		for i, c := range ev {
			if c == '=' {
				idx = i
				break
			}
			if c == ';' || c == ' ' {
				break
			}
		}
		if idx > 0 {
			key := ev[:idx]
			val := ev[idx+1:]
			overrides[key] = val
		}
	}

	// Merge: parent env first, then user overrides.
	result := make([]string, 0, len(parentEnv)+len(overrides))
	parentKeys := make(map[string]bool)

	for _, ev := range parentEnv {
		idx := -1
		for i, c := range ev {
			if c == '=' {
				idx = i
				break
			}
		}
		if idx > 0 {
			key := ev[:idx]
			if override, ok := overrides[key]; ok {
				result = append(result, key+"="+override)
				parentKeys[key] = true
			} else {
				result = append(result, ev)
			}
		} else {
			result = append(result, ev)
		}
	}

	// Add user-only variables (not in parent env).
	for key, val := range overrides {
		if !parentKeys[key] {
			result = append(result, key+"="+val)
		}
	}

	return result
}

// publish sends a log event to all subscribers and stores it in the log store.
// It tracks dropped events and publishes a gap event when messages are lost.
func (m *Manager) publish(ev LogEvent) {
	// Store in log store if available.
	if m.logStore != nil {
		m.logStore.Add(ev)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	dropped := 0
	for ch := range m.logSubs {
		select {
		case ch <- ev:
		default:
			dropped++
		}
	}

	// Publish gap event if any messages were dropped.
	if dropped > 0 {
		gap := LogEvent{
			Time:    time.Now(),
			Stream:  "system",
			Message: fmt.Sprintf("log gap: %d events dropped", dropped),
		}
		if m.logStore != nil {
			m.logStore.Add(gap)
		}
		// Publish gap event to subscribers (non-blocking).
		for ch := range m.logSubs {
			select {
			case ch <- gap:
			default:
			}
		}
	}
}

// GetLogStore returns the log store for querying historical logs.
func (m *Manager) GetLogStore() *LogStore {
	return m.logStore
}

// readPipe reads lines from a process pipe and publishes them as log events.
func (m *Manager) readPipe(stream string, r io.Reader) {
	s := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	s.Buffer(buf, 1024*1024)
	for s.Scan() {
		m.publish(LogEvent{
			Time:    time.Now(),
			Stream:  stream,
			Message: s.Text(),
		})
	}
	if err := s.Err(); err != nil {
		m.publish(LogEvent{
			Time:    time.Now(),
			Stream:  "system",
			Message: fmt.Sprintf("%s scanner error: %v", stream, err),
		})
	}
}
