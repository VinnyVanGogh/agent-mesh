package governance

import (
	"context"
	"log/slog"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/decision"
)

// AdvisoryClient is satisfied by *decision.TogetherDecisionClient.
type AdvisoryClient interface {
	Decide(ctx context.Context, req decision.DecisionRequest) (decision.DecisionResult, error)
}

// globalAdvisory is the package-level shadow voter; nil = disabled.
var globalAdvisory AdvisoryClient

// SetAdvisoryClient installs (or removes with nil) the shadow decision voter.
// This is safe to call before any ExecuteTransition calls.
func SetAdvisoryClient(c AdvisoryClient) {
	globalAdvisory = c
}

// shadowVote fires an advisory Decide call in a background goroutine.
// It never blocks, never returns an error, and never changes task state.
func shadowVote(taskID, from, to string) {
	c := globalAdvisory
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req := decision.DecisionRequest{
			State:    "task_id=" + taskID + " stage=" + from,
			Question: "Is transitioning from " + from + " to " + to + " the right next step?",
			Options: []decision.Option{
				{Key: "proceed", Letter: "A", Label: "Yes, proceed"},
				{Key: "hold", Letter: "B", Label: "No, hold"},
			},
		}
		result, err := c.Decide(ctx, req)
		if err != nil {
			slog.Warn("decision: advisory vote failed",
				"task_id", taskID,
				"from", from,
				"to", to,
				"err", err,
			)
			return
		}
		slog.Info("decision: advisory vote",
			"task_id", taskID,
			"from", from,
			"to", to,
			"pick", result.SelectedKey,
			"letter", result.SelectedLetter,
		)
	}()
}
