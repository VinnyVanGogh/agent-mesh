// Command stepsim stands in for an agent run in the UI e2e suite (STA-353).
//
// The throwaway daemon started by scripts/ui-e2e.sh has no runner: pressing
// Run Now only moves the task to in_progress. To check that the run timeline
// shows steps, the suite runs this helper, which drives the real
// orchestrator.StepRecorder against the throwaway database exactly as the
// harness does during a run. If the recorder cannot persist, no steps reach
// the UI and the timeline spec fails.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
)

func main() {
	dbPath := flag.String("db", "", "throwaway SQLite database written by staypoint-apitest-server (required)")
	taskID := flag.String("task", "", "task id to record steps for (required)")
	runID := flag.String("run", "ui-e2e-run", "run id")
	flag.Parse()
	if *dbPath == "" || *taskID == "" {
		fmt.Fprintln(os.Stderr, "usage: stepsim --db PATH --task ID [--run ID]")
		os.Exit(2)
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stepsim: open db:", err)
		os.Exit(1)
	}
	defer store.Close()

	rec := orchestrator.NewStepRecorder(store.DB(), func(string, any) {}, *runID, *taskID)
	rec.EmitWake("ui-e2e: Run Now")
	rec.Feed(orchestrator.StepDelta{Kind: orchestrator.StepDeltaThinking, Text: "Reading the task description"})
	rec.Feed(orchestrator.StepDelta{Kind: orchestrator.StepDeltaToolUse, ToolName: "Read", ToolID: "t1", Text: "README.md"})
	rec.Feed(orchestrator.StepDelta{Kind: orchestrator.StepDeltaToolResult, ToolID: "t1", Text: "ok"})
	rec.EmitState("done")
	rec.Close()

	n, err := countSteps(store.DB(), *taskID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stepsim: count steps:", err)
		os.Exit(1)
	}
	fmt.Printf("STEPS %d\n", n)
}

func countSteps(conn *sql.DB, taskID string) (int, error) {
	var n int
	err := conn.QueryRow(`SELECT COUNT(*) FROM run_steps WHERE task_id = ?`, taskID).Scan(&n)
	return n, err
}
