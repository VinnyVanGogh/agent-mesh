package orchestrator_test

import (
	"sync"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
)

func collectPublished(t *testing.T) (func(string, any), *[]string) {
	t.Helper()
	var mu sync.Mutex
	var types []string
	return func(eventType string, _ any) {
		mu.Lock()
		types = append(types, eventType)
		mu.Unlock()
	}, &types
}

func TestStepRecorder_ThinkingStep(t *testing.T) {
	pub, types := collectPublished(t)
	r := orchestrator.NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindThinking, Text: "step 1"})
	r.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindThinking, Text: " more"})
	// Close thinking by sending a tool_use
	r.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindToolUse, ToolName: "Bash", ToolID: "t1"})
	r.Close()

	if len(*types) < 1 {
		t.Fatal("expected at least one published event")
	}
	found := false
	for _, tp := range *types {
		if tp == "run.step" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected run.step event to be published")
	}
}

func TestStepRecorder_GroupsToolPair(t *testing.T) {
	pub, types := collectPublished(t)
	r := orchestrator.NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindToolUse, ToolName: "Read", ToolID: "t1"})
	r.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindToolResult, ToolID: "t1"})

	// Should have published exactly one run.step (tool_use + tool_result = 1 step)
	stepCount := 0
	for _, tp := range *types {
		if tp == "run.step" {
			stepCount++
		}
	}
	if stepCount != 1 {
		t.Errorf("expected 1 run.step event, got %d", stepCount)
	}
}

func TestStepRecorder_UsagePublishesStats(t *testing.T) {
	pub, types := collectPublished(t)
	r := orchestrator.NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindUsage, Usage: &orchestrator.RecUsage{
		InputTokens:  100,
		OutputTokens: 50,
	}})

	found := false
	for _, tp := range *types {
		if tp == "run.stats" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected run.stats event from RecKindUsage")
	}
}

func TestStepRecorder_WakeEmitsDone(t *testing.T) {
	pub, types := collectPublished(t)
	r := orchestrator.NewStepRecorder(nil, pub, "run1", "task1")
	r.EmitWake("issue_assigned")

	if len(*types) == 0 || (*types)[0] != "run.step" {
		t.Error("expected run.step from EmitWake")
	}
}

func TestStepRecorder_StateEmitsBoth(t *testing.T) {
	pub, types := collectPublished(t)
	r := orchestrator.NewStepRecorder(nil, pub, "run1", "task1")
	r.EmitState("done")

	stepFound, stateFound := false, false
	for _, tp := range *types {
		switch tp {
		case "run.step":
			stepFound = true
		case "run.state":
			stateFound = true
		}
	}
	if !stepFound || !stateFound {
		t.Errorf("expected run.step and run.state, got %v", *types)
	}
}

func TestToolUseKind(t *testing.T) {
	tests := []struct {
		name string
		want orchestrator.StepKind
	}{
		{"Bash", orchestrator.StepRun},
		{"Read", orchestrator.StepRead},
		{"Edit", orchestrator.StepEdit},
		{"Write", orchestrator.StepEdit},
		{"Glob", orchestrator.StepRead},
		{"Grep", orchestrator.StepRead},
		{"unknown_tool", orchestrator.StepRun},
	}
	for _, tt := range tests {
		r := orchestrator.NewStepRecorder(nil, func(string, any) {}, "r", "t")
		// Feed a tool_use and read back the published step kind via publish hook.
		var published []orchestrator.RunStep
		pub := func(_ string, data any) {
			if s, ok := data.(orchestrator.RunStep); ok {
				published = append(published, s)
			}
		}
		r2 := orchestrator.NewStepRecorder(nil, pub, "r", "t")
		r2.Feed(orchestrator.RecDelta{Kind: orchestrator.RecKindToolUse, ToolName: tt.name, ToolID: "x"})
		r2.Close()
		if len(published) == 0 {
			t.Errorf("toolUseKind(%q): no step published", tt.name)
			continue
		}
		if published[0].Kind != tt.want {
			t.Errorf("toolUseKind(%q) = %q, want %q", tt.name, published[0].Kind, tt.want)
		}
		_ = r
	}
}
