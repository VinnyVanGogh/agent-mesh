package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
)

// QueueRunner executes queued tasks sequentially according to priority, paced by 5-hour rate limits.
type QueueRunner struct {
	DB         *sql.DB
	RepoRoot   string
	Harness    *Harness
	Queue      *QueueManager
	Pacer      *ExecutionPacer
	Safeguards *SafeguardsManager
	SleepFunc  func(d time.Duration)
}

// QueueRunOptions configures a sequential queue execution run.
type RunQueueOptions struct {
	MaxTasks    int           // 0 = run until queue empty or blocked
	Provider    string        // optional provider override
	RunConfig   RunConfig     // base harness configuration
	DryRun      bool          // if true, evaluates pacing and priority without executing adapter
	MaxDelay    time.Duration // maximum sleep allowed for pacing before yielding
	OnTaskStart func(task *QueuedTask, assessment *PacingAssessment)
	OnTaskDone  func(task *QueuedTask, res *RunResult)
}

// NewQueueRunner initializes a complete intelligent priority queue and execution pacer runner.
func NewQueueRunner(dbConn *sql.DB, repoRoot string, appCfg *config.Config) *QueueRunner {
	harness := NewHarness(dbConn, repoRoot)
	queue := NewQueueManager(dbConn)
	pacer := NewExecutionPacer(dbConn, DefaultPacerConfig(), appCfg)
	safeguards := NewSafeguardsManager(dbConn, appCfg)

	return &QueueRunner{
		DB:         dbConn,
		RepoRoot:   repoRoot,
		Harness:    harness,
		Queue:      queue,
		Pacer:      pacer,
		Safeguards: safeguards,
		SleepFunc:  time.Sleep,
	}
}

func (qr *QueueRunner) sleep(d time.Duration) {
	if qr.SleepFunc != nil {
		qr.SleepFunc(d)
	} else {
		time.Sleep(d)
	}
}

// RunNext finds and executes the single highest-priority runnable task in the queue.
func (qr *QueueRunner) RunNext(ctx context.Context, opts RunQueueOptions) (*RunResult, error) {
	task, err := qr.Queue.NextRunnableTask(ctx)
	if err != nil {
		return nil, fmt.Errorf("queue next task: %w", err)
	}
	if task == nil {
		return nil, nil // No runnable tasks
	}

	provider := opts.Provider
	if provider == "" {
		if opts.RunConfig.Provider != "" {
			provider = opts.RunConfig.Provider
		} else {
			provider = "claude"
		}
	}

	// 1. Evaluate Anti-Overuse Safeguards
	safeguard, err := qr.Safeguards.CheckSafeguards(ctx, task, provider)
	if err != nil {
		return nil, fmt.Errorf("check safeguards for %s: %w", task.ID, err)
	}
	if !safeguard.Allowed {
		slog.Warn("safeguard prevented task execution",
			slog.String("task", task.ID),
			slog.String("trip_type", safeguard.TripType),
			slog.String("reason", safeguard.Reason),
		)
		if safeguard.ShouldCap {
			_ = qr.Safeguards.EnforceSafeguardCap(ctx, task.ID, safeguard.Reason)
		}
		return &RunResult{
			TaskID:        task.ID,
			Disposition:   "capped",
			DiagnosticMsg: safeguard.Reason,
		}, nil
	}

	// 2. Evaluate 5-Hour Rate Limit Pacing
	assessment, err := qr.Pacer.EvaluatePacing(ctx, provider, task.Priority)
	if err != nil {
		return nil, fmt.Errorf("evaluate pacing for %s: %w", task.ID, err)
	}

	if !assessment.CanExecute {
		slog.Warn("execution pacer deferred task to protect 5-hour quota",
			slog.String("task", task.ID),
			slog.String("priority", task.Priority),
			slog.String("provider", provider),
			slog.String("reason", assessment.Reason),
		)
		// Return deferred result without modifying database state
		return &RunResult{
			TaskID:        task.ID,
			Disposition:   "in_progress",
			DiagnosticMsg: assessment.Reason,
		}, nil
	}

	// 3. Apply Dynamic Pacing Delay (Throttling)
	if assessment.PacingDelay > 0 {
		slog.Info("applying rate-limit pacing delay",
			slog.String("task", task.ID),
			slog.Duration("delay", assessment.PacingDelay),
		)
		if opts.MaxDelay > 0 && assessment.PacingDelay > opts.MaxDelay {
			assessment.PacingDelay = opts.MaxDelay
		}
		qr.sleep(assessment.PacingDelay)
	}

	if opts.OnTaskStart != nil {
		opts.OnTaskStart(task, assessment)
	}

	if opts.DryRun {
		return &RunResult{
			TaskID:      task.ID,
			Disposition: "todo",
			Turns:       0,
			SpentUSD:    0.0,
		}, nil
	}

	// 4. Sequential Execution through Harness
	runCfg := opts.RunConfig
	runCfg.Provider = provider
	if runCfg.MaxTurns <= 0 && task.MaxTurns > 0 {
		runCfg.MaxTurns = task.MaxTurns
	}
	if runCfg.MaxBudgetUSD <= 0 && task.MaxBudgetUSD > 0 {
		runCfg.MaxBudgetUSD = task.MaxBudgetUSD
	}

	result, err := qr.Harness.Run(ctx, task.ID, runCfg)
	if err != nil {
		return nil, fmt.Errorf("execute task %s: %w", task.ID, err)
	}

	// 5. Record Usage in Safeguards Manager
	qr.Safeguards.RecordRun(provider, result.Turns, result.SpentUSD)

	if opts.OnTaskDone != nil {
		opts.OnTaskDone(task, result)
	}

	return result, nil
}

// RunQueue sequentially executes tasks in priority order until queue is empty or MaxTasks reached.
func (qr *QueueRunner) RunQueue(ctx context.Context, opts RunQueueOptions) ([]*RunResult, error) {
	var results []*RunResult
	count := 0

	for {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		if opts.MaxTasks > 0 && count >= opts.MaxTasks {
			break
		}

		res, err := qr.RunNext(ctx, opts)
		if err != nil {
			return results, err
		}
		if res == nil {
			// No more runnable tasks
			break
		}

		// If a task was deferred due to pacing lockout, stop drain loop
		if res.Disposition == "in_progress" && res.Turns == 0 {
			results = append(results, res)
			break
		}

		results = append(results, res)
		count++
	}

	return results, nil
}
