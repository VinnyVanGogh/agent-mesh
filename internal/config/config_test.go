package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if cfg.DataDir == "" {
		t.Error("DataDir should not be empty")
	}
	if cfg.DBPath == "" {
		t.Error("DBPath should not be empty")
	}
	if cfg.MachineRole != "hybrid" {
		t.Errorf("MachineRole: got %q, want %q", cfg.MachineRole, "hybrid")
	}
	if cfg.MaxHandoffsPerRepo != 3 {
		t.Errorf("MaxHandoffsPerRepo: got %d, want 3", cfg.MaxHandoffsPerRepo)
	}
	if cfg.PreferredPersonalTool != "auto" {
		t.Errorf("PreferredPersonalTool: got %q, want %q", cfg.PreferredPersonalTool, "auto")
	}
	if cfg.GooglePlanTier != "Google AI Ultra" {
		t.Errorf("GooglePlanTier: got %q, want %q", cfg.GooglePlanTier, "Google AI Ultra")
	}
}

func TestExpandPath(t *testing.T) {
	home := "/home/testuser"
	tests := []struct {
		path string
		want string
	}{
		{"~/Documents/dev", "/home/testuser/Documents/dev"},
		{"~", "/home/testuser"},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
	}
	for _, tt := range tests {
		got := expandPath(tt.path, home)
		if got != tt.want {
			t.Errorf("expandPath(%q, %q) = %q, want %q", tt.path, home, got, tt.want)
		}
	}
}

func TestLoadConfig_NoFile(t *testing.T) {
	// Redirect HOME to a temp dir with no config file.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig with no file: unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig returned nil config")
	}
	// Should fall back to defaults.
	if cfg.MachineRole != "hybrid" {
		t.Errorf("expected default MachineRole, got %q", cfg.MachineRole)
	}
}

func TestLoadConfig_TOML(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".staypoint")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	tomlContent := `
company_name = "Acme Corp"
engineer_name = "Alice"
hourly_rate = 150.0
machine_role = "work"
max_handoffs_per_repo = 5
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig TOML: %v", err)
	}
	if cfg.CompanyName != "Acme Corp" {
		t.Errorf("CompanyName: got %q, want %q", cfg.CompanyName, "Acme Corp")
	}
	if cfg.EngineerName != "Alice" {
		t.Errorf("EngineerName: got %q, want %q", cfg.EngineerName, "Alice")
	}
	if cfg.HourlyRate != 150.0 {
		t.Errorf("HourlyRate: got %v, want 150.0", cfg.HourlyRate)
	}
	if cfg.MachineRole != "work" {
		t.Errorf("MachineRole: got %q, want %q", cfg.MachineRole, "work")
	}
	if cfg.MaxHandoffsPerRepo != 5 {
		t.Errorf("MaxHandoffsPerRepo: got %d, want 5", cfg.MaxHandoffsPerRepo)
	}
}

func TestLoadConfig_JSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".staypoint")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	jsonContent := `{
  "company_name": "Beta LLC",
  "machine_role": "personal",
  "max_handoffs_per_repo": 2
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig JSON: %v", err)
	}
	if cfg.CompanyName != "Beta LLC" {
		t.Errorf("CompanyName: got %q, want %q", cfg.CompanyName, "Beta LLC")
	}
	if cfg.MachineRole != "personal" {
		t.Errorf("MachineRole: got %q, want %q", cfg.MachineRole, "personal")
	}
	if cfg.MaxHandoffsPerRepo != 2 {
		t.Errorf("MaxHandoffsPerRepo: got %d, want 2", cfg.MaxHandoffsPerRepo)
	}
}

func TestLoadConfig_LegacyFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	legacyDir := filepath.Join(tmp, ".agent-mesh")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacyJSON := `{"company_name": "Legacy Co", "machine_role": "hybrid"}`
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(legacyJSON), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig legacy: %v", err)
	}
	if cfg.CompanyName != "Legacy Co" {
		t.Errorf("CompanyName: got %q, want %q", cfg.CompanyName, "Legacy Co")
	}
}

func TestLoadConfig_RemoteRepoRootInference(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".staypoint")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Explicitly clear remote_repo_root so inference logic fires.
	// company_name contains "managed solution" → path should resolve to managed_solution.
	tomlContent := `
company_name = "Managed Solution Inc"
remote_repo_root = ""
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RemoteRepoRoot != "~/Documents/dev/managed_solution" {
		t.Errorf("RemoteRepoRoot: got %q, want %q", cfg.RemoteRepoRoot, "~/Documents/dev/managed_solution")
	}
}

func TestLoadConfig_RemoteRepoRootInference_WorkRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".staypoint")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// work_repo_root containing "mansol" should also trigger managed_solution inference.
	tomlContent := `
work_repo_root = "/home/user/Documents/dev/mansol"
remote_repo_root = ""
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RemoteRepoRoot != "~/Documents/dev/managed_solution" {
		t.Errorf("RemoteRepoRoot: got %q, want %q", cfg.RemoteRepoRoot, "~/Documents/dev/managed_solution")
	}
}

func TestLoadConfig_InvalidTOML(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".staypoint")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not valid toml :::"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".staypoint")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestEnsureDataDir(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Config{
		DataDir: filepath.Join(tmp, "staypoint"),
		DBPath:  filepath.Join(tmp, "staypoint", "staypoint.db"),
	}

	if err := EnsureDataDir(cfg); err != nil {
		t.Fatalf("EnsureDataDir: %v", err)
	}

	if _, err := os.Stat(cfg.DataDir); os.IsNotExist(err) {
		t.Errorf("DataDir %q was not created", cfg.DataDir)
	}
}

func TestEnsureDataDir_MigratesLegacyDB(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Create legacy dir with a mesh.db.
	legacyDir := filepath.Join(tmp, ".agent-mesh")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacyDB := filepath.Join(legacyDir, "mesh.db")
	if err := os.WriteFile(legacyDB, []byte("legacy-data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		DataDir: filepath.Join(tmp, ".staypoint"),
		DBPath:  filepath.Join(tmp, ".staypoint", "staypoint.db"),
	}

	if err := EnsureDataDir(cfg); err != nil {
		t.Fatalf("EnsureDataDir: %v", err)
	}

	newDB := filepath.Join(cfg.DataDir, "staypoint.db")
	data, err := os.ReadFile(newDB)
	if err != nil {
		t.Fatalf("staypoint.db not found after migration: %v", err)
	}
	if string(data) != "legacy-data" {
		t.Errorf("migrated DB content: got %q, want %q", string(data), "legacy-data")
	}
}
