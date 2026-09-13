package domain

import (
	"strings"
	"testing"
)

type testSource struct {
	vars map[string]string
}

func (ts *testSource) Lookup(name string) (string, bool) {
	v, ok := ts.vars[name]
	return v, ok
}

func TestResolveString_NoDollar(t *testing.T) {
	src := &testSource{vars: map[string]string{"A": "1"}}
	got, err := ResolveString("hello world", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_SimpleVar(t *testing.T) {
	src := &testSource{vars: map[string]string{"HOME": "/home/user"}}
	got, err := ResolveString("${HOME}/file.txt", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/user/file.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_MultipleVars(t *testing.T) {
	src := &testSource{vars: map[string]string{"A": "x", "B": "y"}}
	got, err := ResolveString("${A}-${B}-${A}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "x-y-x" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_Undefined(t *testing.T) {
	src := &testSource{vars: map[string]string{"A": "1"}}
	_, err := ResolveString("${MISSING}", src)
	if err == nil {
		t.Fatal("expected error")
	}
	uve, ok := err.(*UndefinedVariableError)
	if !ok {
		t.Fatalf("expected UndefinedVariableError, got %T", err)
	}
	if uve.Name != "MISSING" {
		t.Fatalf("got name %q", uve.Name)
	}
}

func TestResolveString_DefinedEmpty(t *testing.T) {
	src := &testSource{vars: map[string]string{"EMPTY": ""}}
	got, err := ResolveString("pre-${EMPTY}-post", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "pre--post" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_Malformed_DigitStart(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveString("${123}", src)
	if err == nil {
		t.Fatal("expected error")
	}
	ivre, ok := err.(*InvalidVariableReferenceError)
	if !ok {
		t.Fatalf("expected InvalidVariableReferenceError, got %T: %v", err, err)
	}
	if !strings.Contains(ivre.Raw, "123") {
		t.Fatalf("raw %q", ivre.Raw)
	}
}

func TestResolveString_Malformed_Empty(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveString("${}", src)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*InvalidVariableReferenceError); !ok {
		t.Fatalf("expected InvalidVariableReferenceError, got %T", err)
	}
}

func TestResolveString_Malformed_Space(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveString("${NA ME}", src)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*InvalidVariableReferenceError); !ok {
		t.Fatalf("expected InvalidVariableReferenceError, got %T", err)
	}
}

func TestResolveString_Malformed_Unterminated(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveString("${NAME", src)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*InvalidVariableReferenceError); !ok {
		t.Fatalf("expected InvalidVariableReferenceError, got %T", err)
	}
}

func TestResolveString_Escape_DoubleDollar(t *testing.T) {
	src := &testSource{vars: map[string]string{"HOME": "/home"}}
	got, err := ResolveString("$$HOME", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "$HOME" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_Escape_LiteralRef(t *testing.T) {
	src := &testSource{vars: map[string]string{"NAME": "value"}}
	got, err := ResolveString("$${NAME}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "${NAME}" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_BareDollar(t *testing.T) {
	src := &testSource{vars: map[string]string{"foo": "bar"}}
	got, err := ResolveString("$foo", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "$foo" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_BuiltinPrecedence(t *testing.T) {
	t.Setenv("GOAL_DATA", "/should-not-be-used")
	src := NewVariableSource("/real/data/dir")
	got, err := ResolveString("${GOAL_DATA}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/real/data/dir" {
		t.Fatalf("got %q, want /real/data/dir", got)
	}
}

func TestResolveString_BuiltinWinsOverEnv(t *testing.T) {
	t.Setenv("GOAL_DATA", "/env-value")
	src := NewVariableSource("/builtin-value")
	got, err := ResolveString("${GOAL_DATA}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/builtin-value" {
		t.Fatalf("got %q, want /builtin-value", got)
	}
}

func TestResolveString_NonBuiltinGoalPrefix(t *testing.T) {
	t.Setenv("GOAL_CUSTOM", "custom-val")
	src := NewVariableSource("/data")
	got, err := ResolveString("${GOAL_CUSTOM}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "custom-val" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveString_NoRecursion(t *testing.T) {
	src := &testSource{vars: map[string]string{"A": "${B}", "B": "resolved"}}
	got, err := ResolveString("${A}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "${B}" {
		t.Fatalf("got %q, want literal ${B} (no recursion)", got)
	}
}

func TestResolveString_NoPartialSubstitution(t *testing.T) {
	src := &testSource{vars: map[string]string{"A": "valA"}}
	_, err := ResolveString("prefix-${A}-${MISSING}-suffix", src)
	if err == nil {
		t.Fatal("expected error for undefined MISSING")
	}
	// The function returns "", not a partial result
}

func TestResolveString_NoPartial_Malformed(t *testing.T) {
	src := &testSource{vars: map[string]string{"A": "valA"}}
	_, err := ResolveString("prefix-${A}-${123}-suffix", src)
	if err == nil {
		t.Fatal("expected error for malformed ${123}")
	}
}

func TestResolveString_Mixed(t *testing.T) {
	src := &testSource{vars: map[string]string{"PATH": "/usr/bin", "X": ""}}
	got, err := ResolveString("$${PATH}:${PATH}$$", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "${PATH}:/usr/bin$" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveStringField_Context(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveStringField("${UNKNOWN}", "model.args[2]", src)
	if err == nil {
		t.Fatal("expected error")
	}
	uve, ok := err.(*UndefinedVariableError)
	if !ok {
		t.Fatalf("expected UndefinedVariableError, got %T", err)
	}
	if uve.Field != "model.args[2]" {
		t.Fatalf("field %q", uve.Field)
	}
	if uve.Name != "UNKNOWN" {
		t.Fatalf("name %q", uve.Name)
	}
}

func TestResolveStringField_InvalidRef(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveStringField("${1BAD}", "runtime.executable", src)
	if err == nil {
		t.Fatal("expected error")
	}
	ivre, ok := err.(*InvalidVariableReferenceError)
	if !ok {
		t.Fatalf("expected InvalidVariableReferenceError, got %T", err)
	}
	if ivre.Field != "runtime.executable" {
		t.Fatalf("field %q", ivre.Field)
	}
}

func TestResolveArgs_Success(t *testing.T) {
	src := &testSource{vars: map[string]string{"M": "/models/x.gguf"}}
	got, err := ResolveArgs([]string{"-m", "${M}", "--port", "8080"}, "model.args", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-m", "/models/x.gguf", "--port", "8080"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveArgs_Error(t *testing.T) {
	src := &testSource{vars: map[string]string{}}
	_, err := ResolveArgs([]string{"-m", "${MISSING}"}, "model.args", src)
	if err == nil {
		t.Fatal("expected error")
	}
	uve, ok := err.(*UndefinedVariableError)
	if !ok {
		t.Fatalf("expected UndefinedVariableError, got %T", err)
	}
	if uve.Field != "model.args[1]" {
		t.Fatalf("field %q", uve.Field)
	}
}

func TestResolveEnvValues_Success(t *testing.T) {
	src := &testSource{vars: map[string]string{"HOME": "/home/u"}}
	got, err := ResolveEnvValues(map[string]string{"CUDA_HOME": "${HOME}/cuda", "PLAIN": "val"}, "runtime.environment", src)
	if err != nil {
		t.Fatal(err)
	}
	if got["CUDA_HOME"] != "/home/u/cuda" {
		t.Fatalf("CUDA_HOME = %q", got["CUDA_HOME"])
	}
	if got["PLAIN"] != "val" {
		t.Fatalf("PLAIN = %q", got["PLAIN"])
	}
}

func TestResolveEnvValues_KeysUntouched(t *testing.T) {
	src := &testSource{vars: map[string]string{"SOME_KEY": "should-not-expand"}}
	env := map[string]string{"${SOME_KEY}": "value"}
	got, err := ResolveEnvValues(env, "model.environment", src)
	if err != nil {
		t.Fatal(err)
	}
	if got["${SOME_KEY}"] != "value" {
		t.Fatalf("key was modified: %q", got["${SOME_KEY}"])
	}
}

func TestIsValidVarName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"A", true},
		{"_A", true},
		{"a1_B", true},
		{"GOAL_DATA", true},
		{"", false},
		{"1A", false},
		{"A B", false},
		{"A-B", false},
		{"A.B", false},
		{"$", false},
	}
	for _, tc := range cases {
		if got := isValidVarName(tc.name); got != tc.want {
			t.Errorf("isValidVarName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestResolveString_UnderscoreStart(t *testing.T) {
	src := &testSource{vars: map[string]string{"_private": "val"}}
	got, err := ResolveString("${_private}", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "val" {
		t.Fatalf("got %q", got)
	}
}
