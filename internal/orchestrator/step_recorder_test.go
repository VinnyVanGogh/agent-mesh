package orchestrator

import (
	"sync"
	"testing"
	"time"
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
	got := extractToolTitle("Bash", `{"command":"go test ./internal/server/...","description":"run tests"}`, "")
	want := "go test ./internal/server/..."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_FilePath(t *testing.T) {
	// No worktree root: path is returned as-is with verb prefix.
	got := extractToolTitle("Edit", `{"file_path":"internal/server/events.go","old_string":"x","new_string":"y"}`, "")
	want := "Edit internal/server/events.go"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_FilePathRelative(t *testing.T) {
	// With worktree root: absolute path is stripped to repo-relative.
	root := "/home/agent/.worktrees/task-abc"
	got := extractToolTitle("Read", `{"file_path":"/home/agent/.worktrees/task-abc/internal/server/events.go"}`, root)
	want := "Read internal/server/events.go"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_Fallback(t *testing.T) {
	got := extractToolTitle("Bash", "", "")
	want := "Run command"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolTitle_FallbackBadJSON(t *testing.T) {
	got := extractToolTitle("Read", "{not json}", "")
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
	// Body is set from tool result text; empty result → empty body.
	if s.Body != "" {
		t.Errorf("expected empty body for result with no text, got %q", s.Body)
	}
}

func TestStepRecorder_ToolResultSetsBody(t *testing.T) {
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
		ToolID:    "t2",
		ToolInput: `{"command":"cat /etc/hosts"}`,
	})
	r.Feed(StepDelta{
		Kind:    StepDeltaToolResult,
		ToolID:  "t2",
		Text:    "127.0.0.1 localhost\n",
		IsError: false,
	})

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected 1 run.step, got %d", len(published))
	}
	s := published[0]
	if s.Status != "done" {
		t.Errorf("expected status done, got %q", s.Status)
	}
	if s.Body != "127.0.0.1 localhost\n" {
		t.Errorf("expected output in body, got %q", s.Body)
	}
}

func TestStepRecorder_ToolResultError(t *testing.T) {
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
		ToolID:    "t3",
		ToolInput: `{"command":"cat does-not-exist.txt"}`,
	})
	r.Feed(StepDelta{
		Kind:    StepDeltaToolResult,
		ToolID:  "t3",
		Text:    "cat: does-not-exist.txt: No such file or directory\n",
		IsError: true,
	})

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected 1 run.step, got %d", len(published))
	}
	s := published[0]
	if s.Status != "error" {
		t.Errorf("expected status error, got %q", s.Status)
	}
	if s.Body != "cat: does-not-exist.txt: No such file or directory\n" {
		t.Errorf("unexpected body: %q", s.Body)
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

// TestStepRecorder_WakeStartedAtIsOwnTime verifies that EmitWake records its own
// start time, not r.startedAt (the run creation time). This ensures the wake step's
// StartedAt is not anchored to a moment before the step actually executed.
func TestStepRecorder_WakeStartedAtIsOwnTime(t *testing.T) {
	var mu sync.Mutex
	var steps []RunStep
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				steps = append(steps, s)
				mu.Unlock()
			}
		}
	}

	before := time.Now().UTC()
	r := NewStepRecorder(nil, pub, "run1", "task1")
	// Pause briefly so run creation time (r.startedAt) is measurably before the wake.
	time.Sleep(2 * time.Millisecond)
	r.EmitWake("issue_assigned")
	after := time.Now().UTC()

	mu.Lock()
	defer mu.Unlock()
	if len(steps) == 0 {
		t.Fatal("no run.step published")
	}
	s := steps[0]
	if s.StartedAt == "" {
		t.Fatal("StartedAt must not be empty")
	}
	ts, err := time.Parse(time.RFC3339Nano, s.StartedAt)
	if err != nil {
		t.Fatalf("StartedAt %q not parseable: %v", s.StartedAt, err)
	}
	// The step's StartedAt must be within [before, after], not at the recorder's creation.
	if ts.Before(before) || ts.After(after) {
		t.Errorf("StartedAt %v not between %v and %v (wake should use its own time, not r.startedAt)", ts, before, after)
	}
}

// TestStepRecorder_EmptyThinkBlockDropped verifies that a thinking delta with empty
// text does not produce a persisted step (STA-460).
func TestStepRecorder_EmptyThinkBlockDropped(t *testing.T) {
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
	// Empty thinking block — simulates a redacted/empty thinking block from Claude.
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: ""})
	// A subsequent tool_use should close the (non-existent) think step.
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "t1"})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t1"})
	r.Close()

	mu.Lock()
	defer mu.Unlock()
	for _, s := range published {
		if s.Kind == StepThink {
			t.Errorf("expected no think step, but got one with body=%q", s.Body)
		}
	}
}

// TestStepRecorder_EmptyThinkThenRealThink verifies that an empty thinking block
// followed by a real thinking block produces exactly one non-empty think step.
func TestStepRecorder_EmptyThinkThenRealThink(t *testing.T) {
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
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: ""})
	r.Feed(StepDelta{Kind: StepDeltaThinking, Text: "actual thinking"})
	r.Close()

	mu.Lock()
	defer mu.Unlock()
	thinkSteps := 0
	for _, s := range published {
		if s.Kind == StepThink {
			thinkSteps++
			if s.Body == "" {
				t.Error("think step has empty body")
			}
			if s.Body != "actual thinking" {
				t.Errorf("think body = %q, want %q", s.Body, "actual thinking")
			}
		}
	}
	if thinkSteps != 1 {
		t.Errorf("expected 1 think step, got %d", thinkSteps)
	}
}

// TestStepRecorder_CloseDropsEmptyThink verifies the defense-in-depth path:
// a think step that somehow ends up with an empty body is not persisted.
func TestStepRecorder_CloseDropsEmptyThink(t *testing.T) {
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
	// Directly inject an empty think step to hit the closePendingWithStatusLocked guard.
	r.mu.Lock()
	r.seq++
	r.pending = &openStep{
		seq:       r.seq,
		kind:      StepThink,
		title:     "Thinking",
		startedAt: time.Now().UTC(),
	}
	r.mu.Unlock()
	r.Close()

	mu.Lock()
	defer mu.Unlock()
	for _, s := range published {
		if s.Kind == StepThink {
			t.Errorf("expected empty think step to be dropped, but it was persisted with body=%q", s.Body)
		}
	}
}

// TestStepRecorder_RouteStartedAtIsOwnTime verifies the same property for EmitRoute.
func TestStepRecorder_RouteStartedAtIsOwnTime(t *testing.T) {
	var mu sync.Mutex
	var steps []RunStep
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				steps = append(steps, s)
				mu.Unlock()
			}
		}
	}

	before := time.Now().UTC()
	r := NewStepRecorder(nil, pub, "run1", "task1")
	time.Sleep(2 * time.Millisecond)
	r.EmitRoute("Ran on Claude Opus", "")
	after := time.Now().UTC()

	mu.Lock()
	defer mu.Unlock()
	if len(steps) == 0 {
		t.Fatal("no run.step published")
	}
	s := steps[0]
	if s.StartedAt == "" {
		t.Fatal("StartedAt must not be empty")
	}
	ts, err := time.Parse(time.RFC3339Nano, s.StartedAt)
	if err != nil {
		t.Fatalf("StartedAt %q not parseable: %v", s.StartedAt, err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("StartedAt %v not between %v and %v", ts, before, after)
	}
}

// collectSteps returns a PublishFunc and a pointer to the slice of RunStep events.
func collectSteps(t *testing.T) (PublishFunc, *[]RunStep) {
	t.Helper()
	var mu sync.Mutex
	var steps []RunStep
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				steps = append(steps, s)
				mu.Unlock()
			}
		}
	}
	return pub, &steps
}

// TestStepRecorder_ParallelToolCallsInOrder sends two tool_use blocks followed by
// their tool_results in the same order and verifies each step gets its own real output.
func TestStepRecorder_ParallelToolCallsInOrder(t *testing.T) {
	pub, steps := collectSteps(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")

	// Two parallel tool_use blocks (as Claude sends them in one assistant message).
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "t1",
		ToolInput: `{"command":"echo hello","description":"print hello"}`})
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "t2",
		ToolInput: `{"command":"sleep 20","description":"long sleep"}`})

	// Results arrive in the same order.
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t1", Text: "hello\n"})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "t2", Text: "slept-again\n"})
	r.Close()

	got := *steps
	if len(got) != 2 {
		t.Fatalf("expected 2 run.step events, got %d: %+v", len(got), got)
	}

	byID := map[string]RunStep{}
	for _, s := range got {
		byID[s.Title] = s
	}

	s1 := got[0]
	if s1.Body != "hello\n" {
		t.Errorf("step 1 body = %q, want %q", s1.Body, "hello\n")
	}
	if s1.Status != "done" {
		t.Errorf("step 1 status = %q, want done", s1.Status)
	}

	s2 := got[1]
	if s2.Body != "slept-again\n" {
		t.Errorf("step 2 body = %q, want %q", s2.Body, "slept-again\n")
	}
	if s2.Status != "done" {
		t.Errorf("step 2 status = %q, want done", s2.Status)
	}
}

// TestStepRecorder_ParallelToolCallsReverseOrder sends results in reverse order
// (second result arrives before first) and verifies correct matching by ID.
func TestStepRecorder_ParallelToolCallsReverseOrder(t *testing.T) {
	pub, steps := collectSteps(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Read", ToolID: "ta",
		ToolInput: `{"file_path":"a.txt"}`})
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Read", ToolID: "tb",
		ToolInput: `{"file_path":"b.txt"}`})

	// Results arrive in reverse order.
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "tb", Text: "content-b"})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "ta", Text: "content-a"})
	r.Close()

	got := *steps
	if len(got) != 2 {
		t.Fatalf("expected 2 run.step events, got %d: %+v", len(got), got)
	}

	byTitle := map[string]RunStep{}
	for _, s := range got {
		byTitle[s.Title] = s
	}

	if sa, ok := byTitle["Read a.txt"]; ok {
		if sa.Body != "content-a" {
			t.Errorf("step a body = %q, want content-a", sa.Body)
		}
	} else {
		t.Errorf("no step with title %q; titles: %v", "Read a.txt", titlesOf(got))
	}

	if sb, ok := byTitle["Read b.txt"]; ok {
		if sb.Body != "content-b" {
			t.Errorf("step b body = %q, want content-b", sb.Body)
		}
	} else {
		t.Errorf("no step with title %q; titles: %v", "Read b.txt", titlesOf(got))
	}
}

// TestStepRecorder_ParallelToolCallDuration verifies that each parallel step's
// duration is measured from its own tool_use, not from the other tool's result.
// Specifically: a step whose result arrives after a real delay should show that delay,
// not a near-zero 294ms (the bug reported in STA-501).
func TestStepRecorder_ParallelToolCallDuration(t *testing.T) {
	var mu sync.Mutex
	type entry struct {
		step RunStep
		seen time.Time
	}
	var entries []entry
	pub := func(eventType string, data any) {
		if eventType == "run.step" {
			if s, ok := data.(RunStep); ok {
				mu.Lock()
				entries = append(entries, entry{step: s, seen: time.Now().UTC()})
				mu.Unlock()
			}
		}
	}
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "fast",
		ToolInput: `{"command":"echo quick","description":"quick"}`})
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "slow",
		ToolInput: `{"command":"sleep 0.05","description":"slow"}`})

	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "fast", Text: "quick\n"})
	time.Sleep(50 * time.Millisecond)
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "slow", Text: "slept\n"})
	r.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(entries) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(entries))
	}

	for _, e := range entries {
		startedAt, err := time.Parse(time.RFC3339Nano, e.step.StartedAt)
		if err != nil {
			t.Fatalf("StartedAt parse: %v", err)
		}
		endedAt, err := time.Parse(time.RFC3339Nano, *e.step.EndedAt)
		if err != nil {
			t.Fatalf("EndedAt parse: %v", err)
		}
		dur := endedAt.Sub(startedAt)
		if e.step.Body == "slept\n" && dur < 40*time.Millisecond {
			t.Errorf("slow step duration = %v, want ≥ 40ms; was started prematurely", dur)
		}
	}
}

// TestStepRecorder_ParallelToolCallErrorStatus verifies is_error propagates correctly
// when one of two parallel tool results is an error.
func TestStepRecorder_ParallelToolCallErrorStatus(t *testing.T) {
	pub, steps := collectSteps(t)
	r := NewStepRecorder(nil, pub, "run1", "task1")

	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "ok", ToolInput: `{"command":"echo ok"}`})
	r.Feed(StepDelta{Kind: StepDeltaToolUse, ToolName: "Bash", ToolID: "fail", ToolInput: `{"command":"false"}`})

	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "ok", Text: "ok\n", IsError: false})
	r.Feed(StepDelta{Kind: StepDeltaToolResult, ToolID: "fail", Text: "exit 1\n", IsError: true})
	r.Close()

	got := *steps
	if len(got) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(got))
	}
	statusByBody := map[string]string{}
	for _, s := range got {
		statusByBody[s.Body] = s.Status
	}
	if statusByBody["ok\n"] != "done" {
		t.Errorf("ok step status = %q, want done", statusByBody["ok\n"])
	}
	if statusByBody["exit 1\n"] != "error" {
		t.Errorf("fail step status = %q, want error", statusByBody["exit 1\n"])
	}
}

func titlesOf(steps []RunStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Title
	}
	return out
}

