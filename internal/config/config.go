package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	DataDir         string  `json:"data_dir" toml:"data_dir"`
	DBPath          string  `json:"db_path" toml:"db_path"`
	TelemetryDBPath string  `json:"telemetry_db_path" toml:"telemetry_db_path"`
	CompanyName     string  `json:"company_name" toml:"company_name"`
	EngineerName    string  `json:"engineer_name" toml:"engineer_name"`
	HourlyRate      float64 `json:"hourly_rate" toml:"hourly_rate"`
	WorkEmail       string  `json:"work_email" toml:"work_email"`
	PersonalEmail   string  `json:"personal_email" toml:"personal_email"`
	WorkRepoRoot    string  `json:"work_repo_root" toml:"work_repo_root"`
	RemoteHost      string  `json:"remote_host" toml:"remote_host"`
	RemoteRepoRoot  string  `json:"remote_repo_root" toml:"remote_repo_root"` // e.g. "~/Documents/dev/managed_solution" or "~/Documents/dev/work"
	MachineRole     string  `json:"machine_role" toml:"machine_role"` // "hybrid" (default), "work", or "personal"
	GooglePlanTier  string  `json:"google_plan_tier" toml:"google_plan_tier"` // e.g. "Google AI Ultra" or "Ultra"
	ClaudePlanTier  string  `json:"claude_plan_tier" toml:"claude_plan_tier"` // e.g. "Max 5x" or "Pro"
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	dataDir := filepath.Join(home, ".agent-mesh")
	return &Config{
		DataDir:         dataDir,
		DBPath:          filepath.Join(dataDir, "mesh.db"),
		TelemetryDBPath: filepath.Join(home, ".config", "token-telemetry", "telemetry.db"),
		CompanyName:     "",
		EngineerName:    "",
		HourlyRate:      0.0,
		WorkEmail:       "engineer@company.com",
		PersonalEmail:   "personal@gmail.com",
		WorkRepoRoot:    filepath.Join(home, "Documents", "dev", "work"),
		RemoteHost:      "company-mbp",
		RemoteRepoRoot:  "~/Documents/dev/work",
		MachineRole:     "hybrid",
		GooglePlanTier:  "Google AI Ultra",
		ClaudePlanTier:  "Pro",
	}
}

// LoadConfig loads configuration from ~/.agent-mesh/config.toml, then config.json if toml does not exist.
// If neither exists, DefaultConfig() is returned.
func LoadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	cfg := DefaultConfig()
	dataDir := filepath.Join(home, ".agent-mesh")
	tomlPath := filepath.Join(dataDir, "config.toml")
	jsonPath := filepath.Join(dataDir, "config.json")

	var raw []byte
	var isTOML bool

	if data, err := os.ReadFile(tomlPath); err == nil {
		raw = data
		isTOML = true
	} else if data, err := os.ReadFile(jsonPath); err == nil {
		raw = data
		isTOML = false
	} else {
		return cfg, nil
	}

	if isTOML {
		if err := toml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config.toml: %w", err)
		}
	} else {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config.json: %w", err)
		}
	}

	cfg.WorkRepoRoot = expandPath(cfg.WorkRepoRoot, home)
	cfg.DataDir = expandPath(cfg.DataDir, home)
	cfg.DBPath = expandPath(cfg.DBPath, home)
	cfg.TelemetryDBPath = expandPath(cfg.TelemetryDBPath, home)

	if cfg.RemoteRepoRoot == "" {
		if strings.Contains(strings.ToLower(cfg.CompanyName), "managed solution") ||
			strings.Contains(strings.ToLower(cfg.WorkRepoRoot), "mansol") {
			cfg.RemoteRepoRoot = "~/Documents/dev/managed_solution"
		} else {
			cfg.RemoteRepoRoot = "~/Documents/dev/work"
		}
	}

	return cfg, nil
}

func expandPath(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	if path == "~" {
		return home
	}
	return path
}

func EnsureDataDir(cfg *Config) error {
	return os.MkdirAll(cfg.DataDir, 0755)
}
