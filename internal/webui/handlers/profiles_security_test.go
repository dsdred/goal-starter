package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

const v22SecretMarker = "super-secret-value-v22"

func TestMain(m *testing.M) {
	code := m.Run()
	if err := fakeruntime.Cleanup(); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

func assertSecretAbsent(t *testing.T, body []byte) {
	t.Helper()
	if bytes.Contains(body, []byte(v22SecretMarker)) {
		t.Fatalf("profile environment value leaked in response: %s", body)
	}
}

func responseBody(t *testing.T, recorder *httptest.ResponseRecorder) []byte {
	t.Helper()
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return body
}

func newV22ProfileHandler(t *testing.T) (storage.Repository, *ProfilesHandler) {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	profileService := application.NewProfileService(repo)
	return repo, NewProfilesHandler(profileService, nil, process.NewSupervisor(repo), nil)
}

func TestProfileResponsesTreatEnvironmentValuesAsWriteOnly(t *testing.T) {
	repo, handler := newV22ProfileHandler(t)

	createBody := `{"name":"secret profile","runtime_id":"runtime-1","host":"127.0.0.1","port":8080,"environment":{"GOAL_TEST_SECRET":"` + v22SecretMarker + `"}}`
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, httptest.NewRequest(http.MethodPost, "/api/v1/profiles", strings.NewReader(createBody)))
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status: got %d, body %s", createRecorder.Code, createRecorder.Body.String())
	}
	createResponse := responseBody(t, createRecorder)
	assertSecretAbsent(t, createResponse)
	var created profileResponse
	if err := json.Unmarshal(createResponse, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(created.EnvironmentKeys) != 1 || created.EnvironmentKeys[0] != "GOAL_TEST_SECRET" {
		t.Fatalf("create response keys: %#v", created.EnvironmentKeys)
	}

	listRecorder := httptest.NewRecorder()
	handler.List(listRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil))
	assertSecretAbsent(t, responseBody(t, listRecorder))

	getRecorder := httptest.NewRecorder()
	handler.Get(getRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/profiles/"+created.ID, nil))
	assertSecretAbsent(t, responseBody(t, getRecorder))

	stored, err := repo.GetProfile(created.ID)
	if err != nil {
		t.Fatalf("get stored profile: %v", err)
	}
	if stored.Environment["GOAL_TEST_SECRET"] != v22SecretMarker {
		t.Fatalf("redaction mutated stored environment: %#v", stored.Environment)
	}
}

func TestProfileUpdatePreservesOrExplicitlyChangesWriteOnlyEnvironment(t *testing.T) {
	repo, handler := newV22ProfileHandler(t)
	profile := &storage.ProfileEntry{
		Name: "before", RuntimeID: "runtime-1", Host: "127.0.0.1", Port: 8080,
		Environment: map[string]string{"GOAL_TEST_SECRET": v22SecretMarker},
	}
	if err := repo.CreateProfile(profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	update := func(body string) []byte {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.Update(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/profiles/"+profile.ID, strings.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("update status: got %d, body %s", recorder.Code, recorder.Body.String())
		}
		response := responseBody(t, recorder)
		assertSecretAbsent(t, response)
		return response
	}

	update(`{"name":"renamed","runtime_id":"runtime-1","host":"127.0.0.1","port":8081}`)
	stored, err := repo.GetProfile(profile.ID)
	if err != nil {
		t.Fatalf("get preserved profile: %v", err)
	}
	if stored.Name != "renamed" || stored.Environment["GOAL_TEST_SECRET"] != v22SecretMarker {
		t.Fatalf("unrelated update lost secret: %#v", stored)
	}

	update(`{"name":"renamed","runtime_id":"runtime-1","host":"127.0.0.1","port":8081,"environment":{"GOAL_TEST_SECRET":"replacement-v22"}}`)
	stored, _ = repo.GetProfile(profile.ID)
	if stored.Environment["GOAL_TEST_SECRET"] != "replacement-v22" {
		t.Fatalf("explicit environment replacement failed: %#v", stored.Environment)
	}

	update(`{"name":"renamed","runtime_id":"runtime-1","host":"127.0.0.1","port":8081,"environment":{}}`)
	stored, _ = repo.GetProfile(profile.ID)
	if len(stored.Environment) != 0 {
		t.Fatalf("explicit environment removal failed: %#v", stored.Environment)
	}
}

func TestProfilePreviewReturnsEnvironmentKeysWithoutValues(t *testing.T) {
	repo, handler := newV22ProfileHandler(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	runtime := &storage.RuntimeEntry{
		ID: "runtime-1", Name: "runtime", Executable: executable,
		Environment: map[string]string{"RUNTIME_SECRET": v22SecretMarker},
	}
	if err := repo.CreateRuntime(runtime); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	profile := &storage.ProfileEntry{
		ID: "profile-1", Name: "profile", RuntimeID: runtime.ID,
		Environment: map[string]string{"GOAL_TEST_SECRET": v22SecretMarker},
	}
	if err := repo.CreateProfile(profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.Resolve(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/profiles/"+profile.ID+"/resolve", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status: got %d, body %s", recorder.Code, recorder.Body.String())
	}
	body := responseBody(t, recorder)
	assertSecretAbsent(t, body)
	if !bytes.Contains(body, []byte("GOAL_TEST_SECRET")) {
		t.Fatalf("preview omitted environment key: %s", body)
	}

	stored, err := repo.GetProfile(profile.ID)
	if err != nil {
		t.Fatalf("get profile after preview: %v", err)
	}
	if stored.Environment["GOAL_TEST_SECRET"] != v22SecretMarker {
		t.Fatal("preview redaction changed the stored secret")
	}
}

func TestProfileServicePreservesSecretForInternalRuntimeUse(t *testing.T) {
	repo, _ := newV22ProfileHandler(t)
	profile := &storage.ProfileEntry{
		Name: "profile", RuntimeID: "runtime-1",
		Environment: map[string]string{"GOAL_TEST_SECRET": v22SecretMarker},
	}
	if err := repo.CreateProfile(profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	stored, err := application.NewProfileService(repo).GetProfile(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("internal get profile: %v", err)
	}
	if stored.Environment["GOAL_TEST_SECRET"] != v22SecretMarker {
		t.Fatalf("internal profile lost required environment: %#v", stored.Environment)
	}
}

func TestLifecycleResponsesNeverExposeProfileEnvironmentValues(t *testing.T) {
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	runtime := &storage.RuntimeEntry{
		ID: "runtime-v22", Name: "runtime", Executable: fakeruntime.Path(t),
		WorkingDirectory: t.TempDir(), DefaultArgs: []string{"graceful"},
	}
	if err := repo.CreateRuntime(runtime); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	profile := &storage.ProfileEntry{
		ID: "profile-v22", Name: "profile", RuntimeID: runtime.ID,
		Environment: map[string]string{"GOAL_TEST_SECRET": v22SecretMarker},
	}
	if err := repo.CreateProfile(profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	supervisor := process.NewSupervisor(repo)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = supervisor.Shutdown(ctx)
	})
	instanceService := application.NewInstanceService(supervisor, repo)
	profileHandler := NewProfilesHandler(application.NewProfileService(repo), instanceService, supervisor, nil)
	instanceHandler := NewInstancesHandler(instanceService, nil)
	runtimeHandler := NewRuntimesHandler(application.NewRuntimeService(repo), instanceService, supervisor, nil)

	startRecorder := httptest.NewRecorder()
	profileHandler.Start(startRecorder, httptest.NewRequest(http.MethodPost, "/api/v1/profiles/"+profile.ID+"/start", nil))
	if startRecorder.Code != http.StatusOK {
		t.Fatalf("start status: got %d, body %s", startRecorder.Code, startRecorder.Body.String())
	}
	startBody := responseBody(t, startRecorder)
	assertSecretAbsent(t, startBody)
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(startBody, &started); err != nil || started.ID == "" {
		t.Fatalf("decode started instance: id=%q err=%v", started.ID, err)
	}
	internal, err := supervisor.Status(processInstanceID(started.ID))
	if err != nil {
		t.Fatalf("internal instance status: %v", err)
	}
	if internal.Environment["GOAL_TEST_SECRET"] != v22SecretMarker {
		t.Fatalf("runtime instance lost required environment: %#v", internal.Environment)
	}

	assertHandlerSecretAbsent := func(name string, invoke func(*httptest.ResponseRecorder)) {
		t.Helper()
		recorder := httptest.NewRecorder()
		invoke(recorder)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status: got %d, body %s", name, recorder.Code, recorder.Body.String())
		}
		assertSecretAbsent(t, responseBody(t, recorder))
	}

	assertHandlerSecretAbsent("instance list", func(recorder *httptest.ResponseRecorder) {
		instanceHandler.List(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil))
	})
	assertHandlerSecretAbsent("instance get", func(recorder *httptest.ResponseRecorder) {
		instanceHandler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+started.ID, nil))
	})
	assertHandlerSecretAbsent("instance status", func(recorder *httptest.ResponseRecorder) {
		instanceHandler.Status(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/instances/status", nil))
	})
	assertHandlerSecretAbsent("runtime health", func(recorder *httptest.ResponseRecorder) {
		runtimeHandler.HealthCheck(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/health", nil))
	})
	assertHandlerSecretAbsent("profile status", func(recorder *httptest.ResponseRecorder) {
		profileHandler.Status(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/profiles/"+profile.ID+"/status", nil))
	})
	assertHandlerSecretAbsent("profile restart", func(recorder *httptest.ResponseRecorder) {
		profileHandler.Restart(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/profiles/"+profile.ID+"/restart", nil))
	})
	assertHandlerSecretAbsent("instance restart", func(recorder *httptest.ResponseRecorder) {
		instanceHandler.RestartInstance(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/instances/"+started.ID+"/restart", nil))
	})
}

func processInstanceID(id string) domain.InstanceID {
	return domain.InstanceID(id)
}
