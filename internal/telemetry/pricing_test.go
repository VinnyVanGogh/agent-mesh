package telemetry

import (
	"testing"
)

func TestEstimateModelCost(t *testing.T) {
	tests := []struct {
		model         string
		in, out       int64
		cRead, cWrite int64
		expectedMin   float64
		expectedMax   float64
	}{
		{
			model:       "claude-3-7-sonnet",
			in:          1_000_000,
			out:         1_000_000,
			cRead:       0,
			cWrite:      0,
			expectedMin: 17.99,
			expectedMax: 18.01, // 3 + 15 = 18
		},
		{
			model:       "claude-3-5-haiku",
			in:          1_000_000,
			out:         1_000_000,
			cRead:       0,
			cWrite:      0,
			expectedMin: 4.79,
			expectedMax: 4.81, // 0.80 + 4.00 = 4.80
		},
		{
			model:       "gemini-1.5-flash",
			in:          1_000_000,
			out:         1_000_000,
			cRead:       0,
			cWrite:      0,
			expectedMin: 0.49,
			expectedMax: 0.51, // 0.10 + 0.40 = 0.50
		},
	}

	for _, tt := range tests {
		cost := EstimateModelCost(tt.model, tt.in, tt.out, tt.cRead, tt.cWrite)
		if cost < tt.expectedMin || cost > tt.expectedMax {
			t.Errorf("model %s cost = %f, expected between %f and %f", tt.model, cost, tt.expectedMin, tt.expectedMax)
		}
	}
}
