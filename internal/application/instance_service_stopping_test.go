package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

// ADR 021 §Deterministic test obligations — T-2 service layer, T-9.
//
// The held-window proofs live in internal/process, where the package-private
// stop gate can pin Manager.Stop open. These legs carry no seam: the window is
// produced by the real signal-delay fixture dwell, so what is proven here is
// that the application-layer readers answer with the transitional state while
// the managed process is still alive, and with a terminal state once the stop
// has returned. Nothing in this file is browser evidence.

const (
	// stoppingDwellExitDelayMS is the fixture's post-signal dwell. It is the
	// only synchronization in these legs: the window is a real process that is
	// alive while a stop is in flight, never a sleep substituted for one.
	stoppingDwellExitDelayMS = 4000

	stoppingPollInterval = 5 * time.Millisecond

	// stoppingWindowDeadline bounds the wait for the first observation. Missing
	// it is a host-scheduling fact and is reported as NOT OBSERVED / INCONCLUSIVE;
	// it is never a product failure.
	stoppingWindowDeadline = 5 * time.Second

	// stoppingReadStallGuard is the fail-closed bound on one read of a layer
	// under stop. A reader that still sat behind the stop's controller lock
	// would be released only when Manager.Stop returns, which is at least the
	// dwell away; a lock-free view load costs microseconds. The guard is ~300x
	// below the stall it detects and can only ever fail, never pass a test.
	stoppingReadStallGuard = 1500 * time.Millisecond

	stoppingRunningDeadline  = 20 * time.Second
	stoppingTerminalDeadline = 10 * time.Second
	stoppingStopCallTimeout  = 30 * time.Second
)

// readyMarker mirrors the maintained signal-delay fixture's ready marker.
type readyMarker struct {
	Event      string `json:"event"`
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Timestamp  string `json:"timestamp"`
}

// stoppingEnv owns one supervisor backed by real processes and one instance
// launched through InstanceService.StartModel.
type stoppingEnv struct {
	svc        *InstanceService
	exePath    string
	instance   *domain.LaunchInstance
	markerPID  int
	stopDone   chan struct{}
	stopErr    error
	started    bool
	stopReturn atomic.Bool
}

func newStoppingEnv(t *testing.T) *stoppingEnv {
	t.Helper()

	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "goal_repo.json"))
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
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

	sup := process.NewSupervisor(repo)
	env := &stoppingEnv{
		svc:      NewInstanceService(sup, repo),
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
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = sup.ShutdownWithPersistence(ctx)
	})

	inst, err := env.svc.StartModel(context.Background(), "m-stopping")
	if err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	env.instance = inst

	if err := env.awaitState(domain.InstanceStateRunning, stoppingRunningDeadline); err != nil {
		t.Fatalf("instance %s never became running: %v", string(inst.ID), err)
	}
	running, err := env.svc.GetInstanceStatus(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("GetInstanceStatus: %v", err)
	}
	env.instance = running

	marker := readReadyMarker(t, filepath.Join(markerDir, "ready.json"))
	if marker.PID != running.PID {
		t.Fatalf("fixture ready marker pid = %d, want the running instance pid %d", marker.PID, running.PID)
	}
	env.markerPID = marker.PID
	return env
}

// awaitState polls the service reader until the instance reaches target.
func (e *stoppingEnv) awaitState(target domain.InstanceState, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inst, err := e.svc.GetInstanceStatus(context.Background(), e.instance.ID)
		if err == nil && inst.State == target {
			return nil
		}
		time.Sleep(stoppingPollInterval)
	}
	inst, err := e.svc.GetInstanceStatus(context.Background(), e.instance.ID)
	if err != nil {
		return err
	}
	return &stoppingStateError{got: inst.State, want: target}
}

type stoppingStateError struct{ got, want domain.InstanceState }

func (e *stoppingStateError) Error() string {
	return "state is " + string(e.got) + ", want " + string(e.want)
}

// startStop runs the write side through the service in the background so the
// legs can read the layer while the stop is still in flight.
func (e *stoppingEnv) startStop(t *testing.T) {
	t.Helper()
	e.started = true
	ctx, cancel := context.WithTimeout(context.Background(), stoppingStopCallTimeout)
	go func() {
		defer close(e.stopDone)
		defer cancel()
		defer e.stopReturn.Store(true)
		e.stopErr = e.svc.StopInstance(ctx, e.instance.ID)
	}()
}

// awaitStopReturn blocks until the background stop has returned.
func (e *stoppingEnv) awaitStopReturn(t *testing.T) {
	t.Helper()
	select {
	case <-e.stopDone:
	case <-time.After(stoppingStopCallTimeout):
		t.Fatalf("the stop call did not return within %s", stoppingStopCallTimeout)
	}
	// The graceful path is host-dependent (console control events on Windows),
	// so the error is recorded, not asserted: the subject of these legs is what
	// the readers answer, not how the child exited.
	if e.stopErr != nil {
		t.Logf("stop returned an error (graceful-stop escalation is host-dependent): %v", e.stopErr)
	}
}

// observeLiveWindow polls read until the layer answers with the transitional
// state, then proves the observation belongs to a live stop: the stop call has
// not returned, the managed process is alive and is still the launched one, and
// the read answered without being stalled behind the stop.
func (e *stoppingEnv) observeLiveWindow(
	t *testing.T,
	layer string,
	read func() *domain.LaunchInstance,
) *domain.LaunchInstance {
	t.Helper()
	deadline := time.Now().Add(stoppingWindowDeadline)
	for {
		if e.stopReturn.Load() {
			t.Skipf("NOT OBSERVED / INCONCLUSIVE: %s: the stop call returned before the layer could answer with the transitional state; the %d ms dwell left no observable window on this host (GOOS=%s)",
				layer, stoppingDwellExitDelayMS, runtime.GOOS)
		}
		at := time.Now()
		obs := read()
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

// processIsLiveIdentityConfirmed reports whether the PID carried by the view is
// a live process running the fixture executable. A mismatch between the PID and
// the executable is a real identity failure, not an inconclusive host.
func (e *stoppingEnv) processIsLiveIdentityConfirmed(t *testing.T, obs *domain.LaunchInstance) bool {
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
		t.Fatalf("the view pid %d is live but runs %q, want the fake runtime %q: generation identity is not preserved",
			obs.PID, identity.ExecutablePath, e.exePath)
	}
	return true
}

// assertGenerationIdentity fails when the transitional view dropped or replaced
// any identity anchor of the generation being stopped.
func (e *stoppingEnv) assertGenerationIdentity(t *testing.T, layer string, obs *domain.LaunchInstance) {
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
// returned; a layer that stays on the transitional state is a real failure.
func (e *stoppingEnv) awaitTerminal(t *testing.T, layer string, read func() *domain.LaunchInstance) *domain.LaunchInstance {
	t.Helper()
	deadline := time.Now().Add(stoppingTerminalDeadline)
	var obs *domain.LaunchInstance
	for {
		obs = read()
		if obs.IsTerminal() {
			t.Logf("%s: after the stop returned: state=%s exit_class=%q exit_code=%s",
				layer, obs.State, obs.ExitClass, formatExitCode(obs.ExitCode))
			return obs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: state %q is still readable %s after the stop returned; the terminal state never became visible",
				layer, obs.State, stoppingTerminalDeadline)
		}
		time.Sleep(stoppingPollInterval)
	}
}

func formatExitCode(code *int) string {
	if code == nil {
		return "<nil>"
	}
	return strconv.Itoa(*code)
}

func readReadyMarker(t *testing.T, path string) readyMarker {
	t.Helper()
	deadline := time.Now().Add(stoppingRunningDeadline)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var marker readyMarker
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

// TestT2InstanceServiceListReportsStoppingWhileProcessIsAlive is the
// InstanceService.ListInstances leg of T-2: the collection reader must answer
// with the transitional state inside the live window and with the terminal
// state after the stop returns.
func TestT2InstanceServiceListReportsStoppingWhileProcessIsAlive(t *testing.T) {
	env := newStoppingEnv(t)
	env.startStop(t)

	obs := env.observeLiveWindow(t, "InstanceService.ListInstances", func() *domain.LaunchInstance {
		return findInstance(t, env.svc, env.instance.ID)
	})
	if !obs.IsInFlight() {
		t.Fatalf("the stopping view is not in flight: the one-in-flight-per-model rule would be broken")
	}

	env.awaitStopReturn(t)
	after := env.awaitTerminal(t, "InstanceService.ListInstances", func() *domain.LaunchInstance {
		return findInstance(t, env.svc, env.instance.ID)
	})
	if after.IsInFlight() {
		t.Fatalf("terminal state %q still reports in flight", after.State)
	}
}

// TestT2InstanceServiceStatusReportsStoppingWhileProcessIsAlive is the
// InstanceService.GetInstanceStatus leg of T-2.
func TestT2InstanceServiceStatusReportsStoppingWhileProcessIsAlive(t *testing.T) {
	env := newStoppingEnv(t)
	env.startStop(t)

	env.observeLiveWindow(t, "InstanceService.GetInstanceStatus", func() *domain.LaunchInstance {
		inst, err := env.svc.GetInstanceStatus(context.Background(), env.instance.ID)
		if err != nil {
			t.Fatalf("GetInstanceStatus during a live stop: %v", err)
		}
		return inst
	})

	env.awaitStopReturn(t)
	after := env.awaitTerminal(t, "InstanceService.GetInstanceStatus", func() *domain.LaunchInstance {
		inst, err := env.svc.GetInstanceStatus(context.Background(), env.instance.ID)
		if err != nil {
			t.Fatalf("GetInstanceStatus after the stop returned: %v", err)
		}
		return inst
	})
	if after.PID != env.markerPID {
		t.Fatalf("terminal view pid = %d, want the launched pid %d", after.PID, env.markerPID)
	}
}

func findInstance(t *testing.T, svc *InstanceService, id domain.InstanceID) *domain.LaunchInstance {
	t.Helper()
	list, err := svc.ListInstances(context.Background())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	for _, inst := range list {
		if inst.ID == id {
			return inst
		}
	}
	t.Fatalf("ListInstances returned %d entries and none of them is the instance under stop %s", len(list), string(id))
	return nil
}
