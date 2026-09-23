package condenser

import (
	"time"
)

// Format represents the detected or specified output format.
type Format string

const (
	FormatAuto       Format = "auto"
	FormatTypeScript Format = "typescript"
	FormatGo         Format = "go"
	FormatPython     Format = "python"
	FormatGeneric    Format = "generic"
)

// CondenseOptions defines configuration parameters for error reduction.
type CondenseOptions struct {
	Format      Format // Target format (default: FormatAuto)
	MaxTokens   int    // Hard limit on estimated output tokens (default: 1200)
	MaxLines    int    // Maximum lines allowed (default: 100)
	ShowSavings bool   // Append [Token Diet: ...] telemetry footer (default: true)
	RepoRoot    string // Used to distinguish application code from external vendor frames
}

// CondenseResult holds condensed text and token economics.
type CondenseResult struct {
	Condensed       string        `json:"condensed"`
	OriginalBytes   int           `json:"original_bytes"`
	CondensedBytes  int           `json:"condensed_bytes"`
	OriginalTokens  int           `json:"original_tokens"`
	CondensedTokens int           `json:"condensed_tokens"`
	ReductionPct    float64       `json:"reduction_pct"`
	DetectedFormat  Format        `json:"detected_format"`
	Duration        time.Duration `json:"duration"`
}

// EstimateTokens provides a fast, accurate token count approximation (1 token ~= 4 chars).
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	tokens := len(text) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}
