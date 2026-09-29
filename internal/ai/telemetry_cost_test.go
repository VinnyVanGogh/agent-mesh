package ai

import (
	"context"
	"strings"
	"testing"
)

func TestHeuristicFallbackIsUnpricedNotZero(t *testing.T) {
	gen := NewGenerator(GeneratorConfig{})
	res, err := gen.GenerateTask(context.Background(), "Fix critical race condition in watcher and add tests", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unpriced || !res.FallbackUsed || res.Model != "heuristic-fallback" {
		t.Fatalf("expected unpriced heuristic fallback, got %+v", res)
	}
	if res.EstimatedCostUSD != 0 {
		t.Errorf("billed cost must be 0 for a local turn, got %f", res.EstimatedCostUSD)
	}
	if res.InputTokens <= 0 || res.OutputTokens <= 0 {
		t.Errorf("token estimate must be positive: in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
	if res.ReferenceCostUSD <= 0 {
		t.Errorf("reference cost estimate must be positive, got %f", res.ReferenceCostUSD)
	}

	out := res.FormatCost()
	if strings.Contains(out, "0.000000 USD") && !strings.Contains(out, "unpriced") {
		t.Errorf("bare zero cost rendered: %q", out)
	}
	if !strings.HasPrefix(out, "unpriced") || !strings.Contains(out, res.ReferenceModel) {
		t.Errorf("cost line must be explicitly unpriced with reference model: %q", out)
	}
}

func TestEstimateHeuristicTokens(t *testing.T) {
	in, out := EstimateHeuristicTokens(strings.Repeat("a", 400))
	if in != 100 || out != heuristicOutputTokens {
		t.Errorf("in=%d out=%d, want 100/%d", in, out, heuristicOutputTokens)
	}
	if in, _ := EstimateHeuristicTokens("abc"); in != 1 {
		t.Errorf("short prompt rounds up, got %d", in)
	}
}

func TestFormatCostPriced(t *testing.T) {
	r := &GenerationResult{EstimatedCostUSD: 0.001234}
	if got := r.FormatCost(); got != "$0.001234 USD" {
		t.Errorf("got %q", got)
	}
	// Gemini 3.8 Flash: 1M in @ $0.15 + 1M out @ $0.60 = $0.75
	if got := CalculateCost("gemini-3.8-flash", 1_000_000, 1_000_000, 0); got < 0.7499 || got > 0.7501 {
		t.Errorf("CalculateCost=%f want 0.75", got)
	}
}
