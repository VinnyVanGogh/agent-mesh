package bridge

import (
	"testing"
)

func TestCleanFileName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "CleanShot Teams Chat with Email and Serial",
			input:    "Chat  Automation Team Sync  Managed Solution  vvasile@managedsolution.com  Microsoft Teams_September-24-2026_013188@2x.png",
			expected: "teams_automation_team_sync_2026-09-24.png",
		},
		{
			name:     "CleanShot Teams Features Follow-Up",
			input:    "Chat  Teams Features Follow-Up  Managed Solution  vvasile@managedsolution.com  Microsoft Teams_September-22-2026_013127@2x.png",
			expected: "teams_features_follow_up_2026-09-22.png",
		},
		{
			name:     "CleanShot Todd Bates Chat",
			input:    "Chat  Todd Bates  Managed Solution  vvasile@managedsolution.com  Microsoft Teams_August-25-2026_011575@2x.png",
			expected: "teams_todd_bates_2026-08-25.png",
		},
		{
			name:     "CleanShot Complex Workflow Title",
			input:    "Chat  VPS HR Forms Workflow Automation - Demo #1 (External)  Managed Solution  vvasile@managedsolution.com  Microsoft Teams_August-14-2026_011303@2x.png",
			expected: "teams_vps_hr_forms_workflow_automation_demo_1_external_2026-08-14.png",
		},
		{
			name:     "CleanShot Sumit Ghosh Chat",
			input:    "Chat  Sumit Ghosh  Microsoft Teams_July-30-2026_010427@2x.png",
			expected: "teams_sumit_ghosh_2026-07-30.png",
		},
		{
			name:     "macOS Standard Screenshot AM",
			input:    "Screenshot 2026-09-24 at 10.28.06 AM.png",
			expected: "screenshot_2026-09-24_102806.png",
		},
		{
			name:     "macOS Screen Shot PM 12-hour conversion",
			input:    "Screen Shot 2026-09-24 at 1.15.30 PM.png",
			expected: "screenshot_2026-09-24_131530.png",
		},
		{
			name:     "CleanShot Timestamp",
			input:    "CleanShot 2026-09-24 at 10.28.06@2x.png",
			expected: "cleanshot_2026-09-24_102806.png",
		},
		{
			name:     "Spaced Document PDF",
			input:    "User Lifecycle Deployment Guide.pdf",
			expected: "user_lifecycle_deployment_guide.pdf",
		},
		{
			name:     "Already Clean Zip",
			input:    "user_lifecycle_app.zip",
			expected: "user_lifecycle_app.zip",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := CleanFileName(tc.input)
			if actual != tc.expected {
				t.Errorf("CleanFileName(%q) = %q; expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestIsOpaqueImage(t *testing.T) {
	opaque := []string{
		"Screenshot 2026-09-24 at 10.28.06 AM.png",
		"Screen Shot 2026-09-24.png",
		"CleanShot 2026-09-24 at 10.28.06.png",
		"IMG_1234.png",
		"image.jpg",
	}
	for _, f := range opaque {
		if !IsOpaqueImage(f) {
			t.Errorf("expected %q to be identified as opaque image", f)
		}
	}

	descriptive := []string{
		"Chat  Automation Team Sync  Microsoft Teams.png",
		"user_lifecycle_app.zip",
		"deployment_guide.pdf",
		"aws_architecture_diagram.png",
	}
	for _, f := range descriptive {
		if IsOpaqueImage(f) {
			t.Errorf("expected %q to NOT be identified as opaque image", f)
		}
	}
}
