package domain

import (
	"path/filepath"
	"testing"
)

// textConcat simulates textual variable substitution: the variable value is
// inserted as-is, followed by the literal suffix (with its original separators).
func textConcat(dataDir, suffix string) string {
	return dataDir + suffix
}

func TestLaunchResolver_Resolve_VariableInExecutable(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "model.gguf"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "${GOAL_DATA}/bin/llama-server", WorkingDirectory: "${GOAL_DATA}/bin"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// On Windows, the resolved exe is absolute (starts with drive letter),
	// so ResolveExecutablePath returns it as-is.
	want := textConcat(dataDir, "/bin/llama-server")
	if spec.Executable != want {
		t.Fatalf("executable = %q, want %q", spec.Executable, want)
	}
}

func TestLaunchResolver_Resolve_VariableInWorkingDir(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1"}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe", WorkingDirectory: "${GOAL_DATA}/bin"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := textConcat(dataDir, "/bin")
	if spec.WorkingDirectory != want {
		t.Fatalf("wd = %q, want %q", spec.WorkingDirectory, want)
	}
}

func TestLaunchResolver_Resolve_VariableInArgs(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "${GOAL_DATA}/models/x.gguf", "--port", "8080"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := textConcat(dataDir, "/models/x.gguf")
	if spec.Args[1] != want {
		t.Fatalf("args[1] = %q, want %q", spec.Args[1], want)
	}
	if spec.Args[3] != "8080" {
		t.Fatalf("args[3] = %q", spec.Args[3])
	}
}

func TestLaunchResolver_Resolve_VariableInRuntimeEnv(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1"}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe", Environment: map[string]string{"CUDA_HOME": "${GOAL_DATA}/cuda"}}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "CUDA_HOME=" + textConcat(dataDir, "/cuda")
	found := false
	for _, ev := range spec.Environment {
		if ev == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("CUDA_HOME not resolved; want %q", want)
	}
}

func TestLaunchResolver_Resolve_VariableInModelEnv(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Environment: map[string]string{"HF_HOME": "${GOAL_DATA}/hf"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "HF_HOME=" + textConcat(dataDir, "/hf")
	found := false
	for _, ev := range spec.Environment {
		if ev == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("HF_HOME not resolved; want %q", want)
	}
}

func TestLaunchResolver_Resolve_UndefinedVar(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "${DEFINITELY_MISSING_VAR_12345}"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	_, err := r.Resolve(model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	uve, ok := err.(*UndefinedVariableError)
	if !ok {
		t.Fatalf("expected UndefinedVariableError, got %T: %v", err, err)
	}
	if uve.Name != "DEFINITELY_MISSING_VAR_12345" {
		t.Fatalf("name = %q", uve.Name)
	}
	if uve.Field != "model.args[1]" {
		t.Fatalf("field = %q", uve.Field)
	}
}

func TestLaunchResolver_Resolve_MalformedVar(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1"}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "${1BAD}"}

	_, err := r.Resolve(model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	ivre, ok := err.(*InvalidVariableReferenceError)
	if !ok {
		t.Fatalf("expected InvalidVariableReferenceError, got %T: %v", err, err)
	}
	if ivre.Field != "runtime.executable" {
		t.Fatalf("field = %q", ivre.Field)
	}
}

func TestLaunchResolver_Resolve_NoVars_BackwardCompat(t *testing.T) {
	dataDir := t.TempDir()
	_ = dataDir
	r := NewLaunchResolver()

	exe := filepath.Join("usr", "bin", "server")
	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "path.gguf", "--port", "8080"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: exe, WorkingDirectory: "workdir"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("workdir", exe)
	if spec.Executable != want {
		t.Fatalf("exe = %q, want %q", spec.Executable, want)
	}
	if spec.Args[1] != "path.gguf" {
		t.Fatalf("args[1] = %q", spec.Args[1])
	}
	if spec.WorkingDirectory != "workdir" {
		t.Fatalf("wd = %q", spec.WorkingDirectory)
	}
}

func TestLaunchResolver_Resolve_ProcessEnv(t *testing.T) {
	t.Setenv("GOAL_TEST_VAR", "process-value")
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--val", "${GOAL_TEST_VAR}"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Args[1] != "process-value" {
		t.Fatalf("args[1] = %q", spec.Args[1])
	}
}

func TestLaunchResolver_Preview_WithVariables(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "${GOAL_DATA}/x.gguf"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "${GOAL_DATA}/bin/server"}

	spec, err := r.Preview(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantExe := textConcat(dataDir, "/bin/server")
	if spec.Executable != wantExe {
		t.Fatalf("exe = %q, want %q", spec.Executable, wantExe)
	}
	wantArg := textConcat(dataDir, "/x.gguf")
	if spec.Args[1] != wantArg {
		t.Fatalf("args[1] = %q, want %q", spec.Args[1], wantArg)
	}
}

func TestLaunchResolver_Preview_UndefinedVar(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"${NO_SUCH_VAR_XYZ}"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	_, err := r.Preview(model, rt, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	uve, ok := err.(*UndefinedVariableError)
	if !ok {
		t.Fatalf("expected UndefinedVariableError, got %T", err)
	}
	if uve.Field != "model.args[0]" {
		t.Fatalf("field = %q", uve.Field)
	}
}

func TestLaunchResolver_Resolve_EscapeInArgs(t *testing.T) {
	dataDir := t.TempDir()
	_ = dataDir
	r := NewLaunchResolver()
	r.SetDataDir(t.TempDir())

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"--note", `$${GOAL_DATA}/literal`}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Args[1] != "${GOAL_DATA}/literal" {
		t.Fatalf("args[1] = %q, want ${GOAL_DATA}/literal", spec.Args[1])
	}
}

func TestLaunchResolver_Resolve_EnvKeysUntouched(t *testing.T) {
	t.Setenv("GOAL_TEST_KEYVAR", "expanded")
	r := NewLaunchResolver()
	r.SetDataDir(t.TempDir())

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Environment: map[string]string{"${GOAL_TEST_KEYVAR}": "plain-value"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range spec.Environment {
		if ev == "${GOAL_TEST_KEYVAR}=plain-value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("key was expanded; env = %v", spec.Environment)
	}
}

func TestLaunchResolver_Resolve_RelativePathAfterVariable(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1"}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe", WorkingDirectory: "${GOAL_DATA}/bin"}

	spec, err := r.Resolve(model, rt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// On Windows, the resolved WD is absolute (C:\...), so relative exe is
	// joined: filepath.Join(absWD, "server.exe")
	want := filepath.Join(textConcat(dataDir, "/bin"), "server.exe")
	if spec.Executable != want {
		t.Fatalf("exe = %q, want %q", spec.Executable, want)
	}
}

func TestLaunchResolver_Resolve_CustomArgsWithVars(t *testing.T) {
	dataDir := t.TempDir()
	r := NewLaunchResolver()
	r.SetDataDir(dataDir)

	model := &Model{ID: "m1", Name: "M", RuntimeID: "rt1", Args: []string{"-m", "base.gguf"}}
	rt := &Runtime{ID: "rt1", Name: "R", Executable: "server.exe"}

	spec, err := r.Resolve(model, rt, []string{"--override", "${GOAL_DATA}/custom.gguf"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := textConcat(dataDir, "/custom.gguf")
	if spec.Args[3] != want {
		t.Fatalf("args[3] = %q, want %q", spec.Args[3], want)
	}
}
