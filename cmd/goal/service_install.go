package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dsdred/goal/internal/config"
)

// serviceImageString renders the registered service image for install output
// and diagnostics (ADR 011 D2): "<EXE>" --service run --config "<CONFIG>",
// quoting applied iff the path contains a space (embedded quotes doubled).
func serviceImageString(exePath, configPath string) string {
	return quoteSCM(exePath) + " --service run --config " + quoteSCM(configPath)
}

// quoteSCM wraps a path in double quotes iff it contains a space or a double
// quote (embedded quotes doubled), per SCM binPath command-line parsing
// (ADR 011 D2). It mirrors syscall.EscapeArg for the common cases.
func quoteSCM(p string) string {
	if !strings.ContainsAny(p, ` "`) {
		return p
	}
	q := strings.ReplaceAll(p, `"`, `""`)
	return `"` + q + `"`
}

// repoPathProbe is the minimal side-effect-free decode of the repository file
// for the install pre-flight (ADR 011 D3.2: raw JSON decode only — no
// repository construction, no file creation, no backup).
type repoPathProbe struct {
	Runtimes []struct {
		ID               string `json:"id"`
		Executable       string `json:"executable"`
		WorkingDirectory string `json:"working_directory"`
	} `json:"runtimes"`
}

// serviceInstallPreflight enforces the deterministic service-install contract
// (ADR 011 D3.2/D3.3): it refuses registration (bounded diagnostic naming
// every offending entry; nothing is written) unless the resolved config
// exists and passes LoadReadOnly + ValidateFull, the effective dataDir is
// absolute AND exists as a directory (a missing/relative dataDir is refused —
// D3.2: it would place the repository and the audit file in the SCM working
// directory; install never creates it), and every seeded runtime executable /
// workingDirectory and seeded model path (when set), plus every current-repo
// runtime executable / workingDirectory, is absolute. It returns the absolute
// cleaned config path.
func serviceInstallPreflight(exePath, configPath string) (string, []string) {
	var problems []string

	if _, err := os.Stat(exePath); err != nil {
		problems = append(problems, fmt.Sprintf("executable not found: %s", exePath))
	}

	absCfg, err := filepath.Abs(filepath.Clean(configPath))
	if err != nil {
		return "", []string{fmt.Sprintf("resolve config path: %v", err)}
	}

	cfg, err := config.LoadReadOnly(absCfg)
	if err != nil {
		return absCfg, append(problems, fmt.Sprintf("config load (read-only): %v", err))
	}

	if err := cfg.ValidateFull(); err != nil {
		problems = append(problems, fmt.Sprintf("config validation: %v", err))
	}

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	if !filepath.IsAbs(dataDir) {
		problems = append(problems, fmt.Sprintf("effective dataDir %q is relative (default %q); the service would place the repository and audit file in the SCM working directory; set an absolute dataDir in the config", dataDir, "./data"))
	} else if fi, err := os.Stat(dataDir); err != nil || !fi.IsDir() {
		problems = append(problems, fmt.Sprintf("effective dataDir %q does not exist or is not a directory; create the directory before installing (install never creates it)", dataDir))
	}

	for i, rt := range cfg.Runtimes {
		if rt.Executable != "" && !filepath.IsAbs(rt.Executable) {
			problems = append(problems, fmt.Sprintf("config runtime %q (index %d): executable %q is relative", rt.Name, i, rt.Executable))
		}
		if rt.WorkingDirectory != "" && !filepath.IsAbs(rt.WorkingDirectory) {
			problems = append(problems, fmt.Sprintf("config runtime %q (index %d): workingDirectory %q is relative", rt.Name, i, rt.WorkingDirectory))
		}
	}

	for i, m := range cfg.Models {
		if m.Path != "" && !filepath.IsAbs(m.Path) {
			problems = append(problems, fmt.Sprintf("config model %q (index %d): path %q is relative", m.Name, i, m.Path))
		}
	}

	if dataDir != "" && filepath.IsAbs(dataDir) {
		repoPath := filepath.Join(dataDir, "goal_repo.json")
		if data, err := os.ReadFile(repoPath); err == nil {
			var probe repoPathProbe
			if err := json.Unmarshal(data, &probe); err != nil {
				problems = append(problems, fmt.Sprintf("repository decode: %v", err))
			} else {
				for _, rt := range probe.Runtimes {
					if rt.Executable != "" && !filepath.IsAbs(rt.Executable) {
						problems = append(problems, fmt.Sprintf("repository runtime %q: executable %q is relative", rt.ID, rt.Executable))
					}
					if rt.WorkingDirectory != "" && !filepath.IsAbs(rt.WorkingDirectory) {
						problems = append(problems, fmt.Sprintf("repository runtime %q: workingDirectory %q is relative", rt.ID, rt.WorkingDirectory))
					}
				}
			}
		}
	}

	return absCfg, problems
}
