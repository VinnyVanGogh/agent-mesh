package orchestrator

import (
	"sync"
	"testing"
)

func collectPublished(t *testing.T) (PublishFunc, *[]string) {
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
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: "step 1"})
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: " more"})
	// Close thinking by sending a tool_use
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "t1"})
	r.Close()

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
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Read", ToolID: "t1"})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t1"})

	// tool_use+tool_result should publish exactly one run.step
	stepCount := 0
	for _, tp := range *types {
		if tp == "run.step" {
			stepCount++
		}
	}
	if stepCount != 1 {
		t.Errorf("expected 1 run.step event, got %d (events: %v)", stepCount, *types)
	}
}

func TestStepRecorder_UsagePublishesStats(t *testing.T) {
	pub, types := collectPublished(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaUsage, Usage: &StepUsage{
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
		t.Error("expected run.stats event from StepDeltaUsage")
	}
}

func TestStepRecorder_WakeEmitsDone(t *testing.T) {
	pub, types := collectPublished(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")
	r.EmitWake("issue_assigned")

	if len(*types) == 0 || (*types)[0] != "run.step" {
		t.Errorf("expected run.step from EmitWake, got %v", *types)
	}
}

func TestStepRecorder_StateEmitsBoth(t *testing.T) {
	pub, types := collectPublished(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")
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
		want StepKind
	}{
		{"Bash", StepRun},
		{"Read", StepRead},
		{"Edit", StepEdit},
		{"Write", StepEdit},
		{"Glob", StepRead},
		{"Grep", StepRead},
		{"unknown_tool", StepRun},
	}
	for _, tt := range tests {
		got := toolUseKind(tt.name)
		if got != tt.want {
			t.Errorf("toolUseKind(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestStepRecorder_MultipleThinkMerged(t *testing.T) {
	pub, types := collectPublished(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")

	// Multiple thinking deltas should produce ONE think step
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: "part1"})
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: "part2"})
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: "part3"})
	r.Close()

	stepCount := 0
	for _, tp := range *types {
		if tp == "run.step" {
			stepCount++
		}
	}
	if stepCount != 1 {
		t.Errorf("expected 1 merged think step, got %d", stepCount)
	}
}

func TestStepRecorder_ErrorToolResult(t *testing.T) {
	pub, types := collectPublished(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "t2"})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t2", IsError: true})

	// Should still produce one run.step, status error
	stepCount := 0
	for _, tp := range *types {
		if tp == "run.step" {
			stepCount++
		}
	}
	if stepCount != 1 {
		t.Errorf("expected 1 run.step for error result, got %d", stepCount)
	}
}

func TestExtractToolTitle_Command(t *testing.T) {
	got := extractToolTitle("Bash", `{"command":"go test ./internal/server/...","description":"run tests"}`)
	want := "go test ./internal/server/..."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_FilePath(t *testing.T) {
	got := extractToolTitle("Edit", `{"file_path":"internal/server/events.go","old_string":"x","new_string":"y"}`)
	want := "internal/server/events.go"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_Fallback(t *testing.T) {
	got := extractToolTitle("Bash", "")
	want := "Run command"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_FallbackBadJSON(t *testing.T) {
	got := extractToolTitle("Read", "{not json}")
	want := "Read file"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStepRecorder_ToolInputInTitle(t *testing.T) {
	var published []RunStep
	var mu sync.Mutex
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				published = append(published, s)
				mu.Unlock()
			}
		}
	}
	r := NewStepRecorder(nil, pub, "run1", "task1")
	r.Feed(StepDelta{
		Kind:      StepDeltaToolUse,
		ToolName:  "Bash",
		ToolID:    "t1",
		ToolInput: `{"command":"go test ./internal/server/...","description":"run tests"}`,
	})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t1"})

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected 1 run.step, got %d", len(published))
	}
	s := published[0]
	if s.Title != "go test ./internal/server/..." {
		t.Errorf("expected command as title, got %q", s.Title)
	}
	if s.Body == "" {
		t.Error("expected non-empty body with tool input JSON")
	}
}

func TestStepRecorder_TextBetweenToolsOpensThink(t *testing.T) {
	var published []RunStep
	var mu sync.Mutex
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				published = append(published, s)
				mu.Unlock()
			}
		}
	}
	r := NewStepRecorder(nil, pub, "run1", "task1")
	// Complete one tool pair
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "t1"})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t1"})
	// Text arrives with no pending step — should open a think step
	r.Feed(StepDelta{Kind: StepDeltaText, Text: "Done. Checking output now."})
	r.Close()

	mu.Lock()
	defer mu.Unlock()
	thinkFound := false
	for _, s := range published {
		if s.Kind == StepThink {
			thinkFound = true
			if s.Body != "Done. Checking output now." {
				t.Errorf("think body = %q, want %q", s.Body, "Done. Checking output now.")
			}
		}
	}
	if !thinkFound {
		t.Error("expected a think step from text between tool calls")
	}
}

func TestStepRecorder_RouteEmitsStep(t *testing.T) {
	var published []RunStep
	var mu sync.Mutex
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				published = append(published, s)
				mu.Unlock()
			}
		}
	}
	r := NewStepRecorder(nil, pub, "run1", "task1")
	r.EmitRoute("Ran on Claude Opus", "Kind of work: coding")

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected 1 run.step, got %d", len(published))
	}
	s := published[0]
	if s.Kind != StepRoute {
		t.Errorf("expected kind %q, got %q", StepRoute, s.Kind)
	}
	if s.Title != "Ran on Claude Opus" {
		t.Errorf("unexpected title: %q", s.Title)
	}
	if s.Body != "Kind of work: coding" {
		t.Errorf("unexpected body: %q", s.Body)
	}
	if s.Status != "done" {
		t.Errorf("expected status done, got %q", s.Status)
	}
}

