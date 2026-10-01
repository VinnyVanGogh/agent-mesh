package telemetry

import (
	"strings"
)

// EstimateModelCost calculates estimated USD cost for model token consumption.
// Rates are based on standard per-million-token published API prices.
func EstimateModelCost(model string, inputTokens, outputTokens, cacheRead, cacheCreation int64) float64 {
	lower := strings.ToLower(model)

	var inPerM, outPerM, cacheReadPerM, cacheCreatePerM float64

	switch {
	case strings.Contains(lower, "opus-5-5") || strings.Contains(lower, "opus-5.5"):
		inPerM = 4.00
		outPerM = 20.00
		cacheReadPerM = 0.20
		cacheCreatePerM = 5.00

	case strings.Contains(lower, "opus-5"):
		inPerM = 5.00
		outPerM = 25.00
		cacheReadPerM = 0.50
		cacheCreatePerM = 6.25

	case strings.Contains(lower, "opus"):
		inPerM = 15.00
		outPerM = 75.00
		cacheReadPerM = 1.50
		cacheCreatePerM = 18.75

	case strings.Contains(lower, "haiku"):
		inPerM = 0.80
		outPerM = 4.00
		cacheReadPerM = 0.08
		cacheCreatePerM = 1.00

	case strings.Contains(lower, "sonnet"):
		inPerM = 3.00
		outPerM = 15.00
		cacheReadPerM = 0.30
		cacheCreatePerM = 3.75

	case strings.Contains(lower, "gemini-3.1-flash-lite") || strings.Contains(lower, "gemini-3.5-flash-lite") || strings.Contains(lower, "flash-lite"):
		inPerM = 0.25
		outPerM = 1.50
		cacheReadPerM = 0.025
		cacheCreatePerM = 0.25

	case strings.Contains(lower, "gemini-3.8-flash") || strings.Contains(lower, "gemini-3.7-flash") || strings.Contains(lower, "gemini-3.6-flash") || strings.Contains(lower, "gemini-3.5-flash") || strings.Contains(lower, "gemini-3-flash"):
		inPerM = 0.75
		outPerM = 3.75
		cacheReadPerM = 0.075
		cacheCreatePerM = 0.75

	case strings.Contains(lower, "gemini-3.1-pro") || strings.Contains(lower, "gemini-3-pro"):
		inPerM = 2.00
		outPerM = 12.00
		cacheReadPerM = 0.20
		cacheCreatePerM = 2.00

	case strings.Contains(lower, "flash"):
		inPerM = 0.10
		outPerM = 0.40
		cacheReadPerM = 0.025
		cacheCreatePerM = 0.10

	case strings.Contains(lower, "pro") && strings.Contains(lower, "gemini"):
		inPerM = 1.25
		outPerM = 5.00
		cacheReadPerM = 0.3125
		cacheCreatePerM = 1.25

	case strings.Contains(lower, "o1") || strings.Contains(lower, "o3"):
		inPerM = 15.00
		outPerM = 60.00
		cacheReadPerM = 7.50
		cacheCreatePerM = 15.00

	case strings.Contains(lower, "gpt-4o-mini"):
		inPerM = 0.15
		outPerM = 0.60
		cacheReadPerM = 0.075
		cacheCreatePerM = 0.15

	case strings.Contains(lower, "gpt-4o"):
		inPerM = 2.50
		outPerM = 10.00
		cacheReadPerM = 1.25
		cacheCreatePerM = 2.50

	default:
		// Default to standard Sonnet-class pricing
		inPerM = 3.00
		outPerM = 15.00
		cacheReadPerM = 0.30
		cacheCreatePerM = 3.75
	}

	cost := (float64(inputTokens) * (inPerM / 1_000_000.0)) +
		(float64(outputTokens) * (outPerM / 1_000_000.0)) +
		(float64(cacheRead) * (cacheReadPerM / 1_000_000.0)) +
		(float64(cacheCreation) * (cacheCreatePerM / 1_000_000.0))

	return cost
}
