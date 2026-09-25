package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

// --- SKIP EXISTING plan through HTTP ---

type planTypeCounts struct {
	Total    int `json:"total"`
	New      int `json:"new"`
	Existing int `json:"existing"`
	Blocked  int `json:"blocked"`
}

type importBodyCounts struct {
	Runtimes  int `json:"runtimes"`
	Models    int `json:"models"`
	Pipelines int `json:"pipelines"`
}

type importResponseBody struct {
	DryRun    bool `json:"dry_run"`
	CanImport bool `json:"can_import"`
	Summary   struct {
		Runtimes  planTypeCounts `json:"runtimes"`
		Models    planTypeCounts `json:"models"`
		Pipelines planTypeCounts `json:"pipelines"`
	} `json:"summary"`
	Created importBodyCounts `json:"created"`
	Skipped importBodyCounts `json:"skipped"`
	Blocked []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Reason    string `json:"reason"`
		RelatedID string `json:"related_id"`
	} `json:"blocked"`
	Runtimes int `json:"runtimes"`
	Models   int `json:"models"`
}

func decodeImportBody(t *testing.T, resp *httptest.ResponseRecorder) importResponseBody {
	t.Helper()
	var body importResponseBody
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import body: %v\n%s", err, resp.Body.String())
	}
	return body
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

// §13.1 — a file describing only existing entities is valid, plans zero
// creations and is not blocked, so import is not offered.
func TestImport_DryRun_AllExisting_NothingToImport(t *testing.T) {
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
		t.Fatalf("existing entities must not be reported as blocked: %+v", body.Blocked)
	}
	if body.Summary.Runtimes.Existing != 1 || body.Summary.Runtimes.New != 0 {
		t.Fatalf("summary = %+v", body.Summary.Runtimes)
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

// §13.3 + §13.10 — mixed plan: existing runtime skipped, new model created.
func TestImport_MixedPlan_CreatedAndSkipped(t *testing.T) {
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
	if body.Created.Models != 1 || body.Skipped.Runtimes != 1 {
		t.Fatalf("created/skipped = %+v / %+v", body.Created, body.Skipped)
	}
	if body.Summary.Models.New != 1 || body.Summary.Runtimes.Existing != 1 {
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
