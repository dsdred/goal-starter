// Command fakegpu is a test-only stand-in for nvidia-smi (ADR 025 D-10/C-2).
//
// It reads spec.json from the directory holding its own executable, appends one
// line per invocation to runs.txt (so a test can count how many children ran),
// then emits the canned stdout/stderr/exit code or hangs until killed. Behavior
// is fully determined by the sibling spec file, so each test is isolated and no
// process-global environment is mutated.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type spec struct {
	CSV      string `json:"csv"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	SleepMS  int    `json:"sleep_ms"`
	Hang     bool   `json:"hang"`
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(99)
	}
	dir := filepath.Dir(exe)

	if f, openErr := os.OpenFile(filepath.Join(dir, "runs.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); openErr == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
		_ = f.Close()
	}

	data, err := os.ReadFile(filepath.Join(dir, "spec.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakegpu: cannot read spec.json")
		os.Exit(98)
	}
	var s spec
	if err := json.Unmarshal(data, &s); err != nil {
		fmt.Fprintln(os.Stderr, "fakegpu: cannot parse spec.json")
		os.Exit(97)
	}

	if s.SleepMS > 0 {
		time.Sleep(time.Duration(s.SleepMS) * time.Millisecond)
	}
	if s.Hang {
		// Block until the provider's context kills us. A pending timer keeps the
		// Go runtime from treating this as a deadlock; the process is killable.
		for {
			time.Sleep(time.Hour)
		}
	}
	if s.Stderr != "" {
		fmt.Fprint(os.Stderr, s.Stderr)
	}
	if s.CSV != "" {
		fmt.Print(s.CSV)
	}
	os.Exit(s.ExitCode)
}
