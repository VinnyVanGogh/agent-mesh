package orchestrator

import (
	"container/heap"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Priority levels for tasks.
const (
	PriorityCritical = "critical"
	PriorityUrgent   = "urgent"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
)

// NormalizePriority standardizes priority strings to known canonical values.
func NormalizePriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "critical":
		return PriorityCritical
	case "urgent":
		return PriorityUrgent
	case "high":
		return PriorityHigh
	case "low":
		return PriorityLow
	case "medium", "normal", "standard":
		return PriorityMedium
	default:
		return PriorityMedium
	}
}

// PriorityWeight returns a numerical rank for comparison. Higher weight = higher priority.
func PriorityWeight(p string) int {
	switch NormalizePriority(p) {
	case PriorityCritical:
		return 400
	case PriorityUrgent:
		return 300
	case PriorityHigh:
		return 200
	case PriorityMedium:
		return 100
	case PriorityLow:
		return 0
	default:
		return 100
	}
}

// QueuedTask is a task record prepared for priority queue dispatch.
type QueuedTask struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	RepoPath        string    `json:"repo_path"`
	GitBranch       string    `json:"git_branch"`
	Status          string    `json:"status"`
	AccountRole     string    `json:"account_role"`
	Priority        string    `json:"priority"`
	PriorityWeight  int       `json:"priority_weight"`
	MaxBudgetUSD    float64   `json:"max_budget_usd"`
	MaxTurns        int       `json:"max_turns"`
	SpentTokens     int64     `json:"spent_tokens"`
	SpentUSD        float64   `json:"spent_usd"`
	SpentTurns      int       `json:"spent_turns"`
	Organization    string    `json:"organization,omitempty"`
	Project         string    `json:"project,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	ExecutionStage  string    `json:"execution_stage"`
	CheckoutRunID   string    `json:"checkout_run_id,omitempty"`
	CheckoutAgentID string    `json:"checkout_agent_id,omitempty"`
	IsBlocked       bool      `json:"is_blocked"`
	BlockReason     string    `json:"block_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Index           int       `json:"index"` // internal heap index
}

// TaskPriorityQueue implements heap.Interface for in-memory priority queueing.
type TaskPriorityQueue []*QueuedTask

func (pq TaskPriorityQueue) Len() int { return len(pq) }

func (pq TaskPriorityQueue) Less(i, j int) bool {
	// Higher PriorityWeight wins.
	if pq[i].PriorityWeight != pq[j].PriorityWeight {
		return pq[i].PriorityWeight > pq[j].PriorityWeight
	}
	// If priority is equal, earlier CreatedAt wins (FIFO).
	return pq[i].CreatedAt.Before(pq[j].CreatedAt)
}

func (pq TaskPriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *TaskPriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*QueuedTask)
	item.Index = n
	*pq = append(*pq, item)
}

func (pq *TaskPriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	item.Index = -1
	*pq = old[0 : n-1]
	return item
}

// Peek returns the highest-priority item without removing it.
func (pq TaskPriorityQueue) Peek() *QueuedTask {
	if len(pq) == 0 {
		return nil
	}
	return pq[0]
}

// PopTask safely pops the highest-priority item using container/heap.
func (pq *TaskPriorityQueue) PopTask() *QueuedTask {
	if len(*pq) == 0 {
		return nil
	}
	return heap.Pop(pq).(*QueuedTask)
}

// PushTask safely pushes an item maintaining heap property using container/heap.
func (pq *TaskPriorityQueue) PushTask(item *QueuedTask) {
	heap.Push(pq, item)
}

// QueueSummary provides an overview of the tasks in queue.
type QueueSummary struct {
	TotalPending int            `json:"total_pending"`
	ByPriority   map[string]int `json:"by_priority"`
	BlockedCount int            `json:"blocked_count"`
	ActiveClaims int            `json:"active_claims"`
	NextTask     *QueuedTask    `json:"next_task,omitempty"`
}

// QueueManager coordinates database querying, dependency checks, and priority queue ordering.
type QueueManager struct {
	mu sync.Mutex
	DB *sql.DB
}

// NewQueueManager creates a QueueManager backed by the database.
func NewQueueManager(dbConn *sql.DB) *QueueManager {
	return &QueueManager{
		DB: dbConn,
	}
}

// FetchRunnableTasks queries and returns all runnable tasks in strict priority order.
// A task is runnable if:
// 1. status = 'active'
// 2. execution_stage in ('todo')
// 3. is_blocked = 0
// 4. has no active blocking dependencies in task_relations
func (qm *QueueManager) FetchRunnableTasks(ctx context.Context) ([]*QueuedTask, error) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	query := `
		SELECT id, name, repo_path, git_branch, status, account_role,
		       COALESCE(priority, 'medium') as priority,
		       max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
		       COALESCE(organization, ''), COALESCE(project, ''), COALESCE(parent_id, ''),
		       execution_stage, COALESCE(checkout_run_id, ''), COALESCE(checkout_agent_id, ''),
		       is_blocked, COALESCE(block_reason, ''), created_at, updated_at
		FROM tasks
		WHERE status = 'active'
		  AND execution_stage = 'todo'
		  AND is_blocked = 0
		  AND id NOT IN (
		      SELECT blocks_id FROM task_relations r
		      JOIN tasks t ON r.task_id = t.id
		      WHERE t.status != 'done' AND t.status != 'soft_deleted'
		  )
		ORDER BY
		  CASE LOWER(COALESCE(priority, 'medium'))
		    WHEN 'critical' THEN 4
		    WHEN 'urgent' THEN 3
		    WHEN 'high' THEN 3
		    WHEN 'medium' THEN 2
		    WHEN 'low' THEN 1
		    ELSE 2
		  END DESC,
		  created_at ASC
	`

	rows, err := qm.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fetch runnable tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*QueuedTask
	for rows.Next() {
		t := &QueuedTask{}
		var createdAtStr, updatedAtStr string
		if err := rows.Scan(
			&t.ID, &t.Name, &t.RepoPath, &t.GitBranch, &t.Status, &t.AccountRole,
			&t.Priority, &t.MaxBudgetUSD, &t.MaxTurns, &t.SpentTokens, &t.SpentUSD, &t.SpentTurns,
			&t.Organization, &t.Project, &t.ParentID, &t.ExecutionStage,
			&t.CheckoutRunID, &t.CheckoutAgentID, &t.IsBlocked, &t.BlockReason,
			&createdAtStr, &updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan runnable task: %w", err)
		}

		t.Priority = NormalizePriority(t.Priority)
		t.PriorityWeight = PriorityWeight(t.Priority)
		t.CreatedAt = parseDBTime(createdAtStr)
		t.UpdatedAt = parseDBTime(updatedAtStr)

		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

// NextRunnableTask returns the single highest-priority runnable task, or nil if none is ready.
func (qm *QueueManager) NextRunnableTask(ctx context.Context) (*QueuedTask, error) {
	tasks, err := qm.FetchRunnableTasks(ctx)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	return tasks[0], nil
}

// GetQueueSummary returns metrics about tasks currently in queue and blocked states.
func (qm *QueueManager) GetQueueSummary(ctx context.Context) (*QueueSummary, error) {
	runnable, err := qm.FetchRunnableTasks(ctx)
	if err != nil {
		return nil, err
	}

	summary := &QueueSummary{
		TotalPending: len(runnable),
		ByPriority:   make(map[string]int),
		ActiveClaims: int(activeClaims.Load()),
	}

	for _, p := range []string{PriorityCritical, PriorityUrgent, PriorityHigh, PriorityMedium, PriorityLow} {
		summary.ByPriority[p] = 0
	}

	for _, t := range runnable {
		summary.ByPriority[t.Priority]++
	}

	if len(runnable) > 0 {
		summary.NextTask = runnable[0]
	}

	// Count blocked tasks
	var blockedCount int
	err = qm.DB.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE status = 'active' AND (is_blocked = 1 OR id IN (SELECT blocks_id FROM task_relations r JOIN tasks t ON r.task_id = t.id WHERE t.status != 'done' AND t.status != 'soft_deleted'))`).Scan(&blockedCount)
	if err == nil {
		summary.BlockedCount = blockedCount
	}

	return summary, nil
}

// parseDBTime parses standard SQLite RFC3339 timestamps.
func parseDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// BuildPriorityQueue creates and initializes a TaskPriorityQueue from a slice of tasks.
func BuildPriorityQueue(tasks []*QueuedTask) *TaskPriorityQueue {
	pq := make(TaskPriorityQueue, len(tasks))
	for i, t := range tasks {
		t.Index = i
		pq[i] = t
	}
	heap.Init(&pq)
	return &pq
}
