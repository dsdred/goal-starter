package portable

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

func newTestRepo(t *testing.T) storage.Repository {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func seedGraph(t *testing.T, repo storage.Repository) {
	t.Helper()
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT One", Executable: "/bin/one", Environment: map[string]string{"TOKEN": "secret", "ALPHA": "val"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt2", Name: "RT Two", Executable: "/bin/two"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m1", Name: "Model 1", RuntimeID: "rt1", Args: []string{"-m", "model1.gguf"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateModel(&storage.ModelEntry{ID: "m2", Name: "Model 2", RuntimeID: "rt2"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePipeline(&storage.PipelineEntry{
		ID:     "p1",
		Name:   "Pipeline 1",
		Active: true,
		Models: []storage.PipelineModel{
			{ID: "e1", ModelID: "m1"},
			{ID: "e2", ModelID: "m2"},
			{ID: "e3", ModelID: "m1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExportAll(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2", len(b.Runtimes))
	}
	if len(b.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(b.Models))
	}
	if len(b.Pipelines) != 1 {
		t.Fatalf("pipelines = %d, want 1", len(b.Pipelines))
	}
	if len(b.Pipelines[0].Models) != 3 {
		t.Fatalf("pipeline entries = %d, want 3", len(b.Pipelines[0].Models))
	}
}

func TestExport_RuntimeRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	if len(b.Runtimes) != 1 || b.Runtimes[0].ID != "rt1" {
		t.Fatalf("unexpected runtimes: %+v", b.Runtimes)
	}
	if len(b.Models) != 0 {
		t.Fatalf("models should be empty, got %d", len(b.Models))
	}
	if len(b.Pipelines) != 0 {
		t.Fatalf("pipelines should be empty, got %d", len(b.Pipelines))
	}
}

func TestExport_ModelRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	if len(b.Runtimes) != 1 || b.Runtimes[0].ID != "rt1" {
		t.Fatalf("expected runtime rt1, got %+v", b.Runtimes)
	}
	if len(b.Models) != 1 || b.Models[0].ID != "m1" {
		t.Fatalf("expected model m1, got %+v", b.Models)
	}
}

func TestExport_PipelineRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	data, err := ExportBundle(repo, ExportRoot{PipelineID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	if len(b.Pipelines) != 1 || b.Pipelines[0].ID != "p1" {
		t.Fatalf("unexpected pipelines: %+v", b.Pipelines)
	}
	if len(b.Models) != 2 {
		t.Fatalf("models = %d, want 2 (m1, m2 deduped)", len(b.Models))
	}
	if len(b.Runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2 (rt1, rt2)", len(b.Runtimes))
	}
	// Entry order preserved.
	if b.Pipelines[0].Models[0].ID != "e1" || b.Pipelines[0].Models[2].ID != "e3" {
		t.Fatalf("entry order not preserved: %+v", b.Pipelines[0].Models)
	}
	// Repeated model entries preserved.
	if b.Pipelines[0].Models[0].ModelID != "m1" || b.Pipelines[0].Models[2].ModelID != "m1" {
		t.Fatal("repeated model entries lost")
	}
}

func TestExport_UnknownRoot(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	_, err := ExportBundle(repo, ExportRoot{ModelID: "nonexistent"})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if _, ok := err.(*ErrNotFound); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestExport_MultipleRoots(t *testing.T) {
	repo := newTestRepo(t)
	_, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1", ModelID: "m1"})
	if err == nil {
		t.Fatal("expected multiple roots error")
	}
}

func TestExport_EnvironmentKeys(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s", Environment: map[string]string{"ZED": "1", "ALPHA": "2"}}); err != nil {
		t.Fatal(err)
	}

	data, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBundle(data)
	ek := b.Runtimes[0].EnvironmentKeys
	if len(ek) != 2 || ek[0] != "ALPHA" || ek[1] != "ZED" {
		t.Fatalf("environment_keys = %v, want [ALPHA ZED]", ek)
	}
}

func TestExport_NoEnvironmentValues(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.CreateRuntime(&storage.RuntimeEntry{ID: "rt1", Name: "RT", Executable: "/bin/s", Environment: map[string]string{"SECRET": "top-secret-value"}}); err != nil {
		t.Fatal(err)
	}

	data, err := ExportBundle(repo, ExportRoot{RuntimeID: "rt1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Contains(got, "top-secret-value") {
		t.Fatal("secret value leaked in bundle")
	}
}

// tlsKeyNames are the native-HTTPS configuration names (ADR 019 §D3). A
// portable bundle must never carry any of them at any depth: server settings are
// outside Portable Configuration (ADR 014) and TLS material must never enter the
// bundle (ADR 019 §D25, test obligation 12).
var tlsKeyNames = map[string]bool{
	"tls":         true,
	"certfile":    true,
	"keyfile":     true,
	"cert":        true,
	"key":         true,
	"privatekey":  true,
	"certificate": true,
	"httpsport":   true,
	"tlsenabled":  true,
	"minversion":  true,
}

func collectJSONKeyNames(value any, found *[]string) {
	switch node := value.(type) {
	case map[string]any:
		for name, child := range node {
			if tlsKeyNames[strings.ToLower(name)] {
				*found = append(*found, name)
			}
			collectJSONKeyNames(child, found)
		}
	case []any:
		for _, child := range node {
			collectJSONKeyNames(child, found)
		}
	}
}

func assertNoTLSKeyNames(t *testing.T, data []byte, label string) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", label, err)
	}
	var found []string
	collectJSONKeyNames(doc, &found)
	if len(found) > 0 {
		t.Fatalf("%s carries TLS configuration key name(s) %v", label, found)
	}
}

// pemMaterialMarkers are PEM armour fragments. Obligation 12 is about key and
// certificate MATERIAL, not only about field names, so these are searched in every
// string a bundle carries — including values nested inside arrays and objects.
var pemMaterialMarkers = []string{"-----BEGIN", "-----END", "BEGIN PRIVATE KEY", "BEGIN CERTIFICATE"}

// collectJSONStringsAtDepth walks map keys, map values and array elements at any
// depth. Key names are collected too: a PEM header could travel as a key.
func collectJSONStringsAtDepth(value any, out *[]string) {
	switch node := value.(type) {
	case string:
		*out = append(*out, node)
	case map[string]any:
		for name, child := range node {
			*out = append(*out, name)
			collectJSONStringsAtDepth(child, out)
		}
	case []any:
		for _, child := range node {
			collectJSONStringsAtDepth(child, out)
		}
	}
}

func firstPEMMaterial(data []byte) string {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "unparseable document"
	}
	var values []string
	collectJSONStringsAtDepth(doc, &values)
	for _, value := range values {
		for _, marker := range pemMaterialMarkers {
			if strings.Contains(value, marker) {
				return marker + " in " + value
			}
		}
	}
	return ""
}

func assertNoPEMMaterial(t *testing.T, data []byte, label string) {
	t.Helper()
	if hit := firstPEMMaterial(data); hit != "" {
		t.Fatalf("%s carries key or certificate material: %s", label, hit)
	}
}

// TestBundleSchema_HasNoTLSField pins the closed shape of the bundle document,
// so a TLS field cannot be added to it later without failing here.
func TestBundleSchema_HasNoTLSField(t *testing.T) {
	data, err := json.Marshal(Bundle{})
	if err != nil {
		t.Fatal(err)
	}

	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(topLevel))
	for name := range topLevel {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"format", "models", "pipelines", "runtimes", "version"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("bundle top-level keys = %v, want %v: server configuration never joins the bundle", got, want)
	}
	assertNoTLSKeyNames(t, data, "bundle schema")
}

// TestExportBundle_CarriesNoTLSConfiguration checks every export root: the
// exported document is built from the repository alone, so it can describe no
// listener, no certificate path and no key path.
func TestExportBundle_CarriesNoTLSConfiguration(t *testing.T) {
	repo := newTestRepo(t)
	seedGraph(t, repo)

	for _, root := range []ExportRoot{{}, {RuntimeID: "rt1"}, {ModelID: "m1"}, {PipelineID: "p1"}} {
		data, err := ExportBundle(repo, root)
		if err != nil {
			t.Fatal(err)
		}
		label := fmt.Sprintf("export %+v", root)
		assertNoTLSKeyNames(t, data, label)
		assertNoPEMMaterial(t, data, label)
	}
}

// TestPEMMaterialScannerIsNotVacuous is the control for obligation 12: it plants
// key material in a deeply nested bundle value and requires the same scanner that
// stays silent above to report it. Without this control the PEM assertions could
// pass simply because they look at nothing.
func TestPEMMaterialScannerIsNotVacuous(t *testing.T) {
	planted := []byte(`{"format":"goal-portable-config","version":1,` +
		`"runtimes":[{"id":"rt1","name":"RT","executable":"/bin/s","args":["--x",["-----BEGIN PRIVATE KEY-----"]]}],` +
		`"models":[],"pipelines":[]}`)
	if firstPEMMaterial(planted) == "" {
		t.Fatal("vacuous scanner: planted nested key material was not detected")
	}
	benign := []byte(`{"format":"goal-portable-config","version":1,` +
		`"runtimes":[{"id":"rt1","name":"RT","executable":"/bin/s","args":["--api-key=visible-by-contract"]}],"models":[],"pipelines":[]}`)
	if hit := firstPEMMaterial(benign); hit != "" {
		t.Fatalf("scanner reports material where the bundle carries none: %s", hit)
	}

	repo := newTestRepo(t)
	seedGraph(t, repo)
	data, err := ExportBundle(repo, ExportRoot{})
	if err != nil {
		t.Fatal(err)
	}
	assertNoPEMMaterial(t, data, "whole-graph export")
}
