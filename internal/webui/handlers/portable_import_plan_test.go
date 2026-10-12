package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

// --- Import plan through HTTP (ADR 018 Slice 2′: summary.{total,new,update,
// unchanged,blocked} per type, with created/updated/unchanged result counts) ---

type planTypeCounts struct {
	Total     int `json:"total"`
	New       int `json:"new"`
	Update    int `json:"update"`
	Unchanged int `json:"unchanged"`
	Blocked   int `json:"blocked"`
}

type importBodyCounts struct {
	Runtimes  int `json:"runtimes"`
	Models    int `json:"models"`
	Pipelines int `json:"pipelines"`
}

func (c importBodyCounts) Total() int { return c.Runtimes + c.Models + c.Pipelines }

type importResponseBody struct {
	DryRun    bool `json:"dry_run"`
	CanImport bool `json:"can_import"`
	Summary   struct {
		Runtimes  planTypeCounts `json:"runtimes"`
		Models    planTypeCounts `json:"models"`
		Pipelines planTypeCounts `json:"pipelines"`
	} `json:"summary"`
	Created   importBodyCounts `json:"created"`
	Updated   importBodyCounts `json:"updated"`
	Unchanged importBodyCounts `json:"unchanged"`
	Blocked   []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Reason    string `json:"reason"`
		RelatedID string `json:"related_id"`
	} `json:"blocked"`
}

func decodeImportBody(t *testing.T, resp *httptest.ResponseRecorder) importResponseBody {
	t.Helper()
	var body importResponseBody
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import body: %v\n%s", err, resp.Body.String())
	}
	return body
}

func topLevelKeys(t *testing.T, resp *httptest.ResponseRecorder) []string {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw import body: %v\n%s", err, resp.Body.String())
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	return keys
}

func assertKeySet(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, k := range want {
		set[k] = true
	}
	if len(got) != len(want) {
		t.Fatalf("key set = %v, want exactly %v", got, want)
	}
	for _, k := range got {
		if !set[k] {
			t.Fatalf("unexpected key %q in %v", k, got)
		}
	}
}

// ADR 018 D-10/D-16: the retired vocabulary is gone from the body, not merely
// unused. The plan body carries exactly the ratified fields, the summary carries
// exactly the five per-type counts, and no response field is named existing or
// skipped.
func TestImport_Wire_CarriesOnlyRatifiedFields(t *testing.T) {
	planKeys := []string{"dry_run", "can_import", "summary", "created", "updated", "unchanged", "blocked"}
	summaryKeys := []string{"total", "new", "update", "unchanged", "blocked"}

	validatePlan := func(resp *httptest.ResponseRecorder, what string) {
		t.Helper()
		assertKeySet(t, topLevelKeys(t, resp), planKeys...)
		var raw struct {
			Summary map[string]map[string]json.RawMessage `json:"summary"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		assertKeySet(t, keysOf(raw.Summary), "runtimes", "models", "pipelines")
		for _, typ := range []string{"runtimes", "models", "pipelines"} {
			assertKeySet(t, keysOf(raw.Summary[typ]), summaryKeys...)
		}
		for _, retired := range []string{`"existing"`, `"skipped"`} {
			if strings.Contains(resp.Body.String(), retired+":") {
				t.Fatalf("%s still carries the retired field %s", what, retired)
			}
		}
	}

	// A mixed plan: one unchanged runtime, one new model.
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-new", Name: "NewRT", Executable: "/bin/new"}); err != nil {
		t.Fatal(err)
	}
	bundle := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt-new","name":"NewRT","executable":"/bin/new"}],"models":[{"id":"m-new","name":"New Model","runtime_id":"rt-new"}],"pipelines":[]}`)

	validate := httptest.NewRecorder()
	router.ServeHTTP(validate, httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(bundle)))
	if validate.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", validate.Code, validate.Body.String())
	}
	validatePlan(validate, "200 dry-run body")

	applied := httptest.NewRecorder()
	router.ServeHTTP(applied, httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(bundle)))
	if applied.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", applied.Code, applied.Body.String())
	}
	assertKeySet(t, topLevelKeys(t, applied), planKeys...)

	// A blocked plan adds only the error envelope fields. rt-new "NewRT" is in the
	// repository now, so a different ID claiming that name blocks.
	clashing := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt-clash","name":"newrt","executable":"/bin/clash"}],"models":[],"pipelines":[]}`)
	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(clashing)))
	if blocked.Code != http.StatusConflict {
		t.Fatalf("expected a blocked plan for the claimed name: %d %s", blocked.Code, blocked.Body.String())
	}
	assertKeySet(t, topLevelKeys(t, blocked), append(append([]string{}, planKeys...), "error", "code", "details")...)
	for _, retired := range []string{`"existing"`, `"skipped"`} {
		if strings.Contains(blocked.Body.String(), retired+":") {
			t.Fatalf("409 body still carries the retired field %s", retired)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// §13.12 — a blocked plan is rejected with 409 and still reports the plan, so
// the caller can show counts and reasons instead of a bare collision dump.
func TestImport_BlockedPlan_409CarriesPlan(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	// Another ID already owns the runtime name the bundle wants.
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-other", Name: "NewRT", Executable: "/bin/other"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", resp.Code, resp.Body.String())
	}
	body := decodeImportBody(t, resp)
	if body.CanImport {
		t.Fatal("blocked plan must not report can_import")
	}
	if len(body.Blocked) != 1 || body.Blocked[0].ID != "rt-new" {
		t.Fatalf("blocked = %+v", body.Blocked)
	}
	if body.Blocked[0].Reason != storage.ImportBlockedRuntimeNameTaken {
		t.Fatalf("reason = %q", body.Blocked[0].Reason)
	}
	if body.Blocked[0].RelatedID != "rt-other" {
		t.Fatalf("related_id = %q", body.Blocked[0].RelatedID)
	}
	if body.Summary.Runtimes.Blocked != 1 || body.Summary.Runtimes.New != 0 {
		t.Fatalf("summary = %+v", body.Summary.Runtimes)
	}
	// The blocked plan describes zero mutation: nothing was created or restored.
	if body.Created.Total() != 0 || body.Updated.Total() != 0 {
		t.Fatalf("blocked plan claims writes: %+v %+v", body.Created, body.Updated)
	}

	var envelope struct {
		Error   string   `json:"error"`
		Code    string   `json:"code"`
		Details []string `json:"details"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != "conflict" || envelope.Error == "" || len(envelope.Details) != 1 {
		t.Fatalf("error envelope = %+v", envelope)
	}

	// Nothing was written.
	runtimes, _ := repo.ListRuntimes()
	if len(runtimes) != 1 {
		t.Fatalf("blocked import changed the repository: %d runtimes", len(runtimes))
	}
}

// ADR 018 D-14: the technical detail presents the colliding counterpart as
// related, never with the retired existing wording.
func TestImport_BlockedPlan_DetailsUseRelatedVocabulary(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-other", Name: "NewRT", Executable: "/bin/other"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", resp.Code, resp.Body.String())
	}

	var envelope struct {
		Details []string `json:"details"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Details) != 1 {
		t.Fatalf("details = %+v", envelope.Details)
	}
	d := envelope.Details[0]
	if !strings.Contains(d, "(related: rt-other)") {
		t.Fatalf("detail must present the counterpart as related: %q", d)
	}
	if strings.Contains(d, "(existing:") {
		t.Fatalf("detail carries the retired existing wording: %q", d)
	}
}

// ADR 018 D5: a Pipeline with no model entries cannot exist in the repository, so
// the plan blocks it with pipeline_entries_empty rather than offering a write
// that must fail. The file itself is valid, which is why validation answers 200.
func TestImport_EmptyPipeline_BlockedWithReason(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	bundle := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[],"models":[],"pipelines":[{"id":"p-empty","name":"Empty","models":[]}]}`)

	validate := httptest.NewRecorder()
	router.ServeHTTP(validate, httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(bundle)))
	if validate.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", validate.Code, validate.Body.String())
	}
	body := decodeImportBody(t, validate)
	if body.CanImport {
		t.Fatal("a plan that cannot be applied must not report can_import")
	}
	if len(body.Blocked) != 1 || body.Blocked[0].ID != "p-empty" {
		t.Fatalf("blocked = %+v", body.Blocked)
	}
	if body.Blocked[0].Reason != storage.ImportBlockedPipelineEntriesEmpty {
		t.Fatalf("reason = %q, want %q", body.Blocked[0].Reason, storage.ImportBlockedPipelineEntriesEmpty)
	}
	if body.Summary.Pipelines.Blocked != 1 || body.Summary.Pipelines.New != 0 {
		t.Fatalf("summary = %+v", body.Summary.Pipelines)
	}

	conflict := httptest.NewRecorder()
	router.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(bundle)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", conflict.Code, conflict.Body.String())
	}
	if got := decodeImportBody(t, conflict); len(got.Blocked) != 1 ||
		got.Blocked[0].Reason != storage.ImportBlockedPipelineEntriesEmpty {
		t.Fatalf("conflict body = %+v", got.Blocked)
	}

	// Nothing was written, in either call.
	pipelines, _ := repo.ListPipelines()
	if len(pipelines) != 0 {
		t.Fatalf("blocked import created a pipeline: %+v", pipelines)
	}
}

// §13.1 — a file describing only up-to-date entities is valid, plans zero writes
// and is not blocked, so import is not offered.
func TestImport_DryRun_AllUnchanged_NothingToImport(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-new", Name: "NewRT", Executable: "/bin/new"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	body := decodeImportBody(t, resp)
	if !body.DryRun {
		t.Fatal("dry_run flag missing")
	}
	if body.CanImport {
		t.Fatal("nothing-to-import must not enable import")
	}
	if len(body.Blocked) != 0 {
		t.Fatalf("unchanged entities must not be reported as blocked: %+v", body.Blocked)
	}
	if body.Summary.Runtimes.Unchanged != 1 || body.Summary.Runtimes.New != 0 || body.Summary.Runtimes.Update != 0 {
		t.Fatalf("summary = %+v", body.Summary.Runtimes)
	}
	if body.Unchanged.Runtimes != 1 || body.Created.Total() != 0 || body.Updated.Total() != 0 {
		t.Fatalf("counts = %+v", body)
	}
	if _, err := repo.GetRuntime("rt-new"); err != nil {
		t.Fatal("seeded runtime disappeared")
	}
}

// A blocked plan is still reported by validation as a valid file whose import is
// impossible: file validity and repository conflicts stay separate.
func TestImport_DryRun_BlockedReportsPlanNotError(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-other", Name: "newrt", Executable: "/bin/other"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(validBundle()))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	body := decodeImportBody(t, resp)
	if body.CanImport {
		t.Fatal("blocked plan must not enable import")
	}
	if body.Summary.Runtimes.Blocked != 1 {
		t.Fatalf("summary = %+v", body.Summary.Runtimes)
	}
	if _, err := repo.GetRuntime("rt-new"); err == nil {
		t.Fatal("dry-run wrote state")
	}
}

// §13.3 + §13.10 — mixed plan: unchanged runtime, new model.
func TestImport_MixedPlan_CreatedAndUnchanged(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-new", Name: "NewRT", Executable: "/bin/new"}); err != nil {
		t.Fatal(err)
	}

	bundle := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt-new","name":"NewRT","executable":"/bin/new"}],"models":[{"id":"m-new","name":"New Model","runtime_id":"rt-new","args":["--port","8080"]}],"pipelines":[]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(bundle))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	body := decodeImportBody(t, resp)
	if !body.CanImport {
		t.Fatal("new model must make import available")
	}
	if body.Created.Models != 1 || body.Unchanged.Runtimes != 1 || body.Updated.Total() != 0 {
		t.Fatalf("created/updated/unchanged = %+v / %+v / %+v", body.Created, body.Updated, body.Unchanged)
	}
	if body.Summary.Models.New != 1 || body.Summary.Runtimes.Unchanged != 1 {
		t.Fatalf("summary = %+v", body.Summary)
	}
	if _, err := repo.GetModel("m-new"); err != nil {
		t.Fatal("new model referencing an existing runtime was not created")
	}
	rt, _ := repo.GetRuntime("rt-new")
	if rt.Executable != "/bin/new" {
		t.Fatalf("runtime overwritten: %+v", rt)
	}
}

// ADR 018 D4/D16: a pending restore is visible in the plan before the write and
// in the result counts after it, with the same numbers describing the same plan.
func TestImport_PendingRestore_PlanThenApply(t *testing.T) {
	router, repo := newPortableTestRouter(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-new", Name: "Stored", Executable: "/bin/stored"}); err != nil {
		t.Fatal(err)
	}

	dry := httptest.NewRecorder()
	router.ServeHTTP(dry, httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(validBundle())))
	if dry.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", dry.Code, dry.Body.String())
	}
	plan := decodeImportBody(t, dry)
	if plan.Summary.Runtimes.Update != 1 || plan.Summary.Runtimes.New != 0 || plan.Summary.Runtimes.Blocked != 0 {
		t.Fatalf("plan summary = %+v", plan.Summary.Runtimes)
	}
	if plan.Updated.Runtimes != 1 || !plan.CanImport {
		t.Fatalf("plan counts = %+v", plan)
	}
	if rt, _ := repo.GetRuntime("rt-new"); rt.Name != "Stored" {
		t.Fatalf("dry run applied the restore: %+v", rt)
	}

	applied := httptest.NewRecorder()
	router.ServeHTTP(applied, httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(validBundle())))
	if applied.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", applied.Code, applied.Body.String())
	}
	res := decodeImportBody(t, applied)
	if res.Updated.Runtimes != 1 || res.Created.Total() != 0 || res.Unchanged.Total() != 0 {
		t.Fatalf("result counts = %+v", res)
	}
	if res.Summary.Runtimes.Update != 1 || res.Summary.Runtimes.Total != 1 {
		t.Fatalf("result summary = %+v", res.Summary.Runtimes)
	}
	rt, _ := repo.GetRuntime("rt-new")
	if rt.Name != "NewRT" || rt.Executable != "/bin/new" {
		t.Fatalf("restore not applied: %+v", rt)
	}

	// Re-importing the applied file now reports Up to date and disables import.
	again := httptest.NewRecorder()
	router.ServeHTTP(again, httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(validBundle())))
	second := decodeImportBody(t, again)
	if second.Unchanged.Runtimes != 1 || second.Updated.Total() != 0 || second.CanImport {
		t.Fatalf("re-import = %+v", second)
	}
}

// §13.9 through HTTP — a plan that validated as importable is rebuilt against
// current state; a new blocking conflict aborts the write.
func TestImport_StalePlanRejected_HTTP(t *testing.T) {
	router, repo := newPortableTestRouter(t)

	bundle := []byte(`{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt-a","name":"Plan RT","executable":"/bin/a"}],"models":[],"pipelines":[]}`)

	validate := httptest.NewRecorder()
	router.ServeHTTP(validate, httptest.NewRequest(http.MethodPost, "/api/v1/import?dry_run=true", bytes.NewReader(bundle)))
	if !decodeImportBody(t, validate).CanImport {
		t.Fatalf("expected the plan to be importable: %s", validate.Body.String())
	}

	// Competing mutation claims the name under a different ID.
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt-competing", Name: "plan rt", Executable: "/bin/c"}); err != nil {
		t.Fatal(err)
	}

	imp := httptest.NewRecorder()
	router.ServeHTTP(imp, httptest.NewRequest(http.MethodPost, "/api/v1/import", bytes.NewReader(bundle)))
	if imp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", imp.Code, imp.Body.String())
	}
	if _, err := repo.GetRuntime("rt-a"); err == nil {
		t.Fatal("stale plan created an entity")
	}
}
