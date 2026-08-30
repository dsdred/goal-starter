package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

const v23RuntimeSecret = "super-secret-runtime-value-v23"

func responseBody(t *testing.T, recorder *httptest.ResponseRecorder) []byte {
	t.Helper()
	body := recorder.Body.Bytes()
	return body
}

func assertValuesAbsent(t *testing.T, body []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if bytes.Contains(body, []byte(value)) {
			t.Fatalf("runtime environment value %q leaked in response: %s", value, body)
		}
	}
	if bytes.Contains(body, []byte(`"environment":`)) {
		t.Fatalf("runtime response exposed environment field: %s", body)
	}
}

func newV23RuntimeHandler(t *testing.T) (storage.Repository, *RuntimesHandler) {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	return repo, NewRuntimesHandler(application.NewRuntimeService(repo), nil, nil, nil)
}

func TestRuntimeResponsesTreatEnvironmentValuesAsWriteOnly(t *testing.T) {
	repo, handler := newV23RuntimeHandler(t)
	createBody := `{"name":"runtime","executable":"runtime.exe","default_args":["serve"],"environment":{"Z_KEY":"z-value-v23","GOAL_RUNTIME_SECRET_V23":"` + v23RuntimeSecret + `","A_KEY":"a-value-v23"}}`
	recorder := httptest.NewRecorder()
	handler.Create(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/runtimes", strings.NewReader(createBody)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status: got %d, body %s", recorder.Code, recorder.Body.String())
	}
	createResponse := responseBody(t, recorder)
	assertValuesAbsent(t, createResponse, v23RuntimeSecret, "a-value-v23", "z-value-v23")
	var created runtimeResponse
	if err := json.Unmarshal(createResponse, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	wantKeys := []string{"A_KEY", "GOAL_RUNTIME_SECRET_V23", "Z_KEY"}
	if !reflect.DeepEqual(created.EnvironmentKeys, wantKeys) {
		t.Fatalf("environment keys: got %#v, want %#v", created.EnvironmentKeys, wantKeys)
	}

	stored, err := repo.GetRuntime(created.ID)
	if err != nil {
		t.Fatalf("get stored runtime: %v", err)
	}
	if stored.Environment["GOAL_RUNTIME_SECRET_V23"] != v23RuntimeSecret {
		t.Fatalf("create response mutated stored environment: %#v", stored.Environment)
	}

	listRecorder := httptest.NewRecorder()
	handler.List(listRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes", nil))
	listBody := responseBody(t, listRecorder)
	assertValuesAbsent(t, listBody, v23RuntimeSecret, "a-value-v23", "z-value-v23")
	var listed []runtimeResponse
	if err := json.Unmarshal(listBody, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("decode list response: count=%d err=%v", len(listed), err)
	}
	if !reflect.DeepEqual(listed[0].EnvironmentKeys, wantKeys) {
		t.Fatalf("list environment keys: got %#v, want %#v", listed[0].EnvironmentKeys, wantKeys)
	}

	getRecorder := httptest.NewRecorder()
	handler.Get(getRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/"+created.ID, nil))
	getBody := responseBody(t, getRecorder)
	assertValuesAbsent(t, getBody, v23RuntimeSecret, "a-value-v23", "z-value-v23")

	stored, err = repo.GetRuntime(created.ID)
	if err != nil || stored.Environment["A_KEY"] != "a-value-v23" || stored.Environment["Z_KEY"] != "z-value-v23" {
		t.Fatalf("read response mutated stored runtime: environment=%#v err=%v", stored.Environment, err)
	}
}

func TestRuntimeUpdatePreservesClearsOrReplacesWriteOnlyEnvironment(t *testing.T) {
	repo, handler := newV23RuntimeHandler(t)
	runtime := &storage.RuntimeEntry{
		ID: "runtime-v23", Name: "before", Executable: "runtime.exe",
		Environment: map[string]string{"GOAL_RUNTIME_SECRET_V23": v23RuntimeSecret, "OTHER": "other-value-v23"},
	}
	if err := repo.CreateRuntime(runtime); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	createdAt := runtime.CreatedAt

	update := func(body string, forbidden ...string) runtimeResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.Update(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/runtimes/"+runtime.ID, strings.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("update status: got %d, body %s", recorder.Code, recorder.Body.String())
		}
		response := responseBody(t, recorder)
		assertValuesAbsent(t, response, forbidden...)
		var decoded runtimeResponse
		if err := json.Unmarshal(response, &decoded); err != nil {
			t.Fatalf("decode update response: %v", err)
		}
		return decoded
	}

	preserved := update(`{"name":"renamed","executable":"runtime.exe","default_args":[]}`, v23RuntimeSecret, "other-value-v23")
	if !reflect.DeepEqual(preserved.EnvironmentKeys, []string{"GOAL_RUNTIME_SECRET_V23", "OTHER"}) {
		t.Fatalf("preserved response keys: %#v", preserved.EnvironmentKeys)
	}
	stored, err := repo.GetRuntime(runtime.ID)
	if err != nil {
		t.Fatalf("get preserved runtime: %v", err)
	}
	if stored.Name != "renamed" || stored.Environment["GOAL_RUNTIME_SECRET_V23"] != v23RuntimeSecret || stored.Environment["OTHER"] != "other-value-v23" {
		t.Fatalf("omitted environment was not preserved: %#v", stored)
	}
	if !stored.CreatedAt.Equal(createdAt) {
		t.Fatalf("unrelated update changed created_at: got %v, want %v", stored.CreatedAt, createdAt)
	}

	cleared := update(`{"name":"renamed","executable":"runtime.exe","default_args":[],"environment":{}}`, v23RuntimeSecret, "other-value-v23")
	if cleared.EnvironmentKeys == nil || len(cleared.EnvironmentKeys) != 0 {
		t.Fatalf("clear response keys: %#v", cleared.EnvironmentKeys)
	}
	stored, _ = repo.GetRuntime(runtime.ID)
	if len(stored.Environment) != 0 {
		t.Fatalf("explicit empty environment did not clear: %#v", stored.Environment)
	}

	replaced := update(`{"name":"renamed","executable":"runtime.exe","default_args":[],"environment":{"NEW_KEY":"new-secret-runtime-v23"}}`, "new-secret-runtime-v23")
	if !reflect.DeepEqual(replaced.EnvironmentKeys, []string{"NEW_KEY"}) {
		t.Fatalf("replace response keys: %#v", replaced.EnvironmentKeys)
	}
	stored, _ = repo.GetRuntime(runtime.ID)
	if len(stored.Environment) != 1 || stored.Environment["NEW_KEY"] != "new-secret-runtime-v23" {
		t.Fatalf("explicit environment replacement failed: %#v", stored.Environment)
	}
}

func TestRuntimeEnvironmentRemainsInternalAcrossModelLifecycle(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	runtime := &storage.RuntimeEntry{
		ID: "runtime-v23", Name: "runtime", Executable: fakeruntime.Path(t),
		WorkingDirectory: t.TempDir(),
		Environment:      map[string]string{"GOAL_RUNTIME_SECRET_V23": v23RuntimeSecret},
	}
	if err := repo.CreateRuntime(runtime); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	supervisor := process.NewSupervisor(repo)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = supervisor.Shutdown(ctx)
	})
	instanceService := application.NewInstanceService(supervisor, repo)
	modelHandler := NewModelsHandler(application.NewModelService(repo), instanceService, supervisor, repo, nil)
	_ = NewInstancesHandler(instanceService, nil)
	runtimeHandler := NewRuntimesHandler(application.NewRuntimeService(repo), instanceService, supervisor, nil)
	_ = NewSystemHandler(supervisor, security.NewSessionStore(), security.NewCSRF(), instanceService)

	createRecorder := httptest.NewRecorder()
	modelHandler.Create(createRecorder, httptest.NewRequest(http.MethodPost, "/api/v1/models", strings.NewReader(`{"name":"model","runtime_id":"runtime-v23","args":["graceful"]}`)))
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("model create status: got %d, body %s", createRecorder.Code, createRecorder.Body.String())
	}
	modelBody := responseBody(t, createRecorder)
	assertValuesAbsent(t, modelBody, v23RuntimeSecret)
	var model modelResponse
	if err := json.Unmarshal(modelBody, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}

	assertResponse := func(name string, expectedStatus int, invoke func(*httptest.ResponseRecorder)) []byte {
		t.Helper()
		recorder := httptest.NewRecorder()
		invoke(recorder)
		if recorder.Code != expectedStatus {
			t.Fatalf("%s status: got %d, body %s", name, recorder.Code, recorder.Body.String())
		}
		body := responseBody(t, recorder)
		assertValuesAbsent(t, body, v23RuntimeSecret)
		return body
	}

	assertResponse("model list", http.StatusOK, func(recorder *httptest.ResponseRecorder) {
		modelHandler.List(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))
	})
	assertResponse("model get", http.StatusOK, func(recorder *httptest.ResponseRecorder) {
		modelHandler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models/"+model.ID, nil))
	})
	assertResponse("model status", http.StatusOK, func(recorder *httptest.ResponseRecorder) {
		modelHandler.Status(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models/"+model.ID+"/status", nil))
	})
	assertResponse("runtime health", http.StatusOK, func(recorder *httptest.ResponseRecorder) {
		runtimeHandler.HealthCheck(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/health", nil))
	})
	assertResponse("runtime health ID", http.StatusNotFound, func(recorder *httptest.ResponseRecorder) {
		runtimeHandler.RuntimeHealth(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/health/"+runtime.ID, nil))
	})
}

func TestRuntimeEnvironmentMergeReachesRealChildProcess(t *testing.T) {
	t.Setenv("GOAL_PARENT_V23", "parent-value-v23")
	proofPath := filepath.Join(t.TempDir(), "environment-proof.txt")
	runtime := &domain.Runtime{
		Executable: fakeruntime.Path(t),
		Environment: map[string]string{
			"GOAL_RUNTIME_SECRET_V23":  v23RuntimeSecret,
			"GOAL_PARENT_V23":          "runtime-value-v23",
			"GOAL_MODEL_OVERRIDE_V23":  "runtime-value-v23",
			"GOAL_CUSTOM_OVERRIDE_V23": "runtime-value-v23",
		},
	}
	model := &domain.Model{
		Args: []string{
			"env-file", proofPath,
			"GOAL_RUNTIME_SECRET_V23", "GOAL_PARENT_V23", "GOAL_MODEL_OVERRIDE_V23", "GOAL_CUSTOM_OVERRIDE_V23",
		},
		Environment: map[string]string{
			"GOAL_MODEL_OVERRIDE_V23":  "model-value-v23",
			"GOAL_CUSTOM_OVERRIDE_V23": "model-value-v23",
		},
	}
	custom := map[string]string{"GOAL_CUSTOM_OVERRIDE_V23": "custom-value-v23"}
	spec, err := domain.NewLaunchResolver().Resolve(model, runtime, nil, custom)
	if err != nil {
		t.Fatalf("resolve command: %v", err)
	}

	manager := process.NewManager()
	if err := manager.Start(context.Background(), process.CommandSpec{
		Executable: spec.Executable, Args: spec.Args, WorkingDirectory: spec.WorkingDirectory, Environment: spec.Environment,
	}); err != nil {
		t.Fatalf("start real child: %v", err)
	}
	done := manager.GetDoneChannel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for environment proof child")
	}

	proof, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatalf("read environment proof: %v", err)
	}
	for _, expected := range []string{
		"GOAL_RUNTIME_SECRET_V23=" + v23RuntimeSecret,
		"GOAL_PARENT_V23=runtime-value-v23",
		"GOAL_MODEL_OVERRIDE_V23=model-value-v23",
		"GOAL_CUSTOM_OVERRIDE_V23=custom-value-v23",
	} {
		if !bytes.Contains(proof, []byte(expected)) {
			t.Fatalf("environment proof omitted %q: %s", expected, proof)
		}
	}
}

func TestRuntimeRedactionIsIdenticalWithAuthOffAndOn(t *testing.T) {
	for _, authEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "auth_off", true: "auth_on"}[authEnabled], func(t *testing.T) {
			repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
			if err != nil {
				t.Fatalf("create repository: %v", err)
			}
			runtime := &storage.RuntimeEntry{ID: "runtime-auth-v23", Name: "runtime", Executable: "runtime.exe", Environment: map[string]string{"GOAL_RUNTIME_SECRET_V23": v23RuntimeSecret}}
			if err := repo.CreateRuntime(runtime); err != nil {
				t.Fatalf("create runtime: %v", err)
			}
			supervisor := process.NewSupervisor(repo)
			passwords := security.NewPasswordStore()
			if err := passwords.SetPassword("admin", "secret"); err != nil {
				t.Fatalf("set password: %v", err)
			}
			assets := fstest.MapFS{"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
			router := NewRouteRegistry(
				application.NewInstanceService(supervisor, repo),
				application.NewRuntimeService(repo), application.NewModelService(repo),
				application.NewPipelineService(supervisor, repo),
				supervisor, repo,
				security.NewCSRF(), security.NewSessionStore(), passwords,
				WithAuthEnabled(authEnabled), WithWebAssets(fs.FS(assets), fs.FS(assets)),
			).Build()

			var cookies []*http.Cookie
			if authEnabled {
				login := httptest.NewRecorder()
				router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret"}`)))
				if login.Code != http.StatusOK {
					t.Fatalf("login status: got %d, body %s", login.Code, login.Body.String())
				}
				cookies = login.Result().Cookies()
			}

			for _, path := range []string{"/api/v1/runtimes", "/api/v1/runtimes/" + runtime.ID} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusOK {
					t.Fatalf("GET %s status: got %d, body %s", path, recorder.Code, recorder.Body.String())
				}
				assertValuesAbsent(t, recorder.Body.Bytes(), v23RuntimeSecret)
				if !bytes.Contains(recorder.Body.Bytes(), []byte("GOAL_RUNTIME_SECRET_V23")) {
					t.Fatalf("GET %s omitted environment key: %s", path, recorder.Body.String())
				}
			}
		})
	}
}
