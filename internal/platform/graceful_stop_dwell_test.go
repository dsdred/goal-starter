//go:build windows || linux

package platform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

const (
	// fixtureExitDelayMS is deliberately different from the fixture default so
	// the test proves the delay is configurable.
	fixtureExitDelayMS = 2500

	markerPollInterval = 10 * time.Millisecond
	markerWaitTimeout  = 10 * time.Second
)

type dwellMarker struct {
	Event       string `json:"event"`
	PID         int    `json:"pid"`
	Executable  string `json:"executable"`
	Signal      string `json:"signal"`
	ExitDelayMS int    `json:"exit_delay_ms"`
	Timestamp   string `json:"timestamp"`
}

type dwellFixture struct {
	cmd       *exec.Cmd
	ctrl      ProcessControl
	exePath   string
	markerDir string
	done      chan error
	exited    bool
}

func startDwellFixture(t *testing.T, exitDelayMS int) *dwellFixture {
	t.Helper()

	exePath := fakeruntime.Path(t)
	markerDir := t.TempDir()
	args := []string{"signal-delay", markerDir}
	if exitDelayMS >= 0 {
		args = append(args, strconv.Itoa(exitDelayMS))
	}

	cmd := exec.Command(exePath, args...)
	ctrl, err := prepare(cmd)
	if err != nil {
		t.Fatalf("prepare fake runtime: %v", err)
	}
	if err := cmd.Start(); err != nil {
		_ = ctrl.Close()
		t.Fatalf("start fake runtime: %v", err)
	}

	f := &dwellFixture{
		cmd:       cmd,
		ctrl:      ctrl,
		exePath:   exePath,
		markerDir: markerDir,
		done:      make(chan error, 1),
	}
	go func() { f.done <- cmd.Wait() }()
	t.Cleanup(f.stop)

	if err := ctrl.AfterStart(cmd.Process.Pid); err != nil {
		t.Fatalf("after start %d: %v", cmd.Process.Pid, err)
	}
	return f
}

func (f *dwellFixture) stop() {
	if !f.exited {
		_ = f.ctrl.ForceKill()
		// ForceKill cannot reach a process that was never assigned to the Job
		// Object (AfterStart failure); keep the fixture from leaking in that
		// case.
		if f.cmd.Process != nil {
			_ = f.cmd.Process.Kill()
		}
		select {
		case <-f.done:
		case <-time.After(15 * time.Second):
		}
		f.exited = true
	}
	_ = f.ctrl.Close()
}

func (f *dwellFixture) waitExit(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-f.done:
		f.exited = true
		return err
	case <-time.After(timeout):
		t.Fatalf("process %d did not exit within %s", f.cmd.Process.Pid, timeout)
		return nil
	}
}

func (f *dwellFixture) markerPath(name string) string {
	return filepath.Join(f.markerDir, name)
}

func waitForMarker(t *testing.T, path string, timeout time.Duration) dwellMarker {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var marker dwellMarker
			if unmarshalErr := json.Unmarshal(data, &marker); unmarshalErr == nil {
				return marker
			} else {
				lastErr = unmarshalErr
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker %s not readable within %s: %v", path, timeout, lastErr)
		}
		time.Sleep(markerPollInterval)
	}
}

func assertSameExecutable(t *testing.T, reported, expected string) {
	t.Helper()
	if reported == "" {
		t.Fatalf("process reported an empty executable path; expected %q", expected)
	}
	if reported == expected {
		return
	}
	reportedInfo, reportedErr := os.Stat(reported)
	expectedInfo, expectedErr := os.Stat(expected)
	if reportedErr == nil && expectedErr == nil && os.SameFile(reportedInfo, expectedInfo) {
		return
	}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(reported), filepath.Clean(expected)) {
		return
	}
	t.Fatalf("process executable = %q, want %q", reported, expected)
}

func assertSameStartTime(t *testing.T, before, during ProcessIdentity) {
	t.Helper()
	if !before.HasStartTime || !during.HasStartTime {
		t.Fatalf("process identity has no start time (before=%v during=%v)", before.HasStartTime, during.HasStartTime)
	}
	if !before.StartTime.Equal(during.StartTime) {
		t.Fatalf("process start time changed: %s -> %s", before.StartTime, during.StartTime)
	}
}

func parseMarkerTime(t *testing.T, marker dwellMarker) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, marker.Timestamp)
	if err != nil {
		t.Fatalf("parse %s marker timestamp %q: %v", marker.Event, marker.Timestamp, err)
	}
	return parsed
}

// TestSignalDelayFixture_GracefulDwell exercises the maintained signal-delay
// fixture through the production ProcessControl path: a real OS stop signal is
// received, independently recorded by the child, the process stays alive for
// the configured delay, and then exits 0 without a ForceKill.
func TestSignalDelayFixture_GracefulDwell(t *testing.T) {
	f := startDwellFixture(t, fixtureExitDelayMS)

	ready := waitForMarker(t, f.markerPath("ready.json"), markerWaitTimeout)
	if ready.Event != "ready" {
		t.Fatalf("ready marker event = %q, want %q", ready.Event, "ready")
	}
	if ready.PID != f.cmd.Process.Pid {
		t.Fatalf("ready marker pid = %d, want %d", ready.PID, f.cmd.Process.Pid)
	}
	assertSameExecutable(t, ready.Executable, f.exePath)
	readyTime := parseMarkerTime(t, ready)

	prober := NewRecoveryProber()
	before, err := prober.GetProcessIdentity(ready.PID)
	if err != nil {
		t.Fatalf("identity before stop: %v", err)
	}
	assertSameExecutable(t, before.ExecutablePath, f.exePath)

	stopAt := time.Now()
	if err := f.ctrl.GracefulStop(); err != nil {
		t.Fatalf("GracefulStop: %v", err)
	}
	if f.ctrl.WasSignaled() {
		_ = f.waitExit(t, 15*time.Second)
		if runtime.GOOS != "windows" {
			t.Fatalf("GracefulStop used ForceKill after %s on %s", time.Since(stopAt), runtime.GOOS)
		}
		if testConsoleAttached() {
			t.Fatalf("GracefulStop escalated to ForceKill after %s although the test process is attached to a console", time.Since(stopAt))
		}
		t.Skipf("GracefulStop escalated to ForceKill after %s: the test process has no console, so no console control event could be delivered and the real signal path could not be exercised", time.Since(stopAt))
	}

	received := waitForMarker(t, f.markerPath("signal.json"), 5*time.Second)
	if received.Event != "signal" {
		t.Fatalf("signal marker event = %q, want %q", received.Event, "signal")
	}
	if received.PID != f.cmd.Process.Pid {
		t.Fatalf("signal marker pid = %d, want %d", received.PID, f.cmd.Process.Pid)
	}
	if received.ExitDelayMS != fixtureExitDelayMS {
		t.Fatalf("signal marker exit_delay_ms = %d, want %d", received.ExitDelayMS, fixtureExitDelayMS)
	}
	assertSameExecutable(t, received.Executable, f.exePath)
	signalTime := parseMarkerTime(t, received)
	if signalTime.Before(readyTime) {
		t.Fatalf("signal marker timestamp %s precedes ready marker timestamp %s", signalTime, readyTime)
	}

	wantSignal := "terminated"
	if runtime.GOOS == "windows" {
		wantSignal = "interrupt"
	}
	if received.Signal != wantSignal {
		t.Fatalf("signal marker signal = %q, want %q", received.Signal, wantSignal)
	}

	alive, err := prober.IsProcessAlive(received.PID)
	if err != nil {
		t.Fatalf("liveness during dwell: %v", err)
	}
	if !alive {
		t.Fatalf("process %d was not alive after acknowledging the stop signal", received.PID)
	}
	during, err := prober.GetProcessIdentity(received.PID)
	if err != nil {
		t.Fatalf("identity during dwell: %v", err)
	}
	assertSameExecutable(t, during.ExecutablePath, f.exePath)
	assertSameStartTime(t, before, during)

	waitErr := f.waitExit(t, 15*time.Second)
	if waitErr != nil {
		t.Fatalf("process did not exit 0 after the graceful dwell: %v", waitErr)
	}
	elapsed := time.Since(stopAt)
	if elapsed < fixtureExitDelayMS*time.Millisecond-300*time.Millisecond {
		t.Fatalf("process exited after %s, before the configured %d ms graceful dwell", elapsed, fixtureExitDelayMS)
	}
	if f.ctrl.WasSignaled() {
		t.Fatalf("process was force-killed during a graceful shutdown")
	}
}

// TestSignalDelayFixture_DefaultDwell verifies the measured default behavior:
// with no delay argument the fixture holds for ~4000 ms after a real stop
// signal and then exits 0.
func TestSignalDelayFixture_DefaultDwell(t *testing.T) {
	f := startDwellFixture(t, -1)

	ready := waitForMarker(t, f.markerPath("ready.json"), markerWaitTimeout)
	if ready.Event != "ready" {
		t.Fatalf("ready marker event = %q, want %q", ready.Event, "ready")
	}
	if ready.PID != f.cmd.Process.Pid {
		t.Fatalf("ready marker pid = %d, want %d", ready.PID, f.cmd.Process.Pid)
	}
	assertSameExecutable(t, ready.Executable, f.exePath)

	prober := NewRecoveryProber()
	before, err := prober.GetProcessIdentity(ready.PID)
	if err != nil {
		t.Fatalf("identity before stop: %v", err)
	}
	assertSameExecutable(t, before.ExecutablePath, f.exePath)

	stopAt := time.Now()
	if err := f.ctrl.GracefulStop(); err != nil {
		t.Fatalf("GracefulStop: %v", err)
	}
	if f.ctrl.WasSignaled() {
		_ = f.waitExit(t, 15*time.Second)
		if runtime.GOOS != "windows" {
			t.Fatalf("GracefulStop used ForceKill after %s on %s", time.Since(stopAt), runtime.GOOS)
		}
		if testConsoleAttached() {
			t.Fatalf("GracefulStop escalated to ForceKill after %s although the test process is attached to a console", time.Since(stopAt))
		}
		t.Skipf("GracefulStop escalated to ForceKill after %s: the test process has no console, so no console control event could be delivered and the real signal path could not be exercised", time.Since(stopAt))
	}

	received := waitForMarker(t, f.markerPath("signal.json"), 5*time.Second)
	if received.Event != "signal" {
		t.Fatalf("signal marker event = %q, want %q", received.Event, "signal")
	}
	if received.ExitDelayMS != 4000 {
		t.Fatalf("default exit_delay_ms = %d, want 4000", received.ExitDelayMS)
	}

	checkpointAt := stopAt.Add(3500 * time.Millisecond)
	if wait := time.Until(checkpointAt); wait > 0 {
		time.Sleep(wait)
	}
	alive, err := prober.IsProcessAlive(ready.PID)
	if err != nil {
		t.Fatalf("liveness at the 3500 ms checkpoint: %v", err)
	}
	if !alive {
		t.Fatalf("process %d was not alive at the 3500 ms checkpoint", ready.PID)
	}
	during, err := prober.GetProcessIdentity(ready.PID)
	if err != nil {
		t.Fatalf("identity at the 3500 ms checkpoint: %v", err)
	}
	assertSameExecutable(t, during.ExecutablePath, f.exePath)
	assertSameStartTime(t, before, during)

	waitErr := f.waitExit(t, 20*time.Second)
	if waitErr != nil {
		t.Fatalf("process did not exit 0 after the default graceful dwell: %v", waitErr)
	}
	elapsed := time.Since(stopAt)
	if elapsed < 3500*time.Millisecond {
		t.Fatalf("process exited after %s, before the default 4000 ms graceful dwell", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("process took %s to exit with a 4000 ms configured dwell", elapsed)
	}
	if f.ctrl.WasSignaled() {
		t.Fatalf("process was force-killed during a graceful shutdown")
	}
}
