package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/bridge"
	"github.com/VinnyVanGogh/agent-mesh/internal/config"
)

type mockFileInfo struct {
	isDir bool
}

func (m mockFileInfo) Name() string       { return "mock" }
func (m mockFileInfo) Size() int64        { return 100 }
func (m mockFileInfo) Mode() os.FileMode  { return 0755 }
func (m mockFileInfo) ModTime() time.Time { return time.Now() }
func (m mockFileInfo) IsDir() bool        { return m.isDir }
func (m mockFileInfo) Sys() interface{}   { return nil }

func newHealthyDoctor() *FleetDoctor {
	doc := NewFleetDoctor(DoctorOptions{
		RemoteHost: "test-host",
		Config: &config.Config{
			RemoteHost:     "test-host",
			RemoteRepoRoot: "~/Documents/dev/work",
		},
	})
	doc.SetLocalVersion("0.1.0")

	// Mocks for clean healthy state
	doc.LocalVersionFunc = func() (string, error) { return "0.1.0", nil }
	doc.RemoteVersionFunc = func(ctx context.Context, host string) (string, time.Duration, error) {
		return "0.1.0", 15 * time.Millisecond, nil
	}
	doc.ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) bridge.ProbeResult {
		return bridge.ProbeResult{Host: host, Reachable: true, Latency: 12 * time.Millisecond}
	}
	doc.CheckPortFunc = func(port int) (bool, string, time.Duration, error) {
		return true, "Loopback port 4119 active", 5 * time.Millisecond, nil
	}
	doc.LocalFileHashFunc = func(path string) (string, error) {
		return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
	}
	doc.RemoteFileHashFunc = func(ctx context.Context, host string, path string) (string, time.Duration, error) {
		return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 20 * time.Millisecond, nil
	}
	doc.CheckTelemetryDBFunc = func(path string) error { return nil }
	doc.CheckStateAgeFunc = func(path string) (time.Duration, error) { return 10 * time.Minute, nil }

	doc.LookPathFunc = func(file string) (string, error) { return "/usr/local/bin/" + file, nil }
	doc.ClaudeDoctorFunc = func(ctx context.Context) (string, time.Duration, error) {
		return "No installation issues found.", 50 * time.Millisecond, nil
	}

	validClaudeJSON := `{"oauthAccount": {"email": "test@example.com"}}`
	validSettingsJSON := `{
		"hooks": {
			"UserPromptSubmit": [
				{
					"hooks": [
						{"type": "command", "command": "mesh hook prompt"}
					]
				}
			]
		}
	}`
	validMCPJSON := `{"mcpServers": {"test-mcp": {"command": "test"}}}`
	validStateJSON := `{
		"quotas": {
			"Gemini": {
				"five_hour_used": 5.0,
				"five_hour_remaining": 95.0
			}
		}
	}`

	doc.ReadFileFunc = func(filename string) ([]byte, error) {
		if strings.HasSuffix(filename, ".claude.json") {
			return []byte(validClaudeJSON), nil
		}
		if strings.HasSuffix(filename, "settings.json") {
			return []byte(validSettingsJSON), nil
		}
		if strings.HasSuffix(filename, "mcp_config.json") {
			return []byte(validMCPJSON), nil
		}
		if strings.HasSuffix(filename, "state.json") {
			return []byte(validStateJSON), nil
		}
		return nil, os.ErrNotExist
	}

	doc.StatFileFunc = func(name string) (os.FileInfo, error) {
		if strings.Contains(name, ".gemini/config") {
			return mockFileInfo{isDir: true}, nil
		}
		if strings.Contains(name, ".caveman-mode") {
			return mockFileInfo{isDir: false}, nil
		}
		return nil, os.ErrNotExist
	}

	doc.RemoteCommandFunc = func(ctx context.Context, host string, cmd string) (string, time.Duration, error) {
		if strings.Contains(cmd, "settings.json") {
			return validSettingsJSON, 25 * time.Millisecond, nil
		}
		// Mapped repos probe
		return "FOUND\nFOUND\nFOUND", 30 * time.Millisecond, nil
	}

	return doc
}

func TestDoctor_AllHealthy(t *testing.T) {
	doc := newHealthyDoctor()
	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(report.Sections))
	}
	if report.Summary.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", report.Summary.Errors)
	}
	if report.Summary.Warnings != 0 {
		t.Errorf("expected 0 warnings, got %d", report.Summary.Warnings)
	}
	if report.Summary.Passed == 0 {
		t.Errorf("expected passed > 0, got %d", report.Summary.Passed)
	}

	formatted := doc.FormatReport(report)
	if !strings.Contains(formatted, "Fleet Doctor Engine") {
		t.Errorf("expected title in formatted report")
	}
	if !strings.Contains(formatted, "0 errors") {
		t.Errorf("expected '0 errors' in summary footer")
	}
}

func TestDoctor_FastMode(t *testing.T) {
	doc := newHealthyDoctor()
	doc.opts.Fast = true

	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.Summary.Skipped == 0 {
		t.Errorf("expected skipped checks in fast mode, got 0")
	}

	formatted := doc.FormatReport(report)
	if !strings.Contains(formatted, "Fast (Remote network checks skipped)") {
		t.Errorf("expected fast mode notification in output")
	}
}

func TestDoctor_SectionFilters(t *testing.T) {
	ctx := context.Background()

	// Claude Only
	docClaude := newHealthyDoctor()
	docClaude.opts.ClaudeOnly = true
	repClaude, err := docClaude.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repClaude.Sections) != 1 || repClaude.Sections[0].ID != "claude" {
		t.Errorf("expected only claude section, got %+v", repClaude.Sections)
	}

	// Gemini Only
	docGemini := newHealthyDoctor()
	docGemini.opts.GeminiOnly = true
	repGemini, err := docGemini.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repGemini.Sections) != 1 || repGemini.Sections[0].ID != "gemini" {
		t.Errorf("expected only gemini section, got %+v", repGemini.Sections)
	}

	// Remote Only
	docRemote := newHealthyDoctor()
	docRemote.opts.RemoteOnly = true
	repRemote, err := docRemote.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repRemote.Sections) != 1 || repRemote.Sections[0].ID != "remote" {
		t.Errorf("expected only remote section, got %+v", repRemote.Sections)
	}
}

func TestDoctor_VersionMismatch(t *testing.T) {
	doc := newHealthyDoctor()
	doc.RemoteVersionFunc = func(ctx context.Context, host string) (string, time.Duration, error) {
		return "0.9.9", 10 * time.Millisecond, nil
	}

	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundWarn := false
	for _, chk := range report.Sections[0].Checks {
		if chk.Name == "Version Parity" {
			if chk.Status != StatusWarn {
				t.Errorf("expected StatusWarn for version mismatch, got %s", chk.Status)
			}
			if !strings.Contains(chk.Message, "Version mismatch") {
				t.Errorf("expected version mismatch message, got: %s", chk.Message)
			}
			if chk.Remediation == "" {
				t.Errorf("expected remediation for version mismatch")
			}
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Errorf("Version Parity check not found")
	}
}

func TestDoctor_SSHUnreachable(t *testing.T) {
	doc := newHealthyDoctor()
	doc.ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) bridge.ProbeResult {
		return bridge.ProbeResult{
			Host:      host,
			Reachable: false,
			Error:     "connection timed out",
		}
	}

	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundErr := false
	for _, chk := range report.Sections[0].Checks {
		if chk.Name == "SSH Connectivity" {
			if chk.Status != StatusError {
				t.Errorf("expected StatusError for unreachable SSH, got %s", chk.Status)
			}
			if !strings.Contains(chk.Message, "unreachable") {
				t.Errorf("expected unreachable message, got: %s", chk.Message)
			}
			if chk.Remediation == "" {
				t.Errorf("expected remediation for SSH error")
			}
			foundErr = true
		}
	}
	if !foundErr {
		t.Errorf("SSH Connectivity check not found")
	}
}

func TestDoctor_ReverseTunnel(t *testing.T) {
	doc := newHealthyDoctor()
	// Port unavailable
	doc.CheckPortFunc = func(port int) (bool, string, time.Duration, error) {
		return false, "bind: address already in use", 2 * time.Millisecond, errors.New("bind failed")
	}

	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, chk := range report.Sections[0].Checks {
		if chk.Name == "Reverse Tunnel" {
			if chk.Status != StatusWarn {
				t.Errorf("expected StatusWarn for occupied port, got %s", chk.Status)
			}
			if !strings.Contains(chk.Remediation, "4119") {
				t.Errorf("expected remediation mentioning port 4119")
			}
		}
	}
}

func TestDoctor_InstructionDrift(t *testing.T) {
	doc := newHealthyDoctor()
	doc.RemoteFileHashFunc = func(ctx context.Context, host string, path string) (string, time.Duration, error) {
		return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 20 * time.Millisecond, nil
	}

	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, chk := range report.Sections[0].Checks {
		if chk.Name == "Instruction Parity" {
			if chk.Status != StatusWarn {
				t.Errorf("expected StatusWarn for drift, got %s", chk.Status)
			}
			if !strings.Contains(chk.Message, "drift") {
				t.Errorf("expected drift message, got %s", chk.Message)
			}
		}
	}
}

func TestDoctor_DatabaseAndQuota(t *testing.T) {
	// DB error
	doc := newHealthyDoctor()
	doc.CheckTelemetryDBFunc = func(path string) error {
		return errors.New("corrupt file")
	}
	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, chk := range report.Sections[0].Checks {
		if chk.Name == "Database & Quota" {
			if chk.Status != StatusError {
				t.Errorf("expected StatusError for bad DB, got %s", chk.Status)
			}
		}
	}

	// Stale state.json (>24h)
	doc2 := newHealthyDoctor()
	doc2.CheckStateAgeFunc = func(path string) (time.Duration, error) {
		return 36 * time.Hour, nil
	}
	report2, _ := doc2.Run(context.Background())
	for _, chk := range report2.Sections[0].Checks {
		if chk.Name == "Database & Quota" {
			if chk.Status != StatusWarn {
				t.Errorf("expected StatusWarn for stale state, got %s", chk.Status)
			}
			if !strings.Contains(chk.Message, "stale") {
				t.Errorf("expected 'stale' in message, got: %s", chk.Message)
			}
		}
	}
}

func TestDoctor_ClaudeHealthChecks(t *testing.T) {
	// CLI not found
	doc := newHealthyDoctor()
	doc.LookPathFunc = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	report, _ := doc.Run(context.Background())
	for _, chk := range report.Sections[1].Checks {
		if chk.Name == "Claude CLI" && chk.Status != StatusError {
			t.Errorf("expected StatusError when claude CLI missing, got %s", chk.Status)
		}
		if chk.Name == "Claude Doctor" && chk.Status != StatusSkipped {
			t.Errorf("expected doctor skipped when CLI missing, got %s", chk.Status)
		}
	}

	// Missing hook
	docHook := newHealthyDoctor()
	docHook.ReadFileFunc = func(filename string) ([]byte, error) {
		if strings.HasSuffix(filename, ".claude.json") {
			return []byte(`{"oauthAccount": {}}`), nil
		}
		if strings.HasSuffix(filename, "settings.json") {
			return []byte(`{"hooks": {}}`), nil
		}
		return nil, os.ErrNotExist
	}
	repHook, _ := docHook.Run(context.Background())
	for _, chk := range repHook.Sections[1].Checks {
		if chk.Name == "Claude Settings & Hooks" {
			if chk.Status != StatusWarn {
				t.Errorf("expected StatusWarn when hook missing, got %s", chk.Status)
			}
		}
	}
}

func TestDoctor_GeminiHealthChecks(t *testing.T) {
	doc := newHealthyDoctor()
	// Quota locked
	doc.ReadFileFunc = func(filename string) ([]byte, error) {
		if strings.HasSuffix(filename, "state.json") {
			return []byte(`{
				"lockouts": {"Gemini": {"locked": true}},
				"quotas": {}
			}`), nil
		}
		if strings.HasSuffix(filename, "mcp_config.json") {
			return []byte(`{"mcpServers": {}}`), nil
		}
		return []byte(`{}`), nil
	}
	doc.StatFileFunc = func(name string) (os.FileInfo, error) {
		if strings.Contains(name, ".gemini/config") {
			return mockFileInfo{isDir: true}, nil
		}
		return nil, os.ErrNotExist // no caveman mode
	}

	report, _ := doc.Run(context.Background())
	for _, chk := range report.Sections[2].Checks {
		if chk.Name == "Google AI Ultra Quota" && chk.Status != StatusError {
			t.Errorf("expected StatusError for locked quota, got %s", chk.Status)
		}
		if chk.Name == "MCP Servers" && chk.Status != StatusWarn {
			t.Errorf("expected StatusWarn for empty MCP servers, got %s", chk.Status)
		}
		if chk.Name == "Caveman Mode" && chk.Status != StatusWarn {
			t.Errorf("expected StatusWarn for missing caveman mode, got %s", chk.Status)
		}
	}
}

func TestDoctor_RemoteNodeChecks(t *testing.T) {
	doc := newHealthyDoctor()
	doc.RemoteCommandFunc = func(ctx context.Context, host string, cmd string) (string, time.Duration, error) {
		if strings.Contains(cmd, "settings.json") {
			return `{"hooks": {}}`, 20 * time.Millisecond, nil
		}
		return "FOUND\nMISSING\nMISSING", 20 * time.Millisecond, nil
	}

	report, _ := doc.Run(context.Background())
	for _, chk := range report.Sections[3].Checks {
		if chk.Name == "Remote Claude Settings & Hook" && chk.Status != StatusWarn {
			t.Errorf("expected StatusWarn for missing remote prompt hook, got %s", chk.Status)
		}
		if chk.Name == "Mapped Repositories" && chk.Status != StatusWarn {
			t.Errorf("expected StatusWarn when some mapped repos missing, got %s", chk.Status)
		}
	}
}

func TestDoctor_JSONOutput(t *testing.T) {
	doc := newHealthyDoctor()
	report, err := doc.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("failed to marshal report to JSON: %v", err)
	}

	var parsed Report
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal report from JSON: %v", err)
	}
	if parsed.RemoteHost != "test-host" {
		t.Errorf("expected remote host 'test-host', got %s", parsed.RemoteHost)
	}
	if parsed.Summary.Passed != report.Summary.Passed {
		t.Errorf("expected %d passed in unmarshaled JSON, got %d", report.Summary.Passed, parsed.Summary.Passed)
	}
}
