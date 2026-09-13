package portable

import (
	"strings"
	"testing"
)

func TestParseBundle_Valid(t *testing.T) {
	data := `{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt1","name":"RT","executable":"/bin/server"}],"models":[],"pipelines":[]}`
	b, err := ParseBundle([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if b.Format != FormatIdentity {
		t.Fatalf("format = %q", b.Format)
	}
	if b.Version != 1 {
		t.Fatalf("version = %d", b.Version)
	}
	if len(b.Runtimes) != 1 || b.Runtimes[0].ID != "rt1" {
		t.Fatalf("runtimes = %+v", b.Runtimes)
	}
}

func TestParseBundle_MalformedJSON(t *testing.T) {
	_, err := ParseBundle([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ErrMalformedBundle); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestParseBundle_TrailingData(t *testing.T) {
	data := `{"format":"goal-portable-config","version":1}{"extra":true}`
	_, err := ParseBundle([]byte(data))
	if err == nil {
		t.Fatal("expected error for trailing data")
	}
}

func TestParseBundle_UnknownTopLevelField(t *testing.T) {
	data := `{"format":"goal-portable-config","version":1,"unknown_field":true}`
	_, err := ParseBundle([]byte(data))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if _, ok := err.(*ErrMalformedBundle); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestParseBundle_UnknownRuntimeField(t *testing.T) {
	data := `{"format":"goal-portable-config","version":1,"runtimes":[{"id":"rt1","name":"RT","executable":"/bin/s","bad_field":1}]}`
	_, err := ParseBundle([]byte(data))
	if err == nil {
		t.Fatal("expected error for unknown runtime field")
	}
}

func TestParseBundle_WrongFormat(t *testing.T) {
	data := `{"format":"wrong-format","version":1}`
	_, err := ParseBundle([]byte(data))
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ErrWrongFormat); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestParseBundle_UnsupportedVersion(t *testing.T) {
	data := `{"format":"goal-portable-config","version":99}`
	_, err := ParseBundle([]byte(data))
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ErrUnsupportedVersion); !ok {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestValidateBundle_DuplicateRuntimeID(t *testing.T) {
	b := &Bundle{
		Format:  FormatIdentity,
		Version: 1,
		Runtimes: []PortableRuntime{
			{ID: "rt1", Name: "A", Executable: "/bin/a"},
			{ID: "rt1", Name: "B", Executable: "/bin/b"},
		},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected duplicate ID error")
	}
}

func TestValidateBundle_DuplicateModelID(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/a"}},
		Models: []PortableModel{
			{ID: "m1", Name: "M1", RuntimeID: "rt1"},
			{ID: "m1", Name: "M2", RuntimeID: "rt1"},
		},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected duplicate model ID error")
	}
}

func TestValidateBundle_DuplicatePipelineID(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
		Pipelines: []PortablePipeline{
			{ID: "p1", Name: "P1", Models: []PortableEntry{{ID: "e1", ModelID: "m1"}}},
			{ID: "p1", Name: "P2", Models: []PortableEntry{{ID: "e2", ModelID: "m1"}}},
		},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected duplicate pipeline ID error")
	}
}

func TestValidateBundle_DuplicateEntryID(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/a"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1"}},
		Pipelines: []PortablePipeline{
			{ID: "p1", Name: "P", Models: []PortableEntry{
				{ID: "e1", ModelID: "m1"},
				{ID: "e1", ModelID: "m1"},
			}},
		},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected duplicate entry ID error")
	}
}

func TestValidateBundle_MissingRuntimeRef(t *testing.T) {
	b := &Bundle{
		Format:  FormatIdentity,
		Version: 1,
		Models:  []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt-missing"}},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected missing runtime ref error")
	}
}

func TestValidateBundle_MissingModelRef(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/a"}},
		Pipelines: []PortablePipeline{
			{ID: "p1", Name: "P", Models: []PortableEntry{{ID: "e1", ModelID: "m-missing"}}},
		},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected missing model ref error")
	}
}

func TestValidateBundle_MalformedVariable(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "${1BAD}"}},
	}
	err := validateBundle(b)
	if err == nil {
		t.Fatal("expected malformed variable error")
	}
	if !strings.Contains(err.Error(), "invalid variable reference") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateBundle_UndefinedVariableAccepted(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "${TOTALLY_UNDEFINED_VAR}"}},
	}
	if err := validateBundle(b); err != nil {
		t.Fatalf("undefined variable should be accepted: %v", err)
	}
}

func TestValidateBundle_EscapedVariableAccepted(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "$${LITERAL}"}},
	}
	if err := validateBundle(b); err != nil {
		t.Fatalf("escaped variable should be accepted: %v", err)
	}
}

func TestValidateBundle_RuntimeNameUniqueness(t *testing.T) {
	b := &Bundle{
		Format:  FormatIdentity,
		Version: 1,
		Runtimes: []PortableRuntime{
			{ID: "rt1", Name: "Llama.cpp", Executable: "/bin/a"},
			{ID: "rt2", Name: "llama.cpp", Executable: "/bin/b"},
		},
	}
	if err := validateBundle(b); err == nil {
		t.Fatal("expected case-insensitive name collision error")
	}
}

func TestValidateBundle_Valid(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "${GOAL_DATA}/bin/server"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "${GOAL_DATA}/model.gguf"}}},
		Pipelines: []PortablePipeline{
			{ID: "p1", Name: "P", Active: true, Models: []PortableEntry{
				{ID: "e1", ModelID: "m1", AutoStart: true},
				{ID: "e2", ModelID: "m1"},
			}},
		},
	}
	if err := validateBundle(b); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
}

func TestMarshalBundle_Deterministic(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/s", EnvironmentKeys: []string{"ZED", "ALPHA"}}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--api-key=secret"}}},
	}
	b1, _ := MarshalBundle(b)
	b2, _ := MarshalBundle(b)
	if string(b1) != string(b2) {
		t.Fatal("marshal not deterministic")
	}
}

func TestMarshalBundle_EnvironmentKeysSorted(t *testing.T) {
	env := map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"}
	keys := sortedKeys(env)
	if len(keys) != 3 || keys[0] != "ALPHA" || keys[1] != "MID" || keys[2] != "ZED" {
		t.Fatalf("keys not sorted: %v", keys)
	}
}

func TestRoundTrip_NoEnvironment(t *testing.T) {
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/s"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--port", "8080"}, Active: true}},
	}
	data1, _ := MarshalBundle(b)

	parsed, err := ParseBundle(data1)
	if err != nil {
		t.Fatal(err)
	}
	data2, _ := MarshalBundle(parsed)
	if string(data1) != string(data2) {
		t.Fatalf("round-trip not byte-identical:\n%s\nvs\n%s", data1, data2)
	}
}

func TestArgsPreservedExactly(t *testing.T) {
	secret := "--api-key=sk-super-secret-12345"
	b := &Bundle{
		Format:   FormatIdentity,
		Version:  1,
		Runtimes: []PortableRuntime{{ID: "rt1", Name: "A", Executable: "/bin/s"}},
		Models:   []PortableModel{{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{secret, "--model", "/path/to.gguf"}}},
	}
	data, _ := MarshalBundle(b)
	if !strings.Contains(string(data), secret) {
		t.Fatal("args token not preserved in bundle")
	}
	parsed, _ := ParseBundle(data)
	if parsed.Models[0].Args[0] != secret {
		t.Fatalf("args not round-tripped: %q", parsed.Models[0].Args[0])
	}
}
