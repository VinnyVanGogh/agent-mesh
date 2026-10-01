package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig_Paths(t *testing.T) {
	cfg := DefaultConfig()

	home, _ := os.UserHomeDir()
	wantDataDir := filepath.Join(home, ".staypoint")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if cfg.DBPath != filepath.Join(wantDataDir, "staypoint.db") {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if !strings.HasPrefix(cfg.TelemetryDBPath, home) {
		t.Errorf("TelemetryDBPath = %q does not start with home %q", cfg.TelemetryDBPath, home)
	}
}

func TestDefaultConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MachineRole != "hybrid" {
		t.Errorf("MachineRole = %q, want hybrid", cfg.MachineRole)
	}
	if cfg.MaxHandoffsPerRepo != 3 {
		t.Errorf("MaxHandoffsPerRepo = %d, want 3", cfg.MaxHandoffsPerRepo)
	}
	if cfg.PreferredPersonalTool != "auto" {
		t.Errorf("PreferredPersonalTool = %q, want auto", cfg.PreferredPersonalTool)
	}
	if cfg.RemoteHost == "" {
		t.Error("RemoteHost should not be empty")
	}
}

func TestExpandPath(t *testing.T) {
	home := "/home/testuser"
	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", "/home/testuser/foo/bar"},
		{"~", "/home/testuser"},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
	}
	for _, tc := range tests {
		got := expandPath(tc.input, home)
		if got != tc.want {
			t.Errorf("expandPath(%q, %q) = %q, want %q", tc.input, home, got, tc.want)
		}
	}
}

func TestEnsureDataDir_CreatesDirectory(t *testing.T) {
	tmp := t.TempDir()
	newDir := filepath.Join(tmp, "staypoint-test")

	cfg := DefaultConfig()
	cfg.DataDir = newDir

	if err := EnsureDataDir(cfg); err != nil {
		t.Fatalf("EnsureDataDir: %v", err)
	}
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		t.Errorf("directory %q was not created", newDir)
	}
}

func TestEnsureDataDir_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	cfg := DefaultConfig()
	cfg.DataDir = tmp

	// Calling twice must not error.
	if err := EnsureDataDir(cfg); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureDataDir(cfg); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestConfig_JSONRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CompanyName = "Acme"
	cfg.EngineerName = "Alice"
	cfg.HourlyRate = 150.0
	cfg.MachineRole = "work"

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.CompanyName != cfg.CompanyName {
		t.Errorf("CompanyName = %q, want %q", got.CompanyName, cfg.CompanyName)
	}
	if got.HourlyRate != cfg.HourlyRate {
		t.Errorf("HourlyRate = %v, want %v", got.HourlyRate, cfg.HourlyRate)
	}
	if got.MachineRole != cfg.MachineRole {
		t.Errorf("MachineRole = %q, want %q", got.MachineRole, cfg.MachineRole)
	}
}

func TestLoadConfig_ReturnsDefaultWhenNoFile(t *testing.T) {
	// Point HOME to an empty temp dir so no config file can be found.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	// DataDir must be within the temp home.
	if !strings.HasPrefix(cfg.DataDir, tmp) {
		t.Errorf("DataDir %q not under temp home %q", cfg.DataDir, tmp)
	}
}
