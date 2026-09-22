package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/agent-mesh/internal/config"
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

func TestSmartPathTranslation(t *testing.T) {
	home := getHomeDir()
	cfg := &config.Config{
		WorkRepoRoot:   filepath.Join(home, "Documents", "dev", "mansol"),
		RemoteRepoRoot: "~/Documents/dev/managed_solution",
	}

	scanCfg := &ScanReposConfig{
		Repos: []RepoMapping{
			{
				Name: "GitHub Repo Prod",
				Path: filepath.Join(home, "Documents", "dev", "mansol-apps-server", "github_repo-prod"),
			},
			{
				Name: "Partner Center API",
				Path: filepath.Join(home, "Documents", "dev", "mansol", "python_projects", "partner-center-api"),
			},
		},
	}

	// 1. Mapped repo root translation
	localRepo := filepath.Join(home, "Documents", "dev", "mansol-apps-server", "github_repo-prod")
	expectedRemote := "~/Documents/dev/managed_solution/github_repo-prod"
	gotRemote := ToRemotePathWithConfig(localRepo, cfg, scanCfg)
	if gotRemote != expectedRemote {
		t.Errorf("ToRemotePathWithConfig(%q) = %q, want %q", localRepo, gotRemote, expectedRemote)
	}

	// 2. Mapped repo subpath translation
	localSub := filepath.Join(home, "Documents", "dev", "mansol-apps-server", "github_repo-prod", "file_sharing", "sync.py")
	expectedSubRemote := "~/Documents/dev/managed_solution/github_repo-prod/file_sharing/sync.py"
	gotSubRemote := ToRemotePathWithConfig(localSub, cfg, scanCfg)
	if gotSubRemote != expectedSubRemote {
		t.Errorf("ToRemotePathWithConfig(%q) = %q, want %q", localSub, gotSubRemote, expectedSubRemote)
	}

	// 3. Reverse translation back to local
	gotLocal := ToLocalPathWithConfig(expectedRemote, cfg, scanCfg)
	if gotLocal != localRepo {
		t.Errorf("ToLocalPathWithConfig(%q) = %q, want %q", expectedRemote, gotLocal, localRepo)
	}

	// 4. Reverse translation of subpath back to local
	gotLocalSub := ToLocalPathWithConfig(expectedSubRemote, cfg, scanCfg)
	if gotLocalSub != localSub {
		t.Errorf("ToLocalPathWithConfig(%q) = %q, want %q", expectedSubRemote, gotLocalSub, localSub)
	}

	// 5. General path under home directory uses ~ instead of local username
	localGeneric := filepath.Join(home, "Documents", "dev", "personal_tool")
	expectedGeneric := "~/Documents/dev/personal_tool"
	gotGeneric := ToRemotePathWithConfig(localGeneric, cfg, scanCfg)
	if gotGeneric != expectedGeneric {
		t.Errorf("ToRemotePathWithConfig(%q) = %q, want %q", localGeneric, gotGeneric, expectedGeneric)
	}
}

func TestShellPathForDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "~/Documents/dev/managed_solution/github_repo-prod",
			want:  `"$HOME"/'Documents/dev/managed_solution/github_repo-prod'`,
		},
		{
			input: "~",
			want:  `"$HOME"`,
		},
		{
			input: "/opt/repos/app",
			want:  `'/opt/repos/app'`,
		},
		{
			input: "~/path with spaces/repo",
			want:  `"$HOME"/'path with spaces/repo'`,
		},
	}

	for _, tc := range tests {
		got := ShellPathForDir(tc.input)
		if got != tc.want {
			t.Errorf("ShellPathForDir(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

