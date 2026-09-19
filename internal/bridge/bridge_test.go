package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathTranslation(t *testing.T) {
	tests := []struct {
		name       string
		local      string
		remote     string
		isWorkRepo bool
	}{
		{
			name:       "mansol root",
			local:      "/Users/vincevasile/Documents/dev/mansol",
			remote:     "/Users/mansolvv/Documents/dev/managed_solution",
			isWorkRepo: true,
		},
		{
			name:       "partner center subrepo",
			local:      "/Users/vincevasile/Documents/dev/mansol/python_projects/partner-center-api",
			remote:     "/Users/mansolvv/Documents/dev/managed_solution/python_projects/partner-center-api",
			isWorkRepo: true,
		},
		{
			name:       "deep nested file",
			local:      "/Users/vincevasile/Documents/dev/mansol/vps-hr-automation/scripts/sync.sh",
			remote:     "/Users/mansolvv/Documents/dev/managed_solution/vps-hr-automation/scripts/sync.sh",
			isWorkRepo: true,
		},
		{
			name:       "non work repo",
			local:      "/Users/vincevasile/Documents/dev/agent-mesh",
			remote:     "/Users/vincevasile/Documents/dev/agent-mesh",
			isWorkRepo: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translatedRemote := ToRemotePath(tc.local)
			if translatedRemote != tc.remote {
				t.Errorf("ToRemotePath(%q) = %q, want %q", tc.local, translatedRemote, tc.remote)
			}

			translatedLocal := ToLocalPath(tc.remote)
			if translatedLocal != tc.local {
				t.Errorf("ToLocalPath(%q) = %q, want %q", tc.remote, translatedLocal, tc.local)
			}

			if got := IsWorkRepo(tc.local); got != tc.isWorkRepo {
				t.Errorf("IsWorkRepo(%q) = %v, want %v", tc.local, got, tc.isWorkRepo)
			}
			if got := IsWorkRepo(tc.remote); got != tc.isWorkRepo {
				t.Errorf("IsWorkRepo(%q) = %v, want %v", tc.remote, got, tc.isWorkRepo)
			}
		})
	}
}

func TestLoadScanRepos(t *testing.T) {
	tmpDir := t.TempDir()
	jsonPath := filepath.Join(tmpDir, "scan-repos.json")

	content := `{
		"author_identities": ["VinnyVanGogh", "Vince Vasile"],
		"repos": [
			{
				"name": "Partner Center Analytics",
				"path": "/Users/vincevasile/Documents/dev/mansol/python_projects/partner-center-api",
				"projects": ["partner-center"]
			}
		]
	}`

	if err := os.WriteFile(jsonPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write tmp scan-repos.json: %v", err)
	}

	cfg, err := LoadScanRepos(jsonPath)
	if err != nil {
		t.Fatalf("LoadScanRepos failed: %v", err)
	}

	if len(cfg.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(cfg.Repos))
	}
	if cfg.Repos[0].Name != "Partner Center Analytics" {
		t.Errorf("unexpected repo name: %s", cfg.Repos[0].Name)
	}

	repo := FindMappedRepo(cfg, "/Users/vincevasile/Documents/dev/mansol/python_projects/partner-center-api/src/main.py")
	if repo == nil {
		t.Fatalf("expected mapped repo, got nil")
	}
	if repo.Name != "Partner Center Analytics" {
		t.Errorf("expected repo name 'Partner Center Analytics', got %s", repo.Name)
	}
}
