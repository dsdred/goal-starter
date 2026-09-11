package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newResolveFixture(t *testing.T) (*LaunchResolver, *Model, *Runtime) {
	t.Helper()
	dir := t.TempDir()
	exePath := filepath.Join(dir, "server")
	if err := os.WriteFile(exePath, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	r := NewLaunchResolver()
	model := &Model{ID: "m1", Name: "m", RuntimeID: "r1"}
	runtime := &Runtime{ID: "r1", Name: "rt", Executable: "server", WorkingDirectory: dir}
	return r, model, runtime
}

// Same-tick worst case: the clock is frozen, so every resolution observes the
// identical UnixNano. The atomic sequence must carry uniqueness alone.
func TestInstanceID_Uniqueness_SameTick(t *testing.T) {
	r, model, runtime := newResolveFixture(t)

	fixed := time.Unix(1788684263, 600000000)
	orig := timeNow
	timeNow = func() time.Time { return fixed }
	t.Cleanup(func() { timeNow = orig })

	const n = 1000
	seen := make(map[InstanceID]int, n)
	for i := 0; i < n; i++ {
		inst, err := r.ResolveToInstance(model, runtime, nil, nil)
		if err != nil {
			t.Fatalf("ResolveToInstance %d: %v", i, err)
		}
		seen[inst.ID]++
	}
	if len(seen) != n {
		t.Fatalf("same-tick: %d resolutions produced only %d unique instance ids", n, len(seen))
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("instance id %s minted %d times", id, c)
		}
	}
}

// Concurrent resolutions under the real clock: every worker resolves the same
// model; all mints must stay unique. Safe under -race.
func TestInstanceID_Uniqueness_Concurrent(t *testing.T) {
	r, model, runtime := newResolveFixture(t)

	const workers = 16
	const perWorker = 64 // 1024 resolutions total

	var mu sync.Mutex
	seen := make(map[InstanceID]int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				inst, err := r.ResolveToInstance(model, runtime, nil, nil)
				if err != nil {
					t.Errorf("ResolveToInstance: %v", err)
					return
				}
				mu.Lock()
				seen[inst.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	total := workers * perWorker
	if len(seen) != total {
		t.Fatalf("concurrent: %d resolutions produced only %d unique instance ids", total, len(seen))
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("instance id %s minted %d times", id, c)
		}
	}
}

// Opaque-string contract: the id starts with the model id and appends a
// deterministic suffix; nothing downstream may parse beyond that prefix.
func TestInstanceID_OpaqueFormat(t *testing.T) {
	r, model, runtime := newResolveFixture(t)
	fixed := time.Unix(1788684263, 600000000)
	orig := timeNow
	timeNow = func() time.Time { return fixed }
	t.Cleanup(func() { timeNow = orig })
	inst, err := r.ResolveToInstance(model, runtime, nil, nil)
	if err != nil {
		t.Fatalf("ResolveToInstance: %v", err)
	}
	want := fmt.Sprintf("%s-%d-", model.ID, fixed.UnixNano())
	if len(inst.ID) < len(want) || string(inst.ID)[:len(want)] != want {
		t.Fatalf("instance id %q must be %q... (opaque modelID-nano-seq)", inst.ID, want)
	}
}
