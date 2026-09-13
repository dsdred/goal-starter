package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dsdred/goal/internal/fsutil"
)

func TestImportGraph_SamePathPersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	// Step 1-2: persist known state A at path P.
	if err := r.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "Original", Executable: "/bin/orig"}); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateModel(&ModelEntry{ID: "m1", Name: "Model", RuntimeID: "rt1"}); err != nil {
		t.Fatal(err)
	}

	// Step 3: capture exact bytes A.
	bytesA, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	origRuntimes, _ := r.ListRuntimes()
	origModels, _ := r.ListModels()

	// Step 4: configure write seam to fail.
	r.writeFunc = func(p string, data []byte, perm os.FileMode) error {
		if p != path {
			t.Fatalf("write target changed: got %q want %q", p, path)
		}
		return errors.New("injected write failure")
	}

	// Step 5-6: import valid non-conflicting graph.
	// The graph will be staged (appended to in-memory slices) before saveLocked.
	err = r.ImportGraph(
		[]*RuntimeEntry{{ID: "rt-new", Name: "NewRT", Executable: "/bin/new"}},
		[]*ModelEntry{{ID: "m-new", Name: "NewModel", RuntimeID: "rt-new"}},
		nil,
	)

	// Step 7: ImportGraph returns persistence error.
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if !errors.Is(err, errors.New("injected write failure")) && !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step 8: in-memory repository == state A.
	runtimes, _ := r.ListRuntimes()
	if len(runtimes) != len(origRuntimes) {
		t.Fatalf("runtimes after rollback = %d, want %d", len(runtimes), len(origRuntimes))
	}
	if runtimes[0].ID != "rt1" || runtimes[0].Name != "Original" {
		t.Fatalf("original runtime modified: %+v", runtimes[0])
	}
	models, _ := r.ListModels()
	if len(models) != len(origModels) {
		t.Fatalf("models after rollback = %d, want %d", len(models), len(origModels))
	}

	// Step 9: file at SAME path P == exact bytes A.
	bytesAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytesAfter, bytesA) {
		t.Fatal("file at same path P changed after failed import")
	}

	// Step 10: no imported entities visible.
	if _, err := r.GetRuntime("rt-new"); err == nil {
		t.Fatal("imported runtime visible after rollback")
	}
	if _, err := r.GetModel("m-new"); err == nil {
		t.Fatal("imported model visible after rollback")
	}

	// Step 11: repository remains usable after failure.
	r.writeFunc = fsutil.WriteFileDurable
	if err := r.CreateRuntime(&RuntimeEntry{ID: "rt2", Name: "After", Executable: "/bin/after"}); err != nil {
		t.Fatalf("repo not usable after failed import: %v", err)
	}
	rt2, err := r.GetRuntime("rt2")
	if err != nil || rt2.Name != "After" {
		t.Fatal("post-failure write did not persist")
	}
}

func TestImportGraph_SliceAliasingWithSpareCapacity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := r.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "A", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}

	// Force spare capacity: append 3 entities then remove 2.
	for i := 2; i <= 4; i++ {
		id := "temp" + string(rune('0'+i))
		if err := r.CreateRuntime(&RuntimeEntry{ID: id, Name: "Temp" + string(rune('0'+i)), Executable: "/bin/t"}); err != nil {
			t.Fatal(err)
		}
	}
	// Remove two to create spare capacity in the backing array.
	if err := r.DeleteRuntime("temp3"); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteRuntime("temp4"); err != nil {
		t.Fatal(err)
	}

	// Now the backing array has cap > len.
	r.mu.RLock()
	capacity := cap(r.runtimes)
	length := len(r.runtimes)
	r.mu.RUnlock()
	if capacity <= length {
		t.Fatalf("test setup failed: cap=%d len=%d, need cap>len", capacity, length)
	}

	// Inject write failure at same path.
	r.writeFunc = func(p string, data []byte, perm os.FileMode) error {
		return errors.New("injected write failure")
	}

	err = r.ImportGraph(
		[]*RuntimeEntry{{ID: "import-rt", Name: "Import", Executable: "/bin/new"}},
		nil, nil,
	)
	if err == nil {
		t.Fatal("expected save failure")
	}

	// Verify original state intact despite potential backing-array overlap.
	runtimes, _ := r.ListRuntimes()
	if len(runtimes) != 2 {
		t.Fatalf("expected 2 runtimes (rt1, temp2), got %d", len(runtimes))
	}
	if runtimes[0].ID != "rt1" {
		t.Fatalf("first runtime wrong: %q", runtimes[0].ID)
	}
	if _, err := r.GetRuntime("import-rt"); err == nil {
		t.Fatal("imported entity visible after rollback")
	}
}

func TestImportGraph_ForcedLockOverlap_CaseA(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	// Seed initial state.
	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "Base", Executable: "/bin/base"}); err != nil {
		t.Fatal(err)
	}

	// Channel-based barrier: ImportGraph fires the hook while holding the lock.
	lockHeld := make(chan struct{})
	release := make(chan struct{})
	r.importLockHook = func() {
		close(lockHeld)
		<-release
	}

	importDone := make(chan error, 1)
	go func() {
		err := r.ImportGraph(
			[]*RuntimeEntry{{ID: "import-rt", Name: "ImportRT", Executable: "/bin/import"}},
			nil, nil,
		)
		importDone <- err
	}()

	// Step 2: wait until ImportGraph holds the lock.
	<-lockHeld

	// Step 3: start concurrent CRUD. It signals it has reached the call
	// boundary immediately before invoking the repository mutation.
	crudAttemptStarted := make(chan struct{})
	crudDone := make(chan error, 1)
	go func() {
		close(crudAttemptStarted)
		err := repo.CreateRuntime(&RuntimeEntry{ID: "crud-rt", Name: "CRUD", Executable: "/bin/crud"})
		crudDone <- err
	}()

	// Step 4: wait until CRUD has reached the mutation boundary.
	<-crudAttemptStarted

	// Step 5: prove CRUD cannot complete while ImportGraph holds the lock.
	select {
	case <-crudDone:
		t.Fatal("CRUD completed while ImportGraph held the lock")
	default:
		// Expected: CRUD is blocked on the mutex.
	}

	// Step 5: release the hook.
	close(release)

	// Step 6: ImportGraph completes.
	importErr := <-importDone
	if importErr != nil {
		t.Fatalf("ImportGraph failed after release: %v", importErr)
	}

	// Step 7: CRUD then completes.
	crudErr := <-crudDone
	if crudErr != nil {
		t.Fatalf("CRUD failed after Import released lock: %v", crudErr)
	}

	// Step 8: final state contains both mutations.
	runtimes, _ := repo.ListRuntimes()
	foundImport := false
	foundCRUD := false
	for _, rt := range runtimes {
		if rt.ID == "import-rt" {
			foundImport = true
		}
		if rt.ID == "crud-rt" {
			foundCRUD = true
		}
	}
	if !foundImport {
		t.Fatal("import-rt missing from final state")
	}
	if !foundCRUD {
		t.Fatal("crud-rt missing from final state")
	}

	// Step 9: disk and memory agree.
	r.mu.RLock()
	memCount := len(r.runtimes)
	r.mu.RUnlock()
	if len(runtimes) != memCount {
		t.Fatalf("disk/memory disagree: disk=%d mem=%d", len(runtimes), memCount)
	}

	// Verify disk has both entities.
	diskBytes, _ := os.ReadFile(path)
	if !bytes.Contains(diskBytes, []byte("import-rt")) {
		t.Fatal("import-rt not on disk")
	}
	if !bytes.Contains(diskBytes, []byte("crud-rt")) {
		t.Fatal("crud-rt not on disk")
	}
}

func TestImportGraph_ForcedLockOverlap_CaseB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	// Case B: CRUD owns lock first → ImportGraph waits → Import sees resulting state.
	// We use a write-failure injection to block CRUD while it holds the lock,
	// then start ImportGraph (which will block on the mutex).

	// Block only the first write call (CRUD's save).
	crudLockHeld := make(chan struct{})
	crudRelease := make(chan struct{})
	var blockOnce sync.Once
	origWriteFunc := r.writeFunc
	r.writeFunc = func(p string, data []byte, perm os.FileMode) error {
		blockOnce.Do(func() {
			close(crudLockHeld)
			<-crudRelease
		})
		return origWriteFunc(p, data, perm)
	}

	// Start CRUD in background.
	crudDone := make(chan error, 1)
	go func() {
		err := repo.CreateRuntime(&RuntimeEntry{ID: "crud-rt", Name: "CRUD", Executable: "/bin/crud"})
		crudDone <- err
	}()

	// Wait until CRUD holds the lock (inside writeFunc).
	<-crudLockHeld

	// Now start ImportGraph — it will block on r.mu.Lock().
	importAttemptStarted := make(chan struct{})
	importDone := make(chan error, 1)
	go func() {
		close(importAttemptStarted)
		err := r.ImportGraph(
			[]*RuntimeEntry{{ID: "import-rt", Name: "ImportRT", Executable: "/bin/import"}},
			nil, nil,
		)
		importDone <- err
	}()

	// Wait until ImportGraph has reached the call boundary.
	<-importAttemptStarted

	// Prove ImportGraph cannot complete while CRUD holds the lock.
	select {
	case <-importDone:
		t.Fatal("ImportGraph completed while CRUD held the lock")
	default:
		// Expected: ImportGraph is blocked on the mutex.
	}

	// Release CRUD.
	close(crudRelease)

	// CRUD completes.
	crudErr := <-crudDone
	if crudErr != nil {
		t.Fatalf("CRUD failed: %v", crudErr)
	}

	// ImportGraph now proceeds and sees CRUD's result.
	importErr := <-importDone
	if importErr != nil {
		t.Fatalf("ImportGraph failed: %v", importErr)
	}

	// Both entities present.
	runtimes, _ := repo.ListRuntimes()
	foundCRUD := false
	foundImport := false
	for _, rt := range runtimes {
		if rt.ID == "crud-rt" {
			foundCRUD = true
		}
		if rt.ID == "import-rt" {
			foundImport = true
		}
	}
	if !foundCRUD {
		t.Fatal("crud-rt missing")
	}
	if !foundImport {
		t.Fatal("import-rt missing")
	}
}

func TestImportGraph_ConcurrentSerialization(t *testing.T) {
	dir := t.TempDir()
	repo, err := NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt1", Name: "Base", Executable: "/bin/base"}); err != nil {
		t.Fatal(err)
	}

	const iterations = 20
	var wg sync.WaitGroup

	for i := 0; i < iterations; i++ {
		wg.Add(2)

		go func(i int) {
			defer wg.Done()
			name := "CRUD"
			if i%2 == 1 {
				name = "CRUD-B"
			}
			_ = repo.CreateRuntime(&RuntimeEntry{ID: "crud-rt", Name: name, Executable: "/bin/crud"})
		}(i)

		go func(i int) {
			defer wg.Done()
			_ = r.ImportGraph(
				[]*RuntimeEntry{{ID: "import-rt", Name: "ImportRT", Executable: "/bin/import"}},
				nil, nil,
			)
		}(i)

		wg.Wait()

		runtimes, err := repo.ListRuntimes()
		if err != nil {
			t.Fatalf("iteration %d: list runtimes: %v", i, err)
		}
		seen := make(map[string]bool)
		for _, rt := range runtimes {
			if seen[rt.ID] {
				t.Fatalf("iteration %d: duplicate runtime ID %q", i, rt.ID)
			}
			seen[rt.ID] = true
		}
	}

	runtimes, _ := repo.ListRuntimes()
	r.mu.RLock()
	memCount := len(r.runtimes)
	r.mu.RUnlock()
	if len(runtimes) != memCount {
		t.Fatalf("disk/memory disagree: disk=%d mem=%d", len(runtimes), memCount)
	}
}
