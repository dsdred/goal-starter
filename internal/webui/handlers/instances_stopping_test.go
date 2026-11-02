package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// ADR 021 §Deterministic test obligations — T-2 HTTP layer (C-11).
//
// These legs carry no stop-gate seam (internal/process owns the held-window
// proofs) and no fabricated response. The window comes from the maintained
// test-only `signal-hold` fixture: the child acknowledges a real OS stop
// signal, records it in signal.json, and then stays alive until this test
// writes release.json. That makes the interval "stopping is published, process
// is alive" a barrier instead of a race.
//
// The ordering that makes a single read sufficient is the production one:
// InstanceController.stop publishes stopping (supervisor.go setStateLocked)
// BEFORE stopCore calls Manager.Stop, and Manager.Stop is the only place the OS
// signal is sent. The child can therefore write signal.json only after the
// transitional state was published, so waiting for signal.json and then reading
// each route exactly once is a happens-before argument, not a poll for state.
// Every deadline below converts a hang into a FAIL; none of them is the
// observation and none produces a SKIP.
//
// Nothing here is browser evidence: a DOM badge is only observable in a
// browser, which is the Slice 4 leg.

const (
	// stoppingHoldSafetyMS bounds the fixture only for a stale manual run. The
	// test holds the child through release.json, never through this clock.
	stoppingHoldSafetyMS = 120000

	stoppingMarkerPollInterval = 5 * time.Millisecond

	// stoppingSignalDeadline bounds the wait for the fixture's real-signal
	// marker. Expiry is a FAIL (a hang converted into a failure), never an
	// inconclusive observation.
	stoppingSignalDeadline = 20 * time.Second

	// stoppingReadStallGuard is the fail-closed bound on one handler call during
	// a held stop. A reader that still acquired the controller lock is released
	// only when Manager.Stop returns, at least the whole hold away; a lock-free
	// view load costs microseconds. The guard sits far below the stall it
	// detects, so it can only ever fail a test, never pass one.
	stoppingReadStallGuard = 1500 * time.Millisecond

	stoppingReleaseDeadline = 10 * time.Second
	stoppingStopCallTimeout = 30 * time.Second

	// stoppingProcessGoneDeadline bounds the OS post-condition after Stop. It
	// exists to turn a never-reaped process into a FAIL; it never widens a
	// window or substitutes for an observation.
	stoppingProcessGoneDeadline = 10 * time.Second
)

// lifecycleMarker mirrors the maintained fixture's marker JSON.
type lifecycleMarker struct {
	Event      string `json:"event"`
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Signal     string `json:"signal,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// stoppingHandlerEnv owns one real supervisor, the instances handler built on
// it, and one launched instance.
type stoppingHandlerEnv struct {
	h            *InstancesHandler
	svc          *application.InstanceService
	sup          *process.Supervisor
	exePath      string
	readyPath    string
	signalPath   string
	releasePath  string
	releasedPath string
	instance     *domain.LaunchInstance
	markerPID    int
	stopDone     chan struct{}
	stopErr      error
	started      bool
	released     bool
}

func newStoppingHandlerEnv(t *testing.T) *stoppingHandlerEnv {
	t.Helper()

	repo := newTestRepo(t)
	fakePath := fakeruntime.Path(t)
	markerDir := t.TempDir()

	if err := repo.CreateRuntime(&storage.RuntimeEntry{
		ID: "rt-stopping", Name: "stopping-runtime", Executable: fakePath,
	}); err != nil {
		t.Fatalf("CreateRuntime: %v", err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{
		ID: "m-stopping", Name: "stopping-model", RuntimeID: "rt-stopping",
		Args: []string{"signal-hold", markerDir, strconv.Itoa(stoppingHoldSafetyMS)},
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	sup := process.NewSupervisorWithConfig(newMockInstanceStoreForTest(),
		process.SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	svc := application.NewInstanceService(sup, repo)
	env := &stoppingHandlerEnv{
		h:            NewInstancesHandler(svc, nil),
		svc:          svc,
		sup:          sup,
		exePath:      fakePath,
		readyPath:    filepath.Join(markerDir, "ready.json"),
		signalPath:   filepath.Join(markerDir, "signal.json"),
		releasePath:  filepath.Join(markerDir, "release.json"),
		releasedPath: filepath.Join(markerDir, "released.json"),
		stopDone:     make(chan struct{}),
	}

	// The in-flight stop must finish before shutdown: the shared fixture
	// executable stays image-locked while a generation is still being reaped,
	// and a stop that has not returned still owns the controller.
	t.Cleanup(func() {
		if env.started && !env.released {
			// Unblock the held child so cleanup never waits on the manager's
			// force-kill timer. A failure here leaves nothing to assert: the
			// stop-goroutine deadline below reports it.
			_ = writeReleaseMarker(env.releasePath, "cleanup")
		}
		if env.started {
			select {
			case <-env.stopDone:
			case <-time.After(stoppingStopCallTimeout):
				t.Errorf("timeout waiting for the in-flight stop goroutine to finish")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sup.Shutdown(ctx); err != nil {
			t.Errorf("Supervisor.Shutdown: %v", err)
		}
	})

	inst, err := svc.StartModel(context.Background(), "m-stopping")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	env.instance = inst
	if err := waitForSupervisorState(t, sup, inst.ID, domain.InstanceStateRunning, 20*time.Second); err != nil {
		t.Fatalf("instance %s never became running: %v", string(inst.ID), err)
	}
	running, err := svc.GetInstanceStatus(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("GetInstanceStatus: %v", err)
	}
	env.instance = running

	ready := readLifecycleMarker(t, env.readyPath)
	if ready.Event != "ready" {
		t.Fatalf("%s event = %q, want %q", env.readyPath, ready.Event, "ready")
	}
	if ready.PID != running.PID {
		t.Fatalf("fixture ready marker pid = %d, want the running instance pid %d", ready.PID, running.PID)
	}
	if !stoppingPathsEqual(ready.Executable, running.Executable) {
		t.Fatalf("fixture ready marker executable = %q, want the launched %q", ready.Executable, running.Executable)
	}
	env.markerPID = ready.PID
	return env
}

// startStop runs the write side in the background so the legs can read the
// handler while the stop is still in flight.
func (e *stoppingHandlerEnv) startStop(t *testing.T) {
	t.Helper()
	e.started = true
	ctx, cancel := context.WithTimeout(context.Background(), stoppingStopCallTimeout)
	go func() {
		defer close(e.stopDone)
		defer cancel()
		e.stopErr = e.svc.StopInstance(ctx, e.instance.ID)
	}()
}

// awaitSignalHeld waits for the fixture's real-signal marker. It never polls a
// product state: signal.json is written by the child only after it received a
// real OS stop signal, and the production stop path publishes `stopping`
// before it sends that signal. Once this returns, the transitional state must
// already be readable, so the routes are read exactly once afterwards.
func (e *stoppingHandlerEnv) awaitSignalHeld(t *testing.T) lifecycleMarker {
	t.Helper()
	deadline := time.Now().Add(stoppingSignalDeadline)
	for {
		select {
		case <-e.stopDone:
			t.Fatalf("the stop call returned before the fixture observed a real OS stop signal (GOOS=%s, stop error: %v): no graceful interval existed on this host, so the held-window claim cannot be made",
				runtime.GOOS, e.stopErr)
		default:
		}
		data, err := os.ReadFile(e.signalPath)
		if err == nil {
			var marker lifecycleMarker
			if jsonErr := json.Unmarshal(data, &marker); jsonErr != nil {
				t.Fatalf("parse %s: %v", e.signalPath, jsonErr)
			}
			if marker.Event != "signal" {
				t.Fatalf("%s event = %q, want %q", e.signalPath, marker.Event, "signal")
			}
			if marker.Signal == "" {
				t.Fatalf("%s carries no signal name: the fixture did not observe a real OS stop signal", e.signalPath)
			}
			if marker.PID != e.markerPID {
				t.Fatalf("%s pid = %d, want the launched pid %d", e.signalPath, marker.PID, e.markerPID)
			}
			if !stoppingPathsEqual(marker.Executable, e.exePath) {
				t.Fatalf("%s executable = %q, want the fake runtime %q", e.signalPath, marker.Executable, e.exePath)
			}
			t.Logf("the fixture observed the real OS stop signal %q and is holding pid %d", marker.Signal, marker.PID)
			return marker
		}
		// The fixture publishes markers by rename. On Windows a reader that races
		// the rename can observe a sharing violation instead of the new file, so a
		// transient open error is retried; a persistent one fails at the deadline.
		if time.Now().After(deadline) {
			t.Fatalf("the fixture wrote no readable %s within %s (last error: %v): the real OS stop signal never arrived (GOOS=%s)",
				e.signalPath, stoppingSignalDeadline, err, runtime.GOOS)
		}
		time.Sleep(stoppingMarkerPollInterval)
	}
}

// readRouteOnce issues exactly one HTTP request against a route and bounds the
// call. The request runs in its own goroutine so that a reader blocked behind
// the controller lock becomes a FAIL instead of hanging the whole test.
func (e *stoppingHandlerEnv) readRouteOnce(
	t *testing.T,
	layer string,
	phase string,
	read func() (*domain.LaunchInstance, error),
) *domain.LaunchInstance {
	t.Helper()
	type result struct {
		inst *domain.LaunchInstance
		err  error
	}
	results := make(chan result, 1)
	startedAt := time.Now()
	go func() {
		inst, err := read()
		results <- result{inst: inst, err: err}
	}()
	select {
	case r := <-results:
		elapsed := time.Since(startedAt)
		if r.err != nil {
			t.Fatalf("%s: %v", layer, r.err)
		}
		if elapsed > stoppingReadStallGuard {
			t.Fatalf("%s: the read took %s %s: a reader that still acquired the controller lock is the regression this bound exists for",
				layer, elapsed, phase)
		}
		t.Logf("%s: answered in %s %s", layer, elapsed, phase)
		return r.inst
	case <-time.After(stoppingReadStallGuard):
		t.Fatalf("%s: the read did not return within %s %s: a reader blocked behind the controller lock is the regression this bound exists for",
			layer, stoppingReadStallGuard, phase)
		return nil
	}
}

// assertStoppingView asserts the wire view is the launched generation in the
// transitional state.
func (e *stoppingHandlerEnv) assertStoppingView(t *testing.T, layer string, obs *domain.LaunchInstance) {
	t.Helper()
	if obs.ID != e.instance.ID {
		t.Fatalf("%s: id = %q, want %q", layer, string(obs.ID), string(e.instance.ID))
	}
	if obs.State != domain.InstanceStateStopping {
		t.Fatalf("%s: state = %q while the fixture held the stop window, want %q: the transitional state is not published before the OS signal",
			layer, obs.State, domain.InstanceStateStopping)
	}
	if !obs.IsInFlight() {
		t.Fatalf("%s: state %q is not in flight: the one-in-flight-per-model rule would be broken", layer, obs.State)
	}
	e.assertGenerationIdentity(t, layer, obs)
}

func (e *stoppingHandlerEnv) assertGenerationIdentity(t *testing.T, layer string, obs *domain.LaunchInstance) {
	t.Helper()
	want := e.instance
	if obs.PID != want.PID {
		t.Fatalf("%s: pid = %d during the stop, want the launched pid %d", layer, obs.PID, want.PID)
	}
	if !obs.StartedAt.Equal(want.StartedAt) {
		t.Fatalf("%s: started_at = %s during the stop, want the launched %s", layer, obs.StartedAt, want.StartedAt)
	}
	if obs.RuntimeID != want.RuntimeID {
		t.Fatalf("%s: runtime_id = %q during the stop, want %q", layer, obs.RuntimeID, want.RuntimeID)
	}
	if obs.Executable != want.Executable {
		t.Fatalf("%s: executable = %q during the stop, want %q", layer, obs.Executable, want.Executable)
	}
	if strings.Join(obs.Args, "\x00") != strings.Join(want.Args, "\x00") {
		t.Fatalf("%s: args = %q during the stop, want %q", layer, obs.Args, want.Args)
	}
}

// assertStopInProgress asserts from the test side that the stop call has not
// returned yet, so the observations above belong to a live stop.
func (e *stoppingHandlerEnv) assertStopInProgress(t *testing.T, layer string) {
	t.Helper()
	select {
	case <-e.stopDone:
		t.Fatalf("%s: the stop call already returned (error: %v) while the fixture release had not been written; a stop cannot complete while the child is held",
			layer, e.stopErr)
	default:
	}
}

// assertProcessLive proves the wire PID is alive and is still the launched
// process, using the same anchors as production verifyIdentity: executable path
// equality and, when the platform supplies it, the process start time.
func (e *stoppingHandlerEnv) assertProcessLive(t *testing.T, layer string, obs *domain.LaunchInstance) {
	t.Helper()
	prober := platform.NewRecoveryProber()
	alive, err := prober.IsProcessAlive(obs.PID)
	if err != nil {
		t.Fatalf("%s: liveness probe for pid %d: %v", layer, obs.PID, err)
	}
	if !alive {
		t.Fatalf("%s: the wire pid %d is not alive although the stop window was held; the process exited before the observation",
			layer, obs.PID)
	}
	identity, err := prober.GetProcessIdentity(obs.PID)
	if err != nil {
		t.Fatalf("%s: identity probe for pid %d: %v", layer, obs.PID, err)
	}
	if identity.ExecutablePath == "" || obs.Executable == "" || !stoppingPathsEqual(obs.Executable, identity.ExecutablePath) {
		t.Fatalf("%s: the live pid %d runs %q, want the launched %q: generation identity is not preserved",
			layer, obs.PID, identity.ExecutablePath, obs.Executable)
	}
	if identity.HasStartTime && !obs.StartedAt.IsZero() && !stoppingTimesApproximatelyEqual(obs.StartedAt, identity.StartTime) {
		t.Fatalf("%s: the live pid %d started at %s, want the launched %s: a recycled pid would not match this anchor",
			layer, obs.PID, identity.StartTime, obs.StartedAt)
	}
	t.Logf("%s: pid %d is alive and identity-confirmed (executable %q, start-time anchor available: %t)",
		layer, obs.PID, identity.ExecutablePath, identity.HasStartTime)
}

// assertReleaseAbsent asserts the test has not released the child yet, so the
// observations cannot have been made after the process was free to exit.
func (e *stoppingHandlerEnv) assertReleaseAbsent(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(e.releasePath); err == nil {
		t.Fatalf("%s already exists before the test released the fixture: the observed window was not the held one", e.releasePath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", e.releasePath, err)
	}
}

// release writes the fixture's release marker. O_EXCL makes the write proof
// that this test created it.
func (e *stoppingHandlerEnv) release(t *testing.T) {
	t.Helper()
	if err := writeReleaseMarker(e.releasePath, "test"); err != nil {
		t.Fatalf("write %s: %v", e.releasePath, err)
	}
	e.released = true
	t.Logf("released the held fixture through %s", e.releasePath)
}

// awaitReleased waits for the fixture to confirm it observed the release. The
// child writes released.json before it exits, so a stop that returns first
// proves the release never reached the child.
func (e *stoppingHandlerEnv) awaitReleased(t *testing.T) lifecycleMarker {
	t.Helper()
	deadline := time.Now().Add(stoppingReleaseDeadline)
	for {
		data, err := os.ReadFile(e.releasedPath)
		if err == nil {
			var marker lifecycleMarker
			if jsonErr := json.Unmarshal(data, &marker); jsonErr != nil {
				t.Fatalf("parse %s: %v", e.releasedPath, jsonErr)
			}
			if marker.Event != "released" {
				t.Fatalf("%s event = %q, want %q", e.releasedPath, marker.Event, "released")
			}
			if marker.PID != e.markerPID {
				t.Fatalf("%s pid = %d, want the launched pid %d", e.releasedPath, marker.PID, e.markerPID)
			}
			return marker
		}
		// Retried like signal.json: the fixture publishes released.json by rename,
		// so a Windows reader can race the rename with a sharing violation.
		select {
		case <-e.stopDone:
			t.Fatalf("the stop returned (error: %v) before the fixture wrote %s: the release did not reach the held child", e.stopErr, e.releasedPath)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fixture wrote no readable %s within %s after the release (last error: %v)",
				e.releasedPath, stoppingReleaseDeadline, err)
		}
		time.Sleep(stoppingMarkerPollInterval)
	}
}

// awaitStopReturn requires the stop call to return nil. The release is what
// lets the child exit, so a non-nil error is a stop-path failure, not a host
// property.
func (e *stoppingHandlerEnv) awaitStopReturn(t *testing.T) {
	t.Helper()
	select {
	case <-e.stopDone:
	case <-time.After(stoppingStopCallTimeout):
		t.Fatalf("the stop call did not return within %s after the fixture release", stoppingStopCallTimeout)
	}
	if e.stopErr != nil {
		t.Fatalf("Stop returned %v after a released, cleanly exiting fixture; want nil", e.stopErr)
	}
}

// assertTerminal asserts the terminal view is already published when Stop
// returns: stopCore waits for the controller's run.done, which wait() closes
// only after it published the terminal state. One read is therefore sufficient
// and a non-terminal answer is a real failure, not a scheduling delay.
func (e *stoppingHandlerEnv) assertTerminal(t *testing.T, layer string, obs *domain.LaunchInstance) {
	t.Helper()
	if !obs.IsTerminal() {
		t.Fatalf("%s: state = %q after Stop returned, want a terminal state", layer, obs.State)
	}
	if obs.State != domain.InstanceStateExited {
		t.Fatalf("%s: terminal state = %q, want %q", layer, obs.State, domain.InstanceStateExited)
	}
	if obs.ExitClass != domain.InstanceExitSignaled {
		t.Fatalf("%s: exit_class = %q, want %q for an intentional stop", layer, obs.ExitClass, domain.InstanceExitSignaled)
	}
	if obs.ExitCode == nil {
		t.Fatalf("%s: exit_code is absent, want 0", layer)
	}
	if *obs.ExitCode != 0 {
		t.Fatalf("%s: exit_code = %d, want 0: the fixture exits cleanly after a release", layer, *obs.ExitCode)
	}
	if obs.PID != e.markerPID {
		t.Fatalf("%s: terminal pid = %d, want the launched pid %d", layer, obs.PID, e.markerPID)
	}
	if obs.IsInFlight() {
		t.Fatalf("%s: terminal state %q still reports in flight", layer, obs.State)
	}
}

// assertNoLeaks asserts this leg left neither a running managed process nor a
// live controller behind. The managed process's termination is already recorded
// by Stop (terminal state, exit code, nil error); this is the OS post-condition
// on top of it. On Windows a terminated PID can stay resolvable for a short
// kernel-teardown window after the exit is confirmed, so the bound below
// converts a process that never goes away into a FAIL. It is not an observation
// and never widens one. A recycled PID is not our process: it is recognized by
// a different executable path and treated as gone.
func (e *stoppingHandlerEnv) assertNoLeaks(t *testing.T) {
	t.Helper()
	select {
	case <-e.stopDone:
	default:
		t.Fatalf("the stop goroutine is still running after Stop returned")
	}
	active, err := e.sup.ListActive()
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("the supervisor still reports %d active instance(s) after the stop: %v", len(active), active[0].ID)
	}

	prober := platform.NewRecoveryProber()
	deadline := time.Now().Add(stoppingProcessGoneDeadline)
	for {
		alive, err := prober.IsProcessAlive(e.markerPID)
		if err != nil {
			t.Fatalf("liveness probe for the stopped pid %d: %v", e.markerPID, err)
		}
		if alive {
			if identity, idErr := prober.GetProcessIdentity(e.markerPID); idErr == nil &&
				!stoppingPathsEqual(identity.ExecutablePath, e.exePath) {
				t.Logf("pid %d now belongs to %q: the launched process is gone and the pid was recycled",
					e.markerPID, identity.ExecutablePath)
				alive = false
			}
		}
		if !alive {
			t.Logf("pid %d is gone and the supervisor reports no active instance", e.markerPID)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is still alive %s after the stop returned; the managed process leaked",
				e.markerPID, stoppingProcessGoneDeadline)
		}
		time.Sleep(stoppingMarkerPollInterval)
	}
}

// getInstanceState calls GET /api/v1/instances/{id}.
func (e *stoppingHandlerEnv) getInstanceState() (*domain.LaunchInstance, error) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+string(e.instance.ID), nil)
	w := httptest.NewRecorder()
	e.h.Get(w, req)
	if w.Code != http.StatusOK {
		return nil, fmt.Errorf("GET /api/v1/instances/%s = %d, want 200: %s", string(e.instance.ID), w.Code, w.Body.String())
	}
	var out domain.LaunchInstance
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("GET /api/v1/instances/%s body is not a LaunchInstance: %w (%s)", string(e.instance.ID), err, w.Body.String())
	}
	return &out, nil
}

// getInstanceStateFromCollection calls GET /api/v1/instances and returns the
// entry for the instance under stop. The status code and the decoder are part
// of the claim: this is the route the browser reads, so a 500 or an
// unparseable body is a failure.
func (e *stoppingHandlerEnv) getInstanceStateFromCollection() (*domain.LaunchInstance, error) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	e.h.List(w, req)
	if w.Code != http.StatusOK {
		return nil, fmt.Errorf("GET /api/v1/instances = %d, want 200: %s", w.Code, w.Body.String())
	}
	var out []domain.LaunchInstance
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("GET /api/v1/instances body is not a LaunchInstance array: %w (%s)", err, w.Body.String())
	}
	for i := range out {
		if out[i].ID == e.instance.ID {
			return &out[i], nil
		}
	}
	return nil, fmt.Errorf("GET /api/v1/instances returned %d entries and none of them is the instance under stop %s",
		len(out), string(e.instance.ID))
}

// writeReleaseMarker creates the release file exclusively, so an existing file
// can never be mistaken for this test's release.
func writeReleaseMarker(path, origin string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]string{"event": "release", "origin": origin})
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readLifecycleMarker(t *testing.T, path string) lifecycleMarker {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var marker lifecycleMarker
			if jsonErr := json.Unmarshal(data, &marker); jsonErr != nil {
				t.Fatalf("parse %s: %v", path, jsonErr)
			}
			return marker
		}
		if time.Now().After(deadline) {
			t.Fatalf("the signal-hold fixture never wrote %s: %v", path, err)
		}
		time.Sleep(stoppingMarkerPollInterval)
	}
}

// stoppingPathsEqual mirrors the unexported production helper used by
// verifyIdentity. It is duplicated here because the comparison has to be made
// from the reader's package; the shape is kept identical on purpose.
func stoppingPathsEqual(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" {
		sa, sb := strings.ToLower(a), strings.ToLower(b)
		if sa == sb {
			return true
		}
		const exe = ".exe"
		if strings.HasSuffix(sb, exe) && sa == sb[:len(sb)-len(exe)] {
			return true
		}
		if strings.HasSuffix(sa, exe) && sb == sa[:len(sa)-len(exe)] {
			return true
		}
	}
	return false
}

// stoppingTimesApproximatelyEqual mirrors production timesApproximatelyEqual.
func stoppingTimesApproximatelyEqual(a, b time.Time) bool {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 5*time.Second
}

// TestT2CollectionRouteReportsStoppingWhileProcessIsAlive is the
// GET /api/v1/instances leg of T-2 — the route the browser's model-row and
// pipeline-chip badges are derived from (§D10). It also reads the item route,
// so both admissible observation surfaces are proven inside ONE held stop
// window rather than in two separately-timed runs.
func TestT2CollectionRouteReportsStoppingWhileProcessIsAlive(t *testing.T) {
	env := newStoppingHandlerEnv(t)
	env.startStop(t)
	env.awaitSignalHeld(t)

	item := env.readRouteOnce(t, "GET /api/v1/instances/{id}", "while the stop was held", env.getInstanceState)
	env.assertStoppingView(t, "GET /api/v1/instances/{id}", item)

	collection := env.readRouteOnce(t, "GET /api/v1/instances", "while the stop was held", env.getInstanceStateFromCollection)
	env.assertStoppingView(t, "GET /api/v1/instances", collection)

	// Both reads are above; everything below proves they were inside one held
	// window, then releases the child.
	env.assertStopInProgress(t, "after both route reads")
	env.assertProcessLive(t, "after both route reads", collection)
	env.assertReleaseAbsent(t)

	env.release(t)
	env.awaitReleased(t)
	env.awaitStopReturn(t)

	after := env.readRouteOnce(t, "GET /api/v1/instances", "after the stop returned", env.getInstanceStateFromCollection)
	env.assertTerminal(t, "GET /api/v1/instances", after)
	env.assertNoLeaks(t)
}

// TestT2ItemRouteReportsStoppingWhileProcessIsAlive is the
// GET /api/v1/instances/{id} leg of T-2, verified on its own so a collection
// regression cannot hide an item-route regression (and vice versa).
func TestT2ItemRouteReportsStoppingWhileProcessIsAlive(t *testing.T) {
	env := newStoppingHandlerEnv(t)
	env.startStop(t)
	env.awaitSignalHeld(t)

	item := env.readRouteOnce(t, "GET /api/v1/instances/{id}", "while the stop was held", env.getInstanceState)
	env.assertStoppingView(t, "GET /api/v1/instances/{id}", item)

	env.assertStopInProgress(t, "after the item-route read")
	env.assertProcessLive(t, "after the item-route read", item)
	env.assertReleaseAbsent(t)

	env.release(t)
	env.awaitReleased(t)
	env.awaitStopReturn(t)

	after := env.readRouteOnce(t, "GET /api/v1/instances/{id}", "after the stop returned", env.getInstanceState)
	env.assertTerminal(t, "GET /api/v1/instances/{id}", after)
	env.assertNoLeaks(t)
}
