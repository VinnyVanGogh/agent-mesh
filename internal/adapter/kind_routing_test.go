package adapter

import (
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

// ---------------------------------------------------------------------------
// ResolveKindProviderChain — these tests MUST FAIL before the fix is applied.
// They verify that work_kind drives provider + model selection and that the
// route display string matches what the adapter actually receives.
// ---------------------------------------------------------------------------

func openPacer() *router.PacerState {
	return &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolPersonalClaude: {IsLocked: false},
			router.PoolWorkClaude:     {IsLocked: false},
			router.PoolGeminiNative:   {IsLocked: false},
		},
	}
}

func lockedClaudePacer() *router.PacerState {
	return &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolPersonalClaude: {IsLocked: true},
			router.PoolWorkClaude:     {IsLocked: true},
			router.PoolGeminiNative:   {IsLocked: false},
		},
	}
}

func lockedGeminiPacer() *router.PacerState {
	return &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolPersonalClaude: {IsLocked: false},
			router.PoolWorkClaude:     {IsLocked: false},
			router.PoolGeminiNative:   {IsLocked: true},
		},
	}
}

// TestResolveKindProviderChain_CodingAllOpen: coding + all pools open →
// provider="claude", model="opus", route="Ran on Claude Opus".
func TestResolveKindProviderChain_CodingAllOpen(t *testing.T) {
	res := ResolveKindProviderChain(router.WorkKindCoding, openPacer())
	if res.AllLocked {
		t.Fatal("expected a viable slot but got AllLocked")
	}
	if res.Provider != "claude" {
		t.Errorf("coding all-open: want provider=%q, got %q", "claude", res.Provider)
	}
	if len(res.ModelArgs) < 2 || res.ModelArgs[0] != "--model" || res.ModelArgs[1] != "opus" {
		t.Errorf("coding all-open: want ModelArgs=[--model opus], got %v", res.ModelArgs)
	}
	if res.RouteDisplay != "Ran on Claude Opus" {
		t.Errorf("coding all-open: want RouteDisplay=%q, got %q", "Ran on Claude Opus", res.RouteDisplay)
	}
}

// TestResolveKindProviderChain_CodingClaudeLocked: coding + claude locked →
// provider="gemini", model="gemini-3.1-pro-high", route="Fell back to Gemini 3.1 Pro: Claude Opus quota locked".
func TestResolveKindProviderChain_CodingClaudeLocked(t *testing.T) {
	res := ResolveKindProviderChain(router.WorkKindCoding, lockedClaudePacer())
	if res.AllLocked {
		t.Fatal("expected gemini fallback but got AllLocked")
	}
	if res.Provider != "gemini" {
		t.Errorf("coding claude-locked: want provider=%q, got %q", "gemini", res.Provider)
	}
	if len(res.ModelArgs) < 2 || res.ModelArgs[1] != "gemini-3.1-pro-high" {
		t.Errorf("coding claude-locked: want ModelArgs=[--model gemini-3.1-pro-high], got %v", res.ModelArgs)
	}
	if !strings.Contains(res.RouteDisplay, "Fell back to Gemini 3.1 Pro") {
		t.Errorf("coding claude-locked: want RouteDisplay containing 'Fell back to Gemini 3.1 Pro', got %q", res.RouteDisplay)
	}
	if !strings.Contains(res.RouteDisplay, "Claude Opus") {
		t.Errorf("coding claude-locked: route must name locked primary 'Claude Opus', got %q", res.RouteDisplay)
	}
}

// TestResolveKindProviderChain_PlanningAllOpen: planning + all open →
// provider="gemini", model="gemini-3.8-flash-high".
func TestResolveKindProviderChain_PlanningAllOpen(t *testing.T) {
	res := ResolveKindProviderChain(router.WorkKindPlanning, openPacer())
	if res.AllLocked {
		t.Fatal("expected a viable slot but got AllLocked")
	}
	if res.Provider != "gemini" {
		t.Errorf("planning all-open: want provider=%q, got %q", "gemini", res.Provider)
	}
	if len(res.ModelArgs) < 2 || res.ModelArgs[1] != "gemini-3.8-flash-high" {
		t.Errorf("planning all-open: want ModelArgs=[--model gemini-3.8-flash-high], got %v", res.ModelArgs)
	}
	if res.RouteDisplay != "Ran on Gemini 3.8 Flash" {
		t.Errorf("planning all-open: want RouteDisplay=%q, got %q", "Ran on Gemini 3.8 Flash", res.RouteDisplay)
	}
}

// TestResolveKindProviderChain_QAGeminiLocked: qa + gemini locked →
// provider="claude", model="sonnet".
func TestResolveKindProviderChain_QAGeminiLocked(t *testing.T) {
	res := ResolveKindProviderChain(router.WorkKindQA, lockedGeminiPacer())
	if res.AllLocked {
		t.Fatal("expected claude-sonnet fallback but got AllLocked")
	}
	if res.Provider != "claude" {
		t.Errorf("qa gemini-locked: want provider=%q, got %q", "claude", res.Provider)
	}
	if len(res.ModelArgs) < 2 || res.ModelArgs[1] != "sonnet" {
		t.Errorf("qa gemini-locked: want ModelArgs=[--model sonnet], got %v", res.ModelArgs)
	}
	if !strings.Contains(res.RouteDisplay, "Fell back to") {
		t.Errorf("qa gemini-locked: want fallback route, got %q", res.RouteDisplay)
	}
}

// TestResolveKindProviderChain_RouteMatchesAdapterInput:
// The route row text must equal the provider and model the adapter stub receives —
// one assertion, same source.
func TestResolveKindProviderChain_RouteMatchesAdapterInput(t *testing.T) {
	cases := []struct {
		kind   router.WorkKind
		pacer  *router.PacerState
		wantP  string
		wantM  string
		wantRD string
	}{
		{router.WorkKindCoding, openPacer(), "claude", "opus", "Ran on Claude Opus"},
		{router.WorkKindCoding, lockedClaudePacer(), "gemini", "gemini-3.1-pro-high", ""},
		{router.WorkKindPlanning, openPacer(), "gemini", "gemini-3.8-flash-high", "Ran on Gemini 3.8 Flash"},
		{router.WorkKindQA, lockedGeminiPacer(), "claude", "sonnet", ""},
	}
	for _, tc := range cases {
		res := ResolveKindProviderChain(tc.kind, tc.pacer)
		if res.AllLocked {
			t.Errorf("kind=%q: unexpected AllLocked", tc.kind)
			continue
		}
		if res.Provider != tc.wantP {
			t.Errorf("kind=%q: provider want=%q got=%q", tc.kind, tc.wantP, res.Provider)
		}
		if len(res.ModelArgs) >= 2 && res.ModelArgs[1] != tc.wantM {
			t.Errorf("kind=%q: model want=%q got=%q", tc.kind, tc.wantM, res.ModelArgs[1])
		}
		if tc.wantRD != "" && res.RouteDisplay != tc.wantRD {
			t.Errorf("kind=%q: RouteDisplay want=%q got=%q", tc.kind, tc.wantRD, res.RouteDisplay)
		}
		// The route display must contain the model that would be passed to the adapter.
		// This is the "one assertion, same source" requirement from STA-531.
		if !routeDisplayContainsModel(res.RouteDisplay, res.ModelArgs) {
			t.Errorf("kind=%q: RouteDisplay %q does not reference the adapter model %v",
				tc.kind, res.RouteDisplay, res.ModelArgs)
		}
	}
}

// routeDisplayContainsModel checks that the route display string refers to the
// same model tier as the adapter will receive.
func routeDisplayContainsModel(display string, modelArgs []string) bool {
	if len(modelArgs) < 2 {
		return false
	}
	model := modelArgs[1]
	switch model {
	case "opus":
		return strings.Contains(display, "Opus")
	case "sonnet":
		return strings.Contains(display, "Sonnet")
	case "gemini-3.1-pro-high":
		return strings.Contains(display, "Gemini 3.1 Pro")
	case "gemini-3.8-flash-high":
		return strings.Contains(display, "Gemini 3.8 Flash")
	}
	return false
}
