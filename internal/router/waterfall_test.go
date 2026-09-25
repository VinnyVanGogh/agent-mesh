package router

import (
	"context"
	"testing"
	"time"
)

func TestIsWorkRepoDetection(t *testing.T) {
	testCases := []struct {
		path     string
		expected bool
	}{
		{"/Users/vincevasile/Documents/dev/worktrees/feat-auth", true},
		{"/Users/vincevasile/Documents/dev/mansol-apps-server/github_repo-prod", true},
		{"/Users/vincevasile/Documents/dev/mansol/vps-hr-automation", true},
		{"/Users/vincevasile/Documents/dev/managed-solution-dashboard", true},
		{"/Users/vincevasile/Documents/dev/personal-game", false},
		{"/Users/vincevasile/Documents/dev/bassline", false},
	}

	for _, tc := range testCases {
		isWork, src, err := IsWorkRepo(tc.path)
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", tc.path, err)
		}
		if isWork != tc.expected {
			t.Errorf("path %s: expected isWork=%v, got %v (source: %s)", tc.path, tc.expected, isWork, src)
		}
	}
}

func TestBalancedPeerPacingRouting(t *testing.T) {
	ctx := context.Background()

	// Base pacer state with healthy pools
	basePacer := &PacerState{
		Pools: map[PoolID]*QuotaPool{
			PoolGeminiNative: {
				TurnsRunway: 100,
				FiveHour:    QuotaWindow{RemainingPct: 80.0},
				Weekly:      QuotaWindow{RemainingPct: 40.0}, // 40% left
			},
			PoolPersonalClaude: {
				TurnsRunway: 80,
				FiveHour:    QuotaWindow{RemainingPct: 100.0},
				Weekly:      QuotaWindow{RemainingPct: 90.0}, // 90% left
			},
			Pool3PClaude: {
				TurnsRunway: 50,
				FiveHour:    QuotaWindow{RemainingPct: 100.0},
				Weekly:      QuotaWindow{RemainingPct: 100.0},
			},
		},
	}

	// Case 1: Claude has significantly more weekly headroom (90% vs 40%) -> favors Claude
	dec1, err := Route(ctx, "/Users/vincevasile/Documents/dev/personal-app", basePacer, RouteOptions{
		CheckSSH: false,
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if dec1.Tool != "claude" {
		t.Errorf("expected tool claude for higher headroom, got %s (reason: %s)", dec1.Tool, dec1.Reason)
	}

	// Case 2: Repo continuity within 20% margin: LastUsedTool is agy sticks with agy even if Claude has slightly more
	continuityPacer := &PacerState{
		Pools: map[PoolID]*QuotaPool{
			PoolGeminiNative: {
				TurnsRunway: 80,
				FiveHour:    QuotaWindow{RemainingPct: 80.0},
				Weekly:      QuotaWindow{RemainingPct: 75.0},
			},
			PoolPersonalClaude: {
				TurnsRunway: 80,
				FiveHour:    QuotaWindow{RemainingPct: 90.0},
				Weekly:      QuotaWindow{RemainingPct: 85.0}, // 10% difference, within 20% margin
			},
		},
	}
	dec2, err := Route(ctx, "/Users/vincevasile/Documents/dev/personal-app", continuityPacer, RouteOptions{
		CheckSSH:     false,
		LastUsedTool: "agy",
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if dec2.Tool != "agy" {
		t.Errorf("expected tool agy for repo continuity within 20%% margin, got %s", dec2.Tool)
	}

	// Case 2b: Repo continuity overridden when tool is >20% starved compared to peer
	dec2b, err := Route(ctx, "/Users/vincevasile/Documents/dev/personal-app", basePacer, RouteOptions{
		CheckSSH:     false,
		LastUsedTool: "agy", // Gemini is at 40% while Claude is at 90% (50% gap)
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if dec2b.Tool != "claude" {
		t.Errorf("expected tool claude when continuity tool is starved by >20%% margin, got %s", dec2b.Tool)
	}

	// Case 3: User explicit preference: PreferredPersonalTool is claude
	dec3, err := Route(ctx, "/Users/vincevasile/Documents/dev/personal-app", basePacer, RouteOptions{
		CheckSSH:              false,
		PreferredPersonalTool: "claude",
		LastUsedTool:          "agy",
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if dec3.Tool != "claude" {
		t.Errorf("expected tool claude for user preference, got %s", dec3.Tool)
	}

	// Case 4: Gemini Native is locked -> routes to Claude Code
	lockedGeminiPacer := &PacerState{
		Pools: map[PoolID]*QuotaPool{
			PoolGeminiNative: {
				IsLocked:      true,
				LockoutReason: "5h rate limit reached",
				LockoutUntil:  time.Now().Add(1 * time.Hour),
				FiveHour:      QuotaWindow{RemainingPct: 0.0},
				Weekly:        QuotaWindow{RemainingPct: 50.0},
			},
			PoolPersonalClaude: {
				TurnsRunway: 60,
				FiveHour:    QuotaWindow{RemainingPct: 90.0},
				Weekly:      QuotaWindow{RemainingPct: 70.0},
			},
		},
	}
	dec4, err := Route(ctx, "/Users/vincevasile/Documents/dev/personal-app", lockedGeminiPacer, RouteOptions{
		CheckSSH: false,
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if dec4.Tool != "claude" {
		t.Errorf("expected tool claude when Gemini locked, got %s", dec4.Tool)
	}
}
