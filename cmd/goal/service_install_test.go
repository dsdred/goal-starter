package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/platform"
)

// recordingServiceManager records SCM mutations so a refusal test can prove
// the registration was never attempted (ADR 011 D3: refusal = zero writes,
// no SCM registration).
type recordingServiceManager struct {
	platform.ServiceManager
	installs int
	other    int
}

func (r *recordingServiceManager) Install(platform.InstallRequest) error {
	r.installs++
	return nil
}

func (r *recordingServiceManager) Uninstall(string) error { r.other++; return nil }
func (r *recordingServiceManager) Start(string) error     { r.other++; return nil }
func (r *recordingServiceManager) Stop(string) error      { r.other++; return nil }
func (r *recordingServiceManager) Restart(string) error   { r.other++; return nil }

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "goal.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func absExe(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "goal.exe")
	if err := os.WriteFile(p, []byte("stub"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServiceInstallPreflightAllAbsolute(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfig(t, dir, `{
		"version": 2,
		"listenAddress": "127.0.0.1",
		"webPort": 8088,
		"dataDir": `+quoteJSON(dir)+`,
		"runtimes": [
			{"id": "r1", "name": "rt", "executable": `+quoteJSON(filepath.Join(dir, "rt.exe"))+`}
		]
	}`)
	// ValidateFull requires the seeded runtime executable to exist.
	if err := os.WriteFile(filepath.Join(dir, "rt.exe"), []byte("stub"), 0700); err != nil {
		t.Fatal(err)
	}
	exe := absExe(t, dir)
	absCfg, problems := serviceInstallPreflight(exe, cfg)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	want, _ := filepath.Abs(cfg)
	if absCfg != want {
		t.Fatalf("absCfg = %q, want %q", absCfg, want)
	}
}

func TestServiceInstallPreflightRefusalMatrix(t *testing.T) {
	cases := []struct {
		name    string
		makeCfg func(dir string) string
		repo    string
		needle  string
	}{
		{
			name: "config failing validation (missing runtime executable)",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(dir) + `,"runtimes":[{"id":"r1","name":"rt","executable":"C:\\definitely-missing\\rt.exe"}]}`
			},
			needle: "config validation",
		},
		{
			name:    "relative dataDir (empty field -> ./data)",
			makeCfg: func(dir string) string { return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321}` },
			needle:  "dataDir",
		},
		{
			name: "relative dataDir (explicit)",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":"data"}`
			},
			needle: "dataDir",
		},
		{
			name: "absolute dataDir that does not exist (ADR 011 D3.2: missing dataDir is refused)",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(filepath.Join(dir, "no-such-data")) + `}`
			},
			needle: "does not exist",
		},
		{
			name: "relative model path in config (ADR 011 D3.2)",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(dir) + `,"models":[{"id":"m1","name":"m","path":"models\\m.gguf"}]}`
			},
			needle: "is relative",
		},
		{
			name: "relative runtime executable in config",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(dir) + `,"runtimes":[{"id":"r1","name":"rt","executable":"rt.exe"}]}`
			},
			needle: "executable",
		},
		{
			name: "relative runtime workingDirectory in config",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(dir) + `,"runtimes":[{"id":"r1","name":"rt","executable":` + quoteJSON(filepath.Join(dir, "rt.exe")) + `,"workingDirectory":"work"}]}`
			},
			needle: "workingDirectory",
		},
		{
			name: "relative runtime executable in repository",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(dir) + `}`
			},
			repo:   `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"models/rt.exe"}]}`,
			needle: "repository runtime",
		},
		{
			name: "relative workingDirectory in repository",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(dir) + `}`
			},
			repo:   `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"C:\\rt\\rt.exe","working_directory":"work"}]}`,
			needle: "workingDirectory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := absExe(t, dir)
			cfg := writeConfig(t, dir, tc.makeCfg(dir))
			if tc.repo != "" {
				repoPath := filepath.Join(dir, "goal_repo.json")
				if err := os.WriteFile(repoPath, []byte(tc.repo), 0600); err != nil {
					t.Fatal(err)
				}
			}
			absCfg, problems := serviceInstallPreflight(exe, cfg)
			if len(problems) == 0 {
				t.Fatalf("expected refusal, got none (absCfg=%q)", absCfg)
			}
			joined := ""
			for _, p := range problems {
				joined += p + " | "
			}
			if !strings.Contains(joined, tc.needle) {
				t.Fatalf("problems %q: missing %q", joined, tc.needle)
			}
		})
	}
}

func TestServiceInstallPreflightMissingFiles(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing config", func(t *testing.T) {
		exe := absExe(t, dir)
		_, problems := serviceInstallPreflight(exe, filepath.Join(dir, "nope.json"))
		if len(problems) == 0 || !strings.Contains(problems[0], "config load") {
			t.Fatalf("expected config load problem, got %v", problems)
		}
	})

	t.Run("malformed config", func(t *testing.T) {
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{not json`)
		_, problems := serviceInstallPreflight(exe, cfg)
		if len(problems) == 0 || !strings.Contains(problems[0], "config load") {
			t.Fatalf("expected config load problem, got %v", problems)
		}
	})

	t.Run("missing executable", func(t *testing.T) {
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		_, problems := serviceInstallPreflight(filepath.Join(dir, "missing.exe"), cfg)
		if len(problems) == 0 || !strings.Contains(problems[0], "executable not found") {
			t.Fatalf("expected executable problem, got %v", problems)
		}
	})
}

func TestServiceInstallPreflightWritesNothing(t *testing.T) {
	dir := t.TempDir()
	exe := absExe(t, dir)
	cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321}`)
	_, problems := serviceInstallPreflight(exe, cfg)
	if len(problems) == 0 {
		t.Fatal("expected refusal")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Only the two files we created: no data dir, no repo, no default config.
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("preflight created files: %v", names)
	}
}

func TestServiceInstallPreflightRelativeConfigArgResolvedAtInstallTime(t *testing.T) {
	dir := t.TempDir()
	exe := absExe(t, dir)
	writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
	oldwd, _ := os.Getwd()
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	absCfg, problems := serviceInstallPreflight(exe, "goal.json")
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if !filepath.IsAbs(absCfg) {
		t.Fatalf("absCfg not absolute: %q", absCfg)
	}
	want := filepath.Join(dir, "goal.json")
	if absCfg != want {
		t.Fatalf("absCfg = %q, want %q", absCfg, want)
	}
}

// TestServiceInstallRefusalNeverRegisters reproduces the real-SCM acceptance
// defect: an absolute-but-nonexistent dataDir (and a relative dataDir) must be
// refused by pre-flight with a bounded diagnostic that names the dataDir, and
// the SCM registration callback must NEVER be invoked (no registration, zero
// writes) — ADR 011 D3.2/D3.3, acceptance item 2.
func TestServiceInstallRefusalNeverRegisters(t *testing.T) {
	cases := []struct {
		name    string
		makeCfg func(dir string) string
		needle  string
	}{
		{
			name: "absolute dataDir that does not exist",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":` + quoteJSON(filepath.Join(dir, "no-such-data")) + `}`
			},
			needle: "dataDir",
		},
		{
			name: "relative dataDir",
			makeCfg: func(dir string) string {
				return `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":"data"}`
			},
			needle: "dataDir",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := absExe(t, dir)
			cfg := writeConfig(t, dir, tc.makeCfg(dir))
			rec := &recordingServiceManager{}
			if code := serviceInstall(rec, "GoAl", "auto", cfg); code != 1 {
				t.Fatalf("serviceInstall exit = %d, want 1", code)
			}
			if rec.installs != 0 {
				t.Fatalf("SCM Install invoked %d times on refusal; must never be invoked", rec.installs)
			}
			if rec.other != 0 {
				t.Fatalf("other SCM verbs invoked %d times on refusal; must never be invoked", rec.other)
			}
			// The refusal diagnostic must name the offending dataDir.
			_, problems := serviceInstallPreflight(exe, cfg)
			joined := strings.Join(problems, " | ")
			if !strings.Contains(joined, tc.needle) {
				t.Fatalf("refusal %q does not identify the dataDir", joined)
			}
		})
	}
}

func TestServiceImageString(t *testing.T) {
	cases := []struct {
		exe, cfg, want string
	}{
		{
			exe:  `C:\Program Files\GoAl\goal.exe`,
			cfg:  `C:\Program Files\GoAl\goal.json`,
			want: `"C:\Program Files\GoAl\goal.exe" --service run --config "C:\Program Files\GoAl\goal.json"`,
		},
		{
			exe:  `C:\Program Files\GoAl\goal.exe`,
			cfg:  `C:\g\goal.json`,
			want: `"C:\Program Files\GoAl\goal.exe" --service run --config C:\g\goal.json`,
		},
		{
			exe:  `C:\g\goal.exe`,
			cfg:  `C:\g\goal.json`,
			want: `C:\g\goal.exe --service run --config C:\g\goal.json`,
		},
	}
	for _, tc := range cases {
		if got := serviceImageString(tc.exe, tc.cfg); got != tc.want {
			t.Errorf("serviceImageString(%q, %q) = %q, want %q", tc.exe, tc.cfg, got, tc.want)
		}
	}
}

func TestQuoteSCM(t *testing.T) {
	if got := quoteSCM(`C:\a\b.exe`); got != `C:\a\b.exe` {
		t.Errorf("no-space path must stay unquoted: %q", got)
	}
	if got := quoteSCM(`C:\a b\goal.exe`); got != `"C:\a b\goal.exe"` {
		t.Errorf("space path must be quoted: %q", got)
	}
	if got := quoteSCM(`C:\a"b\goal.exe`); got != `"C:\a""b\goal.exe"` {
		t.Errorf("embedded quote must be doubled: %q", got)
	}
}

// absMissingExe returns a platform-absolute path that does not exist.
func absMissingExe(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\definitely-missing\rt.exe`
	}
	return "/definitely-missing/rt.exe"
}

// writeRepo writes a minimal repository file into the dataDir.
func writeRepo(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "goal_repo.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestServiceInstallPreflightRelativeExecutableAddendum covers the ADR 011
// D3.2 addendum (owner decision 11): a relative runtime executable passes
// pre-flight only when WorkingDirectory is textually absolute and the
// effective executable produced by the launch join semantics
// (domain.ResolveExecutablePath) is absolute and exists.
func TestServiceInstallPreflightRelativeExecutableAddendum(t *testing.T) {
	// A. The real-user shape (temp equivalent of llama-server.exe +
	// C:\tools\llamacpp\b10519): relative exe + absolute wd + existing exe.
	t.Run("A: relative exe + absolute wd + existing exe accepted", func(t *testing.T) {
		dir := t.TempDir()
		wd := dir
		if err := os.WriteFile(filepath.Join(wd, "llama-server.exe"), []byte("stub"), 0700); err != nil {
			t.Fatal(err)
		}
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"llama-server.exe","working_directory":`+quoteJSON(wd)+`}]}`)
		if _, problems := serviceInstallPreflight(exe, cfg); len(problems) != 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
	})

	// B. relative exe + absolute wd + missing exe -> refuse, naming the
	// effective joined path.
	t.Run("B: relative exe + absolute wd + missing exe refused", func(t *testing.T) {
		dir := t.TempDir()
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"missing-server.exe","working_directory":`+quoteJSON(dir)+`}]}`)
		_, problems := serviceInstallPreflight(exe, cfg)
		if len(problems) == 0 {
			t.Fatal("expected refusal")
		}
		joined := ""
		for _, p := range problems {
			joined += p + " | "
		}
		if !strings.Contains(joined, "does not exist") {
			t.Fatalf("problems %q: missing 'does not exist'", joined)
		}
		if !strings.Contains(joined, fmt.Sprintf("%q", filepath.Join(dir, "missing-server.exe"))) {
			t.Fatalf("problems %q: effective path not named", joined)
		}
	})

	// C. relative exe + empty wd -> refuse.
	t.Run("C: relative exe + empty wd refused", func(t *testing.T) {
		dir := t.TempDir()
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"llama-server.exe"}]}`)
		_, problems := serviceInstallPreflight(exe, cfg)
		joined := strings.Join(problems, " | ")
		if len(problems) == 0 || !strings.Contains(joined, "is relative") {
			t.Fatalf("expected relative refusal, got %q", joined)
		}
	})

	// D. relative exe + relative wd -> refuse.
	t.Run("D: relative exe + relative wd refused", func(t *testing.T) {
		dir := t.TempDir()
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"llama-server.exe","working_directory":"work"}]}`)
		_, problems := serviceInstallPreflight(exe, cfg)
		joined := strings.Join(problems, " | ")
		if len(problems) == 0 || !strings.Contains(joined, "is relative") {
			t.Fatalf("expected relative refusal, got %q", joined)
		}
	})

	// E. absolute exe (even nonexistent) + absolute wd -> existing behavior
	// unchanged: pre-flight does not stat absolute executables.
	t.Run("E: absolute exe unchanged (no existence check)", func(t *testing.T) {
		dir := t.TempDir()
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":`+quoteJSON(absMissingExe(t))+`,"working_directory":`+quoteJSON(dir)+`}]}`)
		if _, problems := serviceInstallPreflight(exe, cfg); len(problems) != 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
	})

	// F. relative subpath + absolute wd -> joined correctly, accepted.
	t.Run("F: relative subpath + absolute wd accepted", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "llama-server.exe"), []byte("stub"), 0700); err != nil {
			t.Fatal(err)
		}
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":`+quoteJSON(filepath.Join("sub", "llama-server.exe"))+`,"working_directory":`+quoteJSON(dir)+`}]}`)
		if _, problems := serviceInstallPreflight(exe, cfg); len(problems) != 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
	})

	// G. paths containing spaces.
	t.Run("G: spaces in working directory accepted", func(t *testing.T) {
		dir := t.TempDir()
		wd := filepath.Join(dir, "my tools")
		if err := os.MkdirAll(wd, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "llama-server.exe"), []byte("stub"), 0700); err != nil {
			t.Fatal(err)
		}
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"llama-server.exe","working_directory":`+quoteJSON(wd)+`}]}`)
		if _, problems := serviceInstallPreflight(exe, cfg); len(problems) != 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
	})

	// H. ".." normalization: the joined effective path is cleaned.
	t.Run("H: dot-dot subpath normalized and accepted", func(t *testing.T) {
		dir := t.TempDir()
		a := filepath.Join(dir, "a")
		if err := os.MkdirAll(filepath.Join(dir, "b"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(a, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(a, "llama-server.exe"), []byte("stub"), 0700); err != nil {
			t.Fatal(err)
		}
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
		writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":`+quoteJSON(filepath.Join("b", "..", "a", "llama-server.exe"))+`,"working_directory":`+quoteJSON(dir)+`}]}`)
		if _, problems := serviceInstallPreflight(exe, cfg); len(problems) != 0 {
			t.Fatalf("unexpected problems: %v", problems)
		}
	})

	// Config-seeded relative executable remains refused: ValidateFull stats
	// it against the installer CWD (and the service would fail the same check
	// under the SCM working directory), so the addendum relaxes only the
	// repository path — the real-user shape.
	t.Run("config-seeded relative exe still refused (unchanged)", func(t *testing.T) {
		dir := t.TempDir()
		wd := dir
		if err := os.WriteFile(filepath.Join(wd, "llama-server.exe"), []byte("stub"), 0700); err != nil {
			t.Fatal(err)
		}
		exe := absExe(t, dir)
		cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`,"runtimes":[{"id":"r1","name":"rt","executable":"llama-server.exe","workingDirectory":`+quoteJSON(wd)+`}]}`)
		_, problems := serviceInstallPreflight(exe, cfg)
		joined := strings.Join(problems, " | ")
		if len(problems) == 0 || !strings.Contains(joined, "config validation") {
			t.Fatalf("expected config validation refusal, got %q", joined)
		}
	})
}

// TestServiceInstallPreflightRelativeExecutableRefusalNeverRegisters proves
// the addendum refusal (missing effective executable) keeps the D3.3
// side-effect guarantee: no SCM registration, zero files.
func TestServiceInstallPreflightRelativeExecutableRefusalNeverRegisters(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfig(t, dir, `{"version":2,"listenAddress":"127.0.0.1","webPort":18321,"dataDir":`+quoteJSON(dir)+`}`)
	writeRepo(t, dir, `{"schema_version":8,"runtimes":[{"id":"r9","name":"rt","executable":"missing-server.exe","working_directory":`+quoteJSON(dir)+`}]}`)
	rec := &recordingServiceManager{}
	if code := serviceInstall(rec, "GoAl", "auto", cfg); code != 1 {
		t.Fatalf("serviceInstall exit = %d, want 1", code)
	}
	if rec.installs != 0 {
		t.Fatalf("SCM Install invoked %d times on refusal; must never be invoked", rec.installs)
	}
	if rec.other != 0 {
		t.Fatalf("other SCM verbs invoked %d times on refusal; must never be invoked", rec.other)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// goal.json + goal_repo.json only — nothing else created.
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("preflight created files: %v", names)
	}
}

func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
