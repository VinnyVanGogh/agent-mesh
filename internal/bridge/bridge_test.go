package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathTranslation(t *testing.T) {
	// Set generic prefixes for test predictability
	origLocal := LocalWorkPrefix
	origRemote := RemoteWorkPrefix
	defer func() {
		LocalWorkPrefix = origLocal
		RemoteWorkPrefix = origRemote
	}()

	LocalWorkPrefix = "/Users/developer/Documents/dev/work"
	RemoteWorkPrefix = "/Users/remote/Documents/dev/work"

	tests := []struct {
		name       string
		local      string
		remote     string
		isWorkRepo bool
	}{
		{
			name:       "work root",
			local:      "/Users/developer/Documents/dev/work",
			remote:     "/Users/remote/Documents/dev/work",
			isWorkRepo: true,
		},
		{
			name:       "api service subrepo",
			local:      "/Users/developer/Documents/dev/work/services/api-service",
			remote:     "/Users/remote/Documents/dev/work/services/api-service",
			isWorkRepo: true,
		},
		{
			name:       "deep nested file",
			local:      "/Users/developer/Documents/dev/work/automation/scripts/sync.sh",
			remote:     "/Users/remote/Documents/dev/work/automation/scripts/sync.sh",
			isWorkRepo: true,
		},
		{
			name:       "non work repo",
			local:      "/Users/developer/Documents/personal/project",
			remote:     "/Users/developer/Documents/personal/project",
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
		"author_identities": ["VinnyVanGogh", "Developer"],
		"repos": [
			{
				"name": "Analytics Service",
				"path": "/Users/developer/Documents/dev/work/services/analytics-api",
				"projects": ["analytics"]
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
	if cfg.Repos[0].Name != "Analytics Service" {
		t.Errorf("unexpected repo name: %s", cfg.Repos[0].Name)
	}

	repo := FindMappedRepo(cfg, "/Users/developer/Documents/dev/work/services/analytics-api/src/main.py")
	if repo == nil {
		t.Fatalf("expected mapped repo, got nil")
	}
	if repo.Name != "Analytics Service" {
		t.Errorf("expected repo name 'Analytics Service', got %s", repo.Name)
	}
}
