//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
)

// lookupNvidiaSmi resolves nvidia-smi on Windows (ADR 025 D-2): the canonical
// NVSMI path under %ProgramFiles% first, then exec.LookPath as a fallback. The
// binary is frequently not on PATH, so the canonical location is tried first.
func lookupNvidiaSmi() (string, error) {
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidate := filepath.Join(pf, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return exec.LookPath("nvidia-smi")
}
