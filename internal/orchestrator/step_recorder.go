package orchestrator

import (
	"bytes"
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

// StepDeltaKind mirrors adapter.DeltaKind without the import cycle.
// The harness converts adapter.StreamDelta → StepDelta before feeding the recorder.
type StepDeltaKind string

const (
	StepDeltaThinking   StepDeltaKind = "thinking"
	StepDeltaToolUse    StepDeltaKind = "tool_use"
	StepDeltaToolResult StepDeltaKind = "tool_result"
	StepDeltaText       StepDeltaKind = "text"
	StepDeltaUsage      StepDeltaKind = "usage"
	StepDeltaResult     StepDeltaKind = "result"
	StepDeltaOther      StepDeltaKind = "other"
)

// StepUsage carries token accounting from a stream event.
type StepUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// StepDelta is the recorder's internal representation of one stream event.
// It mirrors adapter.StreamDelta but lives in this package to avoid the import cycle.
type StepDelta struct {
	Kind     StepDeltaKind
	Text     string
	ToolName string
	ToolID   string
	IsError  bool
	Usage    *StepUsage
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
	Status    string   `json:"status"` // running | done | error
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

// StepRecorder groups StepDeltas into steps, persists them, and publishes SSE events.
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

// EmitState emits a state step (done / in_review / blocked / stopped).
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

// Feed processes one StepDelta, updating the current open step.
func (r *StepRecorder) Feed(d StepDelta) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch d.Kind {
	case StepDeltaThinking:
		r.openOrReuseThinkLocked(d)

	case StepDeltaToolUse:
		r.closeCurrentLocked()
		r.seq++
		r.pending = &openStep{
			seq:       r.seq,
			kind:      toolUseKind(d.ToolName),
			title:     toolUseTitle(d.ToolName),
			startedAt: time.Now().UTC(),
			toolID:    d.ToolID,
		}

	case StepDeltaToolResult:
		if r.pending != nil && (r.pending.toolID == d.ToolID || d.ToolID == "") {
			status := "done"
			if d.IsError {
				r.pending.body.WriteString("[error]")
				status = "error"
			}
			r.closePendingWithStatusLocked(r.pending, status)
			return
		}
		r.closeCurrentLocked()

	case StepDeltaText:
		r.accumulateTextLocked(d.Text)

	case StepDeltaUsage:
		if d.Usage != nil {
			r.publish("run.stats", RunStats{
				RunID:        r.runID,
				TaskID:       r.taskID,
				InputTokens:  d.Usage.InputTokens,
				OutputTokens: d.Usage.OutputTokens,
				ElapsedSec:   time.Since(r.startedAt).Seconds(),
			})
		}

	case StepDeltaResult:
		r.closeCurrentLocked()
	}
}

// FeedRawLine feeds a raw provider output line. parse is injected to avoid import cycle.
// parse should be adapter.ProviderAdapter.ParseStreamDelta converted by the caller.
func (r *StepRecorder) FeedRawLine(line []byte, parse func([]byte) ([]StepDelta, error)) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	deltas, err := parse(line)
	if err != nil {
		slog.Debug("step_recorder: parse error", slog.Any("err", err))
		return
	}
	for _, d := range deltas {
		r.Feed(d)
	}
}

// Close flushes any pending open step as "done".
func (r *StepRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCurrentLocked()
}

// --- locked helpers (must be called with r.mu held) ---

func (r *StepRecorder) openOrReuseThinkLocked(d StepDelta) {
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

func (r *StepRecorder) closePendingWithStatusLocked(p *openStep, status string) {
	if p == nil {
		return
	}
	r.pending = nil
	now := time.Now().UTC().Format(time.RFC3339Nano)
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
	r.closePendingWithStatusLocked(r.pending, "done")
}

func (r *StepRecorder) persist(step RunStep) {
	if r.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if step.ID == "" {
		step.ID = uuid.New().String()
	}

	var parentSeqNull sql.NullInt64
	if step.ParentSeq != nil {
		parentSeqNull = sql.NullInt64{Int64: int64(*step.ParentSeq), Valid: true}
	}
	var endedAtNull sql.NullString
	if step.EndedAt != nil {
		endedAtNull = sql.NullString{String: *step.EndedAt, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO run_steps (id, run_id, task_id, seq, parent_seq, kind, title, body, status, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		step.ID, step.RunID, step.TaskID, step.Seq, parentSeqNull,
		string(step.Kind), step.Title, step.Body, step.Status,
		step.StartedAt, endedAtNull,
	)
	if err != nil {
		slog.Warn("run_steps insert failed", slog.Any("err", err))
	}
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
