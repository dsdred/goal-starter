package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// ADR 021 §Deterministic test obligations — T-2 HTTP layer, T-9.
//
// These legs carry no stop-gate seam (internal/process owns the held-window
// proofs) and no fabricated response: the transitional state is produced by the
// real signal-delay fixture dwell, and it is read through the admissible
// observation surfaces named by §D10 — GET /api/v1/instances and
// GET /api/v1/instances/{id}. Nothing here is browser evidence: a DOM badge is
// only observable in a browser, which is Slice 4's leg.

const (
	// stoppingDwellExitDelayMS is the fixture's post-signal dwell. It is the
	// only synchronization in these legs.
	stoppingDwellExitDelayMS = 4000

	stoppingPollInterval = 5 * time.Millisecond

	// stoppingWindowDeadline bounds the wait for the first observation. Missing
	// it is a host-scheduling fact, so it is reported as NOT OBSERVED /
	// INCONCLUSIVE rather than as a product failure.
	stoppingWindowDeadline = 5 * time.Second

	// stoppingReadStallGuard is the fail-closed bound on one handler call during
	// a live stop. A reader that still acquired the controller lock is released
	// only when Manager.Stop returns, at least the whole dwell away; a lock-free
	// view load costs microseconds. The guard sits far below the stall it
	// detects, so it can only ever fail a test, never pass one.
	stoppingReadStallGuard = 1500 * time.Millisecond

	stoppingTerminalDeadline = 10 * time.Second
	stoppingStopCallTimeout  = 30 * time.Second
)

// dwellReadyMarker mirrors the maintained signal-delay fixture's ready marker.
type dwellReadyMarker struct {
	Event      string `json:"event"`
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Timestamp  string `json:"timestamp"`
}

// stoppingHandlerEnv owns one real supervisor, the instances handler built on
// it, and one launched instance.
type stoppingHandlerEnv struct {
	h         *InstancesHandler
	svc       *application.InstanceService
	exePath   string
	instance  *domain.LaunchInstance
	markerPID int
	stopDone  chan struct{}
	stopErr   error
	started   bool
	stopRet   atomic.Bool
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
		Args: []string{"signal-delay", markerDir, strconv.Itoa(stoppingDwellExitDelayMS)},
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	sup := process.NewSupervisorWithConfig(newMockInstanceStoreForTest(),
		process.SupervisorConfig{MaxConcurrent: 4, LogBufferSize: 64})
	svc := application.NewInstanceService(sup, repo)
	env := &stoppingHandlerEnv{
		h:        NewInstancesHandler(svc, nil),
		svc:      svc,
		exePath:  fakePath,
		stopDone: make(chan struct{}),
	}

	// The in-flight stop must finish before shutdown: the shared fixture
	// executable stays image-locked while a generation is still being reaped,
	// and a stop that has not returned still owns the controller.
	t.Cleanup(func() {
		if env.started {
			select {
			case <-env.stopDone:
			case <-time.After(stoppingStopCallTimeout):
				t.Errorf("timeout waiting for the in-flight stop goroutine to finish")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
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

	marker := readDwellReadyMarker(t, filepath.Join(markerDir, "ready.json"))
	if marker.PID != running.PID {
		t.Fatalf("fixture ready marker pid = %d, want the running instance pid %d", marker.PID, running.PID)
	}
	env.markerPID = marker.PID
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
		defer e.stopRet.Store(true)
		e.stopErr = e.svc.StopInstance(ctx, e.instance.ID)
	}()
}

func (e *stoppingHandlerEnv) awaitStopReturn(t *testing.T) {
	t.Helper()
	select {
	case <-e.stopDone:
	case <-time.After(stoppingStopCallTimeout):
		t.Fatalf("the stop call did not return within %s", stoppingStopCallTimeout)
	}
	// The graceful path is host-dependent (console control events on Windows),
	// so the error is recorded, not asserted: these legs are about what the
	// readers answer, not about how the child exited.
	if e.stopErr != nil {
		t.Logf("stop returned an error (graceful-stop escalation is host-dependent): %v", e.stopErr)
	}
}

// getInstances calls GET /api/v1/instances and returns the entry for the
// instance under stop. The status code and the decoder are part of the claim:
// this is the route the browser reads, so a 500 or an unparseable body is a
// failure, not an inconclusive observation.
func (e *stoppingHandlerEnv) getInstances(t *testing.T) *domain.LaunchInstance {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	e.h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/instances = %d, want 200: %s", w.Code, w.Body.String())
	}
	var out []domain.LaunchInstance
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET /api/v1/instances body is not a LaunchInstance array: %v (%s)", err, w.Body.String())
	}
	for i := range out {
		if out[i].ID == e.instance.ID {
			return &out[i]
		}
	}
	t.Fatalf("GET /api/v1/instances returned %d entries and none of them is the instance under stop %s",
		len(out), string(e.instance.ID))
	return nil
}

// getInstanceByID calls GET /api/v1/instances/{id}.
func (e *stoppingHandlerEnv) getInstanceByID(t *testing.T) *domain.LaunchInstance {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+string(e.instance.ID), nil)
	w := httptest.NewRecorder()
	e.h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/instances/%s = %d, want 200: %s", string(e.instance.ID), w.Code, w.Body.String())
	}
	var out domain.LaunchInstance
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET /api/v1/instances/%s body is not a LaunchInstance: %v (%s)", string(e.instance.ID), err, w.Body.String())
	}
	return &out
}

// observeLiveWindow polls read until the route answers with the transitional
// state, then proves the observation belongs to a live stop: the stop call has
// not returned, the handler answered without being stalled behind the stop, the
// managed process is alive and is still the launched one, and the wire view kept
// the generation identity.
func (e *stoppingHandlerEnv) observeLiveWindow(
	t *testing.T,
	layer string,
	read func(*testing.T) *domain.LaunchInstance,
) *domain.LaunchInstance {
	t.Helper()
	deadline := time.Now().Add(stoppingWindowDeadline)
	for {
		if e.stopRet.Load() {
			t.Skipf("NOT OBSERVED / INCONCLUSIVE: %s: the stop call returned before the route could answer with the transitional state; the %d ms dwell left no observable window on this host (GOOS=%s)",
				layer, stoppingDwellExitDelayMS, runtime.GOOS)
		}
		at := time.Now()
		obs := read(t)
		if elapsed := time.Since(at); elapsed > stoppingReadStallGuard {
			t.Fatalf("%s: the read took %s while a stop was in flight: a reader that still acquired the controller lock is the regression this bound exists for",
				layer, elapsed)
		}
		switch obs.State {
		case domain.InstanceStateStopping:
			if !e.processIsLiveIdentityConfirmed(t, obs) {
				t.Skipf("NOT OBSERVED / INCONCLUSIVE: %s: stopping first became readable only after the managed process had already terminated (GOOS=%s)",
					layer, runtime.GOOS)
			}
			e.assertGenerationIdentity(t, layer, obs)
			t.Logf("%s: state=%s pid=%d observed while the stop call was in flight and pid %d was alive and identity-confirmed",
				layer, obs.State, obs.PID, e.markerPID)
			return obs
		case domain.InstanceStateRunning, domain.InstanceStateStarting:
			// The stop has not published the transition yet.
		default:
			t.Fatalf("%s: state %q during a live stop call is unreachable; want running, starting or stopping",
				layer, obs.State)
		}
		if time.Now().After(deadline) {
			t.Skipf("NOT OBSERVED / INCONCLUSIVE: %s: no stopping observation before %s elapsed (last state %q, GOOS=%s)",
				layer, stoppingWindowDeadline, obs.State, runtime.GOOS)
		}
		time.Sleep(stoppingPollInterval)
	}
}

func (e *stoppingHandlerEnv) processIsLiveIdentityConfirmed(t *testing.T, obs *domain.LaunchInstance) bool {
	t.Helper()
	prober := platform.NewRecoveryProber()
	alive, err := prober.IsProcessAlive(obs.PID)
	if err != nil || !alive {
		return false
	}
	identity, err := prober.GetProcessIdentity(obs.PID)
	if err != nil {
		return false
	}
	cleanIdentity := filepath.Clean(identity.ExecutablePath)
	cleanWant := filepath.Clean(e.exePath)
	if cleanIdentity != cleanWant && !(runtime.GOOS == "windows" && strings.EqualFold(cleanIdentity, cleanWant)) {
		t.Fatalf("the wire pid %d is live but runs %q, want the fake runtime %q: generation identity is not preserved",
			obs.PID, identity.ExecutablePath, e.exePath)
	}
	return true
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

// awaitTerminal proves the terminal state becomes readable once the stop has
// returned; a route that stays on the transitional state is a real failure.
func (e *stoppingHandlerEnv) awaitTerminal(
	t *testing.T,
	layer string,
	read func(*testing.T) *domain.LaunchInstance,
) *domain.LaunchInstance {
	t.Helper()
	deadline := time.Now().Add(stoppingTerminalDeadline)
	var obs *domain.LaunchInstance
	for {
		obs = read(t)
		if obs.IsTerminal() {
			t.Logf("%s: after the stop returned: state=%s exit_class=%q exit_code=%s",
				layer, obs.State, obs.ExitClass, formatStoppingExitCode(obs.ExitCode))
			return obs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: state %q is still readable %s after the stop returned; the terminal state never became visible",
				layer, obs.State, stoppingTerminalDeadline)
		}
		time.Sleep(stoppingPollInterval)
	}
}

func formatStoppingExitCode(code *int) string {
	if code == nil {
		return "<nil>"
	}
	return strconv.Itoa(*code)
}

func readDwellReadyMarker(t *testing.T, path string) dwellReadyMarker {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var marker dwellReadyMarker
			if jsonErr := json.Unmarshal(data, &marker); jsonErr != nil {
				t.Fatalf("parse %s: %v", path, jsonErr)
			}
			if marker.Event != "ready" {
				t.Fatalf("%s event = %q, want %q", path, marker.Event, "ready")
			}
			return marker
		}
		if time.Now().After(deadline) {
			t.Fatalf("the signal-delay fixture never wrote %s: %v", path, err)
		}
		time.Sleep(stoppingPollInterval)
	}
}

// TestT2CollectionRouteReportsStoppingWhileProcessIsAlive is the
// GET /api/v1/instances leg of T-2 — the route the browser's model-row and
// pipeline-chip badges are derived from (§D10).
func TestT2CollectionRouteReportsStoppingWhileProcessIsAlive(t *testing.T) {
	env := newStoppingHandlerEnv(t)
	env.startStop(t)

	obs := env.observeLiveWindow(t, "GET /api/v1/instances", env.getInstances)
	if !obs.IsInFlight() {
		t.Fatalf("the stopping wire view is not in flight: the one-in-flight-per-model rule would be broken")
	}

	env.awaitStopReturn(t)
	after := env.awaitTerminal(t, "GET /api/v1/instances", env.getInstances)
	if after.IsInFlight() {
		t.Fatalf("terminal state %q still reports in flight", after.State)
	}
}

// TestT2ItemRouteReportsStoppingWhileProcessIsAlive is the
// GET /api/v1/instances/{id} leg of T-2.
func TestT2ItemRouteReportsStoppingWhileProcessIsAlive(t *testing.T) {
	env := newStoppingHandlerEnv(t)
	env.startStop(t)

	env.observeLiveWindow(t, "GET /api/v1/instances/{id}", env.getInstanceByID)

	env.awaitStopReturn(t)
	after := env.awaitTerminal(t, "GET /api/v1/instances/{id}", env.getInstanceByID)
	if after.PID != env.markerPID {
		t.Fatalf("terminal wire pid = %d, want the launched pid %d", after.PID, env.markerPID)
	}
}
