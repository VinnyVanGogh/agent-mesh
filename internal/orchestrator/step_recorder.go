package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// StepKind classifies a run timeline step for the board UI.
type StepKind string

const (
	StepWake       StepKind = "wake"
	StepThink      StepKind = "think"
	StepRead       StepKind = "read"
	StepRun        StepKind = "run"
	StepEdit       StepKind = "edit"
	StepCheckpoint StepKind = "checkpoint"
	StepMessage    StepKind = "message"
	StepState      StepKind = "state"
	StepStats      StepKind = "stats"
)

// RecDeltaKind mirrors adapter.DeltaKind without importing the adapter package
// (which would form an import cycle via adapter → router → context → orchestrator).
type RecDeltaKind string

const (
	RecKindThinking   RecDeltaKind = "thinking"
	RecKindToolUse    RecDeltaKind = "tool_use"
	RecKindToolResult RecDeltaKind = "tool_result"
	RecKindText       RecDeltaKind = "text"
	RecKindUsage      RecDeltaKind = "usage"
	RecKindResult     RecDeltaKind = "result"
)

// RecUsage holds normalized token counts for a delta usage event.
type RecUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// RecDelta is the provider-neutral event the harness feeds into StepRecorder.
// Callers that hold an adapter.ProviderAdapter translate adapter.StreamDelta → RecDelta
// at the call site, keeping the import cycle out of this package.
type RecDelta struct {
	Kind     RecDeltaKind
	Text     string
	ToolName string
	ToolID   string
	IsError  bool
	Usage    *RecUsage
}

// RunStep is one timeline entry, stored in run_steps and broadcast as run.step SSE.
type RunStep struct {
	ID        string   `json:"id"`
	RunID     string   `json:"run_id"`
	TaskID    string   `json:"task_id"`
	Seq       int      `json:"seq"`
	ParentSeq *int     `json:"parent_seq,omitempty"`
	Kind      StepKind `json:"kind"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Status    string   `json:"status"` // running | done | error (SSE only; not persisted)
	StartedAt string   `json:"started_at"`
	EndedAt   *string  `json:"ended_at,omitempty"`
}

// RunStats is broadcast as run.stats SSE.
type RunStats struct {
	RunID        string  `json:"run_id"`
	TaskID       string  `json:"task_id"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	SpentUSD     float64 `json:"spent_usd,omitempty"`
	ElapsedSec   float64 `json:"elapsed_sec"`
}

// PublishFunc is the EventHub.Publish signature subset used by StepRecorder.
type PublishFunc func(eventType string, data any)

// StepRecorder groups RecDeltas into steps, persists them, and publishes SSE events.
// It is safe for concurrent use.
type StepRecorder struct {
	db        *sql.DB
	publish   PublishFunc
	runID     string
	taskID    string
	startedAt time.Time

	mu      sync.Mutex
	seq     int
	pending *openStep // step being assembled
}

// openStep is a step that has been started but not yet closed.
type openStep struct {
	seq       int
	kind      StepKind
	title     string
	body      strings.Builder
	startedAt time.Time
	toolID    string // for matching tool_use → tool_result
}

// NewStepRecorder creates a StepRecorder for the given run.
func NewStepRecorder(db *sql.DB, publish PublishFunc, runID, taskID string) *StepRecorder {
	return &StepRecorder{
		db:        db,
		publish:   publish,
		runID:     runID,
		taskID:    taskID,
		startedAt: time.Now().UTC(),
	}
}

// EmitWake emits a wake step describing why the run started.
func (r *StepRecorder) EmitWake(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCurrentLocked()
	r.seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	step := RunStep{
		RunID:     r.runID,
		TaskID:    r.taskID,
		Seq:       r.seq,
		Kind:      StepWake,
		Title:     "Woke up",
		Body:      reason,
		Status:    "done",
		StartedAt: r.startedAt.Format(time.RFC3339Nano),
		EndedAt:   &now,
	}
	r.persist(step)
	r.publish("run.step", step)
}

// EmitCheckpoint emits a checkpoint step.
func (r *StepRecorder) EmitCheckpoint(sha, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCurrentLocked()
	r.seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	step := RunStep{
		RunID:     r.runID,
		TaskID:    r.taskID,
		Seq:       r.seq,
		Kind:      StepCheckpoint,
		Title:     "Checkpoint",
		Body:      "sha=" + sha + " " + msg,
		Status:    "done",
		StartedAt: now,
		EndedAt:   &now,
	}
	r.persist(step)
	r.publish("run.step", step)
}

// EmitState emits a state step (done / in_review / blocked / stopped) and run.state SSE.
func (r *StepRecorder) EmitState(disposition string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCurrentLocked()
	r.seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	elapsed := time.Since(r.startedAt).Seconds()
	step := RunStep{
		RunID:     r.runID,
		TaskID:    r.taskID,
		Seq:       r.seq,
		Kind:      StepState,
		Title:     "Finished: " + disposition,
		Status:    "done",
		StartedAt: now,
		EndedAt:   &now,
	}
	r.persist(step)
	r.publish("run.step", step)
	r.publish("run.state", map[string]any{
		"run_id":      r.runID,
		"task_id":     r.taskID,
		"disposition": disposition,
		"elapsed_sec": elapsed,
	})
}

// Feed processes one RecDelta, updating the current open step.
func (r *StepRecorder) Feed(d RecDelta) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch d.Kind {
	case RecKindThinking:
		r.openOrReuseThinkLocked(d)

	case RecKindToolUse:
		r.closeCurrentLocked()
		r.seq++
		r.pending = &openStep{
			seq:       r.seq,
			kind:      toolUseKind(d.ToolName),
			title:     toolUseTitle(d.ToolName),
			startedAt: time.Now().UTC(),
			toolID:    d.ToolID,
		}

	case RecKindToolResult:
		// tool_use + tool_result pair counts as one step: close the pending tool_use.
		if r.pending != nil && r.pending.toolID == d.ToolID {
			if d.IsError {
				r.pending.body.WriteString("[error]")
				r.closePendingLocked("error")
			} else {
				r.closePendingLocked("done")
			}
		} else {
			r.closeCurrentLocked()
		}

	case RecKindText:
		r.accumulateTextLocked(d.Text)

	case RecKindUsage:
		if d.Usage != nil {
			r.publish("run.stats", RunStats{
				RunID:        r.runID,
				TaskID:       r.taskID,
				InputTokens:  d.Usage.InputTokens,
				OutputTokens: d.Usage.OutputTokens,
				ElapsedSec:   time.Since(r.startedAt).Seconds(),
			})
		}

	case RecKindResult:
		r.closeCurrentLocked()
	}
}

// Close flushes any pending open step as "done".
func (r *StepRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCurrentLocked()
}

// --- locked helpers (must be called with r.mu held) ---

func (r *StepRecorder) openOrReuseThinkLocked(d RecDelta) {
	if r.pending != nil && r.pending.kind == StepThink {
		r.pending.body.WriteString(d.Text)
		return
	}
	r.closeCurrentLocked()
	r.seq++
	r.pending = &openStep{
		seq:       r.seq,
		kind:      StepThink,
		title:     "Thinking",
		startedAt: time.Now().UTC(),
	}
	r.pending.body.WriteString(d.Text)
}

func (r *StepRecorder) accumulateTextLocked(text string) {
	if r.pending == nil {
		return
	}
	r.pending.body.WriteString(text)
}

func (r *StepRecorder) closePendingLocked(forceStatus string) {
	if r.pending == nil {
		return
	}
	p := r.pending
	r.pending = nil
	now := time.Now().UTC().Format(time.RFC3339Nano)
	status := "done"
	if forceStatus != "" {
		status = forceStatus
	}
	step := RunStep{
		RunID:     r.runID,
		TaskID:    r.taskID,
		Seq:       p.seq,
		Kind:      p.kind,
		Title:     p.title,
		Body:      p.body.String(),
		Status:    status,
		StartedAt: p.startedAt.Format(time.RFC3339Nano),
		EndedAt:   &now,
	}
	r.persist(step)
	r.publish("run.step", step)
}

func (r *StepRecorder) closeCurrentLocked() {
	r.closePendingLocked("done")
}

func (r *StepRecorder) persist(step RunStep) {
	if r.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id := uuid.New().String()
	var parentSeqNull sql.NullInt64
	if step.ParentSeq != nil {
		parentSeqNull = sql.NullInt64{Int64: int64(*step.ParentSeq), Valid: true}
	}
	var endedAtNull sql.NullString
	if step.EndedAt != nil {
		endedAtNull = sql.NullString{String: *step.EndedAt, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO run_steps (id, run_id, task_id, seq, parent_seq, kind, title, body, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, step.RunID, step.TaskID, step.Seq, parentSeqNull,
		string(step.Kind), step.Title, step.Body,
		step.StartedAt, endedAtNull,
	)
	if err != nil {
		slog.Warn("run_steps insert failed", slog.Any("err", err))
		return
	}
	step.ID = id
}

// toolUseKind maps a tool name to a StepKind.
func toolUseKind(name string) StepKind {
	name = strings.ToLower(name)
	switch {
	case name == "bash" || name == "computer" || strings.Contains(name, "execute"):
		return StepRun
	case strings.Contains(name, "edit") || strings.Contains(name, "write") || strings.Contains(name, "create"):
		return StepEdit
	case strings.Contains(name, "read") || strings.Contains(name, "view") || strings.Contains(name, "glob") || strings.Contains(name, "grep") || strings.Contains(name, "list") || strings.Contains(name, "search"):
		return StepRead
	case strings.Contains(name, "checkpoint"):
		return StepCheckpoint
	default:
		return StepRun
	}
}

// toolUseTitle returns a human-readable title for a tool_use delta.
func toolUseTitle(name string) string {
	switch strings.ToLower(name) {
	case "bash":
		return "Run command"
	case "read", "readfile":
		return "Read file"
	case "edit", "multiedit":
		return "Edit file"
	case "write", "writefile":
		return "Write file"
	case "glob":
		return "Glob files"
	case "grep":
		return "Search codebase"
	default:
		if name == "" {
			return "Tool call"
		}
		return name
	}
}

// MarshalJSON for RunStep — used by SSE publish and tests.
func (s RunStep) MarshalJSON() ([]byte, error) {
	type Alias RunStep
	return json.Marshal(Alias(s))
}
