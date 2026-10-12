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
	_, err = r.ImportGraph(
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

	_, err = r.ImportGraph(
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
		_, err := r.ImportGraph(
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
		_, err := r.ImportGraph(
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
			_, _ = r.ImportGraph(
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

// ADR 018 D3/D13: a persistence failure rolls back replaced records (UPDATE) as
// coherently as appended ones (NEW). Neither half of a mixed graph survives, and
// the durable file keeps its exact previous bytes.
func TestImportGraph_PersistenceFailureRollsBackReplacedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{
		ID: "rt1", Name: "Original", Executable: "/bin/orig", WorkingDirectory: "/orig",
		Environment: map[string]string{"SECRET_TOKEN": "keep-me"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&ModelEntry{
		ID: "m1", Name: "Original Model", RuntimeID: "rt1", Args: []string{"--keep"}, Active: true, AutostartDelay: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePipeline(&PipelineEntry{
		ID: "p1", Name: "Original Pipe", Active: true, Models: []PipelineModel{
			{ID: "e1", ModelID: "m1", Args: []string{"--x"}, AutoStart: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rtBefore, err := repo.GetRuntime("rt1")
	if err != nil {
		t.Fatal(err)
	}
	mBefore, err := repo.GetModel("m1")
	if err != nil {
		t.Fatal(err)
	}
	pBefore, err := repo.GetPipeline("p1")
	if err != nil {
		t.Fatal(err)
	}
	bytesBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	r.writeFunc = func(p string, data []byte, perm os.FileMode) error {
		return errors.New("injected write failure")
	}

	// Mixed graph: three restores plus one create, all valid and unconflicted.
	_, err = r.ImportGraph(
		[]*RuntimeEntry{{ID: "rt1", Name: "Restored", Executable: "/bin/restored", WorkingDirectory: "/restored"},
			{ID: "rt-new", Name: "Fresh", Executable: "/bin/fresh"}},
		[]*ModelEntry{{ID: "m1", Name: "Restored Model", RuntimeID: "rt1", Args: []string{"--restored"}, Active: false}},
		[]*PipelineEntry{{ID: "p1", Name: "Restored Pipe", Active: false, Models: []PipelineModel{
			{ID: "e1", ModelID: "m1", Args: []string{"--restored"}, AutoStart: false},
		}}},
	)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("expected injected write failure, got %v", err)
	}

	rtAfter, err := repo.GetRuntime("rt1")
	if err != nil {
		t.Fatal(err)
	}
	if rtAfter.Name != "Original" || rtAfter.Executable != "/bin/orig" || rtAfter.WorkingDirectory != "/orig" {
		t.Fatalf("restored runtime survived a failed write: %+v", rtAfter)
	}
	if rtAfter.Environment["SECRET_TOKEN"] != "keep-me" {
		t.Fatalf("rollback cleared local environment: %+v", rtAfter.Environment)
	}
	if !rtAfter.CreatedAt.Equal(rtBefore.CreatedAt) || !rtAfter.UpdatedAt.Equal(rtBefore.UpdatedAt) {
		t.Fatalf("rollback changed runtime timestamps: %+v vs %+v", rtAfter, rtBefore)
	}

	mAfter, err := repo.GetModel("m1")
	if err != nil {
		t.Fatal(err)
	}
	if mAfter.Name != "Original Model" || mAfter.Active != true || mAfter.AutostartDelay != 5 ||
		len(mAfter.Args) != 1 || mAfter.Args[0] != "--keep" {
		t.Fatalf("restored model survived a failed write: %+v", mAfter)
	}
	if !mAfter.CreatedAt.Equal(mBefore.CreatedAt) || !mAfter.UpdatedAt.Equal(mBefore.UpdatedAt) {
		t.Fatalf("rollback changed model timestamps: %+v vs %+v", mAfter, mBefore)
	}

	pAfter, err := repo.GetPipeline("p1")
	if err != nil {
		t.Fatal(err)
	}
	if pAfter.Name != "Original Pipe" || pAfter.Active != true || len(pAfter.Models) != 1 {
		t.Fatalf("restored pipeline survived a failed write: %+v", pAfter)
	}
	if pAfter.Models[0].ID != pBefore.Models[0].ID || pAfter.Models[0].AutoStart != true ||
		len(pAfter.Models[0].Args) != 1 || pAfter.Models[0].Args[0] != "--x" {
		t.Fatalf("restored pipeline entries survived a failed write: %+v vs %+v", pAfter.Models, pBefore.Models)
	}
	if !pAfter.CreatedAt.Equal(pBefore.CreatedAt) || !pAfter.UpdatedAt.Equal(pBefore.UpdatedAt) {
		t.Fatalf("rollback changed pipeline timestamps: %+v vs %+v", pAfter, pBefore)
	}

	if _, err := repo.GetRuntime("rt-new"); err == nil {
		t.Fatal("created runtime survived a failed write")
	}
	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 1 {
		t.Fatalf("runtime count after rollback = %d, want 1", len(runtimes))
	}

	bytesAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytesBefore, bytesAfter) {
		t.Fatal("failed import changed the durable file")
	}

	r.writeFunc = fsutil.WriteFileDurable
	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt2", Name: "After", Executable: "/bin/after"}); err != nil {
		t.Fatalf("repo not usable after rolled-back import: %v", err)
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(disk, []byte("rt2")) {
		t.Fatal("post-failure write did not persist")
	}
}

// ADR 018 D-15: the planner owns classification, so the repository's pre-write
// name invariant can only fire on a graph that already holds two different IDs
// under one case-insensitive name — a state the planner reports no verdict for,
// because those records are not bundle entities. Such an escaped invariant must
// abort before saveLocked with zero durable writes, name both collided runtimes,
// and leave no in-memory residue.
func TestImportGraph_EscapedCollisionFailsBeforeSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt-dup-a", Name: "Dup", Executable: "/bin/a"}); err != nil {
		t.Fatal(err)
	}
	// Inject the second holder in memory only. Going through CreateRuntime would
	// be rejected by the name uniqueness check, so this is the only way to reach
	// the escaped state.
	r.mu.Lock()
	r.runtimes = append(r.runtimes, &RuntimeEntry{ID: "rt-dup-b", Name: "dup", Executable: "/bin/b"})
	r.mu.Unlock()

	bytesBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	writes := 0
	origWrite := r.writeFunc
	r.writeFunc = func(p string, data []byte, perm os.FileMode) error {
		writes++
		return origWrite(p, data, perm)
	}
	defer func() { r.writeFunc = origWrite }()

	plan, err := r.ImportGraph(
		[]*RuntimeEntry{{ID: "rt-new", Name: "Fresh", Executable: "/bin/new"}}, nil, nil)
	var conflictErr *ErrImportConflict
	if !errorsAsImportConflict(err, &conflictErr) {
		t.Fatalf("expected ErrImportConflict, got %v", err)
	}
	if len(conflictErr.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v", conflictErr.Conflicts)
	}
	if conflictErr.Conflicts[0].ID != "rt-dup-a" || conflictErr.Conflicts[1].ID != "rt-dup-b" {
		t.Fatalf("conflicts must name both collided runtimes: %+v", conflictErr.Conflicts)
	}
	for _, c := range conflictErr.Conflicts {
		if c.Type != "runtime" || c.Reason != ImportBlockedRuntimeNameTaken {
			t.Fatalf("conflict shape = %+v", c)
		}
	}
	if conflictErr.Conflicts[0].RelatedID != "rt-dup-b" || conflictErr.Conflicts[1].RelatedID != "rt-dup-a" {
		t.Fatalf("conflicts must name the other holder: %+v", conflictErr.Conflicts)
	}

	// can_import is derived from the single D-9 predicate, so the escaped conflict
	// must also be visible as a blocked plan.
	if !plan.HasBlocked() || len(plan.Blocked) != 2 {
		t.Fatalf("escaped conflict must report a blocked plan: %+v", plan.Blocked)
	}
	statuses := classificationIndex(plan)
	if statuses["runtime:rt-new"] != ImportStatusNew {
		t.Fatalf("the escape adds no verdict for bundle entities: %+v", statuses)
	}

	if writes != 0 {
		t.Fatalf("escaped collision performed %d durable writes", writes)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytesBefore, after) {
		t.Fatal("escaped collision changed the durable file")
	}
	if _, err := repo.GetRuntime("rt-new"); err == nil {
		t.Fatal("escaped collision left the created runtime in memory")
	}
	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 2 {
		t.Fatalf("runtime count after the escaped collision = %d, want 2", len(runtimes))
	}
}

// ADR 018 obligation 8, case 6: every successful apply leaves a graph with zero
// case-insensitive runtime-name collisions, whatever shape the applied plan had.
func TestImportGraph_AppliedGraphHoldsNoCollisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.json")
	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.(*JSONRepository)

	for _, rt := range []*RuntimeEntry{
		{ID: "rt-a", Name: "Alpha", Executable: "/bin/a"},
		{ID: "rt-b", Name: "Beta", Executable: "/bin/b"},
		{ID: "rt-c", Name: "Gamma", Executable: "/bin/c"},
	} {
		if err := repo.CreateRuntime(rt); err != nil {
			t.Fatal(err)
		}
	}

	assertNoCollisions := func(what string) {
		t.Helper()
		runtimes, err := repo.ListRuntimes()
		if err != nil {
			t.Fatal(err)
		}
		if conflicts := runtimeNameConflicts(runtimes); len(conflicts) > 0 {
			t.Fatalf("%s left a collided graph: %+v", what, conflicts)
		}
	}

	// Mixed create + restore in one write.
	plan, err := r.ImportGraph(
		[]*RuntimeEntry{
			{ID: "rt-a", Name: "Alpha Renewed", Executable: "/bin/a"},
			{ID: "rt-d", Name: "Delta", Executable: "/bin/d"},
		}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Created.Runtimes != 1 || plan.Updated.Runtimes != 1 || !plan.WillMutate() {
		t.Fatalf("plan = %s", countPlan(plan))
	}
	assertNoCollisions("mixed create+restore")
	renamed, err := repo.GetRuntime("rt-a")
	if err != nil || renamed.Name != "Alpha Renewed" {
		t.Fatalf("restore not applied: %+v (%v)", renamed, err)
	}

	// A display-case rename onto a name nobody else holds.
	plan, err = r.ImportGraph([]*RuntimeEntry{{ID: "rt-b", Name: "beta lower", Executable: "/bin/b"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Updated.Runtimes != 1 || plan.Created.Total() != 0 {
		t.Fatalf("plan = %s", countPlan(plan))
	}
	assertNoCollisions("display-case rename restore")

	// A new runtime beside the display-cased record, then an all-UNCHANGED import.
	plan, err = r.ImportGraph(
		[]*RuntimeEntry{
			{ID: "rt-e", Name: "BETA LOWER", Executable: "/bin/e"},
			{ID: "rt-c", Name: "Gamma", Executable: "/bin/c"},
		}, nil, nil)
	if err == nil {
		t.Fatal("a new runtime claiming a display-case twin of a held name must block")
	}
	var conflictErr *ErrImportConflict
	if !errorsAsImportConflict(err, &conflictErr) {
		t.Fatalf("expected ErrImportConflict, got %v", err)
	}
	assertNoCollisions("rejected display-case twin")
	if _, err := repo.GetRuntime("rt-e"); err == nil {
		t.Fatal("blocked import created a runtime")
	}

	bytesBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = r.ImportGraph(
		[]*RuntimeEntry{
			{ID: "rt-a", Name: "Alpha Renewed", Executable: "/bin/a"},
			{ID: "rt-d", Name: "Delta", Executable: "/bin/d"},
		}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WillMutate() || plan.Created.Total() != 0 || plan.Updated.Total() != 0 || plan.Unchanged.Runtimes != 2 {
		t.Fatalf("all-unchanged plan = %s", countPlan(plan))
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytesBefore, after) {
		t.Fatal("all-unchanged import rewrote the durable file")
	}
	assertNoCollisions("all-unchanged import")

	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 4 {
		t.Fatalf("runtime count = %d, want 4", len(runtimes))
	}
}

// ADR 018 D-12 proven at the repository level: two NEW runtimes of one bundle that
// claim one case-insensitive name block each other with no precedence, whichever
// order the bundle arrives in, and neither is written. Over HTTP this shape is a
// bundle-validation 400 before planning, so the planner and the repository are the
// only places it can be exercised.
func TestImportGraph_TwoNewCollidingRuntimesWriteNothing(t *testing.T) {
	first := &RuntimeEntry{ID: "rt-1", Name: "Same", Executable: "/bin/1"}
	second := &RuntimeEntry{ID: "rt-2", Name: "same", Executable: "/bin/2"}
	beside := &RuntimeEntry{ID: "rt-3", Name: "Other", Executable: "/bin/3"}

	for _, bundle := range [][]*RuntimeEntry{{first, second, beside}, {beside, second, first}} {
		path := filepath.Join(t.TempDir(), "repo.json")
		repo, err := NewJSONRepository(path)
		if err != nil {
			t.Fatal(err)
		}
		r := repo.(*JSONRepository)
		if err := repo.CreateRuntime(&RuntimeEntry{ID: "rt-live", Name: "Live", Executable: "/bin/live"}); err != nil {
			t.Fatal(err)
		}
		bytesBefore, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		plan, err := r.ImportGraph(bundle, nil, nil)
		var conflictErr *ErrImportConflict
		if !errorsAsImportConflict(err, &conflictErr) {
			t.Fatalf("expected ErrImportConflict, got %v", err)
		}
		statuses, reasons := classificationIndex(plan), reasonIndex(plan)
		if statuses["runtime:rt-1"] != ImportStatusBlocked || statuses["runtime:rt-2"] != ImportStatusBlocked {
			t.Fatalf("both claimants must block: %+v", statuses)
		}
		if reasons["runtime:rt-1"] != ImportBlockedRuntimeNameTaken || reasons["runtime:rt-2"] != ImportBlockedRuntimeNameTaken {
			t.Fatalf("reasons = %q / %q", reasons["runtime:rt-1"], reasons["runtime:rt-2"])
		}
		// D-14: each side names the lexicographically smallest other holder.
		related := relatedIndex(plan)
		if related["runtime:rt-1"] != "rt-2" || related["runtime:rt-2"] != "rt-1" {
			t.Fatalf("related = %+v", related)
		}
		// An uninvolved new entity is not re-classified by the collision, and it is
		// still not written: the whole plan aborts.
		if statuses["runtime:rt-3"] != ImportStatusNew {
			t.Fatalf("unrelated entity re-classified: %+v", statuses)
		}
		if plan.Created.Runtimes != 1 || plan.Total.Runtimes != 3 || len(plan.Blocked) != 2 {
			t.Fatalf("plan = %s", countPlan(plan))
		}
		if len(conflictErr.Conflicts) != 2 {
			t.Fatalf("conflicts = %+v", conflictErr.Conflicts)
		}

		runtimes, _ := repo.ListRuntimes()
		if len(runtimes) != 1 || runtimes[0].ID != "rt-live" {
			t.Fatalf("blocked plan wrote runtimes: %+v", runtimes)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(bytesBefore, after) {
			t.Fatal("blocked plan rewrote the durable file")
		}
	}
}
