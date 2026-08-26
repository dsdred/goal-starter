package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBcryptHash returns a structurally valid (60-char) bcrypt hash for
// classification tests; it is never verified against a password.
func fakeBcryptHash() string {
	return "$2a$12$" + strings.Repeat("x", 53)
}

func TestLogLevel_Parse(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"", slog.LevelInfo, true},
		{"info", slog.LevelInfo, true},
		{"INFO", slog.LevelInfo, true},
		{"  info ", slog.LevelInfo, true},
		{"debug", slog.LevelDebug, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"trace", 0, false},
		{"bogus", 0, false},
		{"infol", 0, false},
	}
	for _, tc := range cases {
		got, err := LogLevel(tc.in)
		if tc.ok {
			if err != nil {
				t.Errorf("LogLevel(%q) unexpected error: %v", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("LogLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		} else if err == nil {
			t.Errorf("LogLevel(%q) expected error, got level %v", tc.in, got)
		}
	}
}

func TestValidate_LogLevel(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
	cfg.LogLevel = "debug"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid logLevel rejected: %v", err)
	}
	cfg.LogLevel = "trace"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid logLevel accepted")
	}
}

func TestDiffHot_Identical(t *testing.T) {
	cfg := Default()
	d := DiffHot(cfg, cfg)
	if d.Applied == nil || len(d.Applied) != 0 {
		t.Errorf("Applied = %v, want non-nil empty", d.Applied)
	}
	if d.RestartRequired == nil || len(d.RestartRequired) != 0 {
		t.Errorf("RestartRequired = %v, want non-nil empty", d.RestartRequired)
	}
}

func TestDiffHot_EachRestartField(t *testing.T) {
	fields := map[string]func(file, live *Config){
		"listenAddress": func(f, l *Config) { f.ListenAddress = "0.0.0.0" },
		"webPort":       func(f, l *Config) { f.WebPort = 9999 },
		"dataDir":       func(f, l *Config) { f.DataDir = "/other/data" },
		"authEnabled":   func(f, l *Config) { f.AuthEnabled = true; f.AdminPasswordHash = fakeBcryptHash() },
		"adminUser":     func(f, l *Config) { f.AdminUser = "root" },
	}
	for name, mutate := range fields {
		live := Default()
		file := Default()
		mutate(&file, &live)
		d := DiffHot(file, live)
		if len(d.RestartRequired) != 1 || d.RestartRequired[0] != name {
			t.Errorf("%s: RestartRequired = %v, want [%s]", name, d.RestartRequired, name)
		}
		if len(d.Applied) != 0 {
			t.Errorf("%s: Applied = %v, want empty", name, d.Applied)
		}
	}
}

func TestDiffHot_LogLevel(t *testing.T) {
	live := Default()
	file := Default()
	file.LogLevel = "debug"
	d := DiffHot(file, live)
	if len(d.Applied) != 1 || d.Applied[0] != "logLevel" {
		t.Errorf("Applied = %v, want [logLevel]", d.Applied)
	}
	if len(d.RestartRequired) != 0 {
		t.Errorf("RestartRequired = %v, want empty", d.RestartRequired)
	}
}

func TestDiffHot_Multiple(t *testing.T) {
	live := Default()
	file := Default()
	file.LogLevel = "debug"
	file.WebPort = 9999
	file.AdminUser = "root"
	d := DiffHot(file, live)
	if len(d.Applied) != 1 || d.Applied[0] != "logLevel" {
		t.Errorf("Applied = %v, want [logLevel]", d.Applied)
	}
	if len(d.RestartRequired) != 2 || d.RestartRequired[0] != "webPort" || d.RestartRequired[1] != "adminUser" {
		t.Errorf("RestartRequired = %v, want [webPort adminUser] (struct order)", d.RestartRequired)
	}
}

func TestDiffHot_IgnoresCredentialsAndSeedSections(t *testing.T) {
	live := Default()
	file := Default()
	file.AdminPasswordHash = fakeBcryptHash()
	file.Runtimes = []Runtime{{ID: "r1", Name: "r1", Executable: "/bin/x"}}
	file.Models = []Model{{ID: "m1", Name: "m1"}}
	file.Profiles = []Profile{{ID: "p1", Name: "p1"}}
	d := DiffHot(file, live)
	if len(d.Applied) != 0 || len(d.RestartRequired) != 0 {
		t.Errorf("credential and seed sections must not be classified: got %+v", d)
	}
}

func TestLoadReadOnly_Valid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.json")
	cfg := Default()
	cfg.LogLevel = "debug"
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadReadOnly(path)
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if got.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", got.LogLevel)
	}
}

func TestLoadReadOnly_MissingFileDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.json")
	if _, err := LoadReadOnly(path); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("LoadReadOnly created the default file (ADR 009 D3: reload never writes)")
	}
}

func TestLoadReadOnly_BrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadReadOnly(path); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestLoadReadOnly_InvalidLogLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"listenAddress":"127.0.0.1","webPort":8088,"logLevel":"trace"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadReadOnly(path); err == nil {
		t.Fatal("expected validation error for unknown logLevel")
	}
}
