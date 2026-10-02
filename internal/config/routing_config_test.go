package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// ---------------------------------------------------------------------------
// 1. [routing] table parsing from config.toml
// ---------------------------------------------------------------------------

func TestLoadConfig_RoutingTableParsed(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
data_dir = "` + dir + `"

[routing]

[[routing.coding]]
provider = "claude-opus"
model    = "opus"
enabled  = true

[[routing.coding]]
provider = "gemini-3.1-pro"
model    = "gemini-3.1-pro-high"
enabled  = true

[[routing.architecture]]
provider = "gemini-3.1-pro"
model    = "gemini-3.1-pro-high"
enabled  = true

[[routing.architecture]]
provider = "claude-cloud"
model    = "opus"
enabled  = false
cloud_credit_expires = "2026-11-04T00:00:00Z"

[[routing.architecture]]
provider = "claude-opus"
model    = "opus"
enabled  = true

[[routing.planning]]
provider = "gemini-3.8-flash"
model    = "gemini-3.8-flash-high"
enabled  = true

[[routing.planning]]
provider = "claude-cloud"
model    = "opus"
enabled  = false

[[routing.planning]]
provider = "claude-sonnet"
model    = "sonnet"
enabled  = true

[[routing.qa]]
provider = "gemini-3.8-flash"
model    = "gemini-3.8-flash-high"
enabled  = true

[[routing.qa]]
provider = "claude-sonnet"
model    = "sonnet"
enabled  = true
`
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// LoadConfig reads ~/.staypoint/config.toml; override data dir via env
	t.Setenv("HOME", dir)
	stDir := filepath.Join(dir, ".staypoint")
	if err := os.MkdirAll(stDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stDir, "config.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Routing == nil {
		t.Fatal("cfg.Routing is nil; [routing] table was not parsed")
	}
	if len(cfg.Routing.Coding) != 2 {
		t.Errorf("routing.coding: want 2 entries, got %d", len(cfg.Routing.Coding))
	}
	if len(cfg.Routing.Architecture) != 3 {
		t.Errorf("routing.architecture: want 3 entries, got %d", len(cfg.Routing.Architecture))
	}
	if len(cfg.Routing.Planning) != 3 {
		t.Errorf("routing.planning: want 3 entries, got %d", len(cfg.Routing.Planning))
	}
	if len(cfg.Routing.QA) != 2 {
		t.Errorf("routing.qa: want 2 entries, got %d", len(cfg.Routing.QA))
	}
}

func TestLoadConfig_MissingRoutingTableIsNil(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `data_dir = "` + dir + `"
engineer_name = "test"
`
	t.Setenv("HOME", dir)
	stDir := filepath.Join(dir, ".staypoint")
	if err := os.MkdirAll(stDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stDir, "config.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// When the [routing] table is absent, cfg.Routing must be nil
	// so the caller can fall back to DefaultKindChains.
	if cfg.Routing != nil {
		t.Errorf("expected cfg.Routing nil when [routing] absent; got non-nil: %+v", cfg.Routing)
	}
}

func TestRoutingConfig_TOMLRoundTrip(t *testing.T) {
	// Direct TOML unmarshal into RoutingConfig (does not require LoadConfig).
	const raw = `
[[coding]]
provider = "claude-opus"
model    = "opus"
enabled  = true

[[coding]]
provider = "gemini-3.1-pro"
model    = "gemini-3.1-pro-high"
enabled  = true

[[architecture]]
provider = "gemini-3.1-pro"
model    = "gemini-3.1-pro-high"
enabled  = true

[[qa]]
provider = "gemini-3.8-flash"
model    = "gemini-3.8-flash-high"
enabled  = true
`
	var rc RoutingConfig
	if err := toml.Unmarshal([]byte(raw), &rc); err != nil {
		t.Fatalf("TOML unmarshal: %v", err)
	}
	if len(rc.Coding) != 2 {
		t.Errorf("Coding: want 2, got %d", len(rc.Coding))
	}
	if len(rc.Architecture) != 1 {
		t.Errorf("Architecture: want 1, got %d", len(rc.Architecture))
	}
	if len(rc.QA) != 1 {
		t.Errorf("QA: want 1, got %d", len(rc.QA))
	}
}

func TestRoutingKindEntry_EnabledDefaultTrue(t *testing.T) {
	// When enabled is omitted in TOML, the slot should be considered enabled.
	// A nil *bool Enabled means "not set" → treat as true.
	const raw = `
[[coding]]
provider = "claude-opus"
model    = "opus"
`
	var rc RoutingConfig
	if err := toml.Unmarshal([]byte(raw), &rc); err != nil {
		t.Fatalf("TOML unmarshal: %v", err)
	}
	if len(rc.Coding) == 0 {
		t.Fatal("Coding is empty after unmarshal")
	}
	entry := rc.Coding[0]
	if entry.Enabled != nil && !*entry.Enabled {
		t.Errorf("Enabled field when absent should be nil (true by convention), got false")
	}
}
