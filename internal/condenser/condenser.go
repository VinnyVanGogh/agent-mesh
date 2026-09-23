package condenser

import (
	"fmt"
	"strings"
	"time"
)

// Condense processes raw error logs and produces a compact, token-efficient version.
func Condense(raw string, opts CondenseOptions) (*CondenseResult, error) {
	start := time.Now()
	rawTrimmed := strings.TrimSpace(raw)
	if rawTrimmed == "" {
		return &CondenseResult{
			Condensed:      "",
			DetectedFormat: FormatGeneric,
			Duration:       time.Since(start),
		}, nil
	}

	origBytes := len(raw)
	origTokens := EstimateTokens(raw)

	if opts.MaxLines <= 0 {
		opts.MaxLines = 80
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 1200
	}

	detectedFormat := opts.Format
	if detectedFormat == "" || detectedFormat == FormatAuto {
		switch {
		case IsTypeScriptOutput(rawTrimmed):
			detectedFormat = FormatTypeScript
		case IsGoOutput(rawTrimmed):
			detectedFormat = FormatGo
		case IsPythonOutput(rawTrimmed):
			detectedFormat = FormatPython
		default:
			detectedFormat = FormatGeneric
		}
	}

	var condensedText string
	switch detectedFormat {
	case FormatTypeScript:
		condensedText = CondenseTypeScript(rawTrimmed, opts.MaxLines)
	case FormatGo:
		condensedText = CondenseGo(rawTrimmed, opts.MaxLines)
	case FormatPython:
		condensedText = CondensePython(rawTrimmed, opts.MaxLines)
	default:
		condensedText = strings.Join(ClampLines(DeduplicateLines(strings.Split(StripANSI(rawTrimmed), "\n")), opts.MaxLines), "\n")
	}

	condensedBytes := len(condensedText)
	condensedTokens := EstimateTokens(condensedText)

	reductionPct := 0.0
	if origTokens > 0 && condensedTokens < origTokens {
		reductionPct = float64(origTokens-condensedTokens) / float64(origTokens) * 100.0
	}

	// Append telemetry footer if savings were achieved
	if opts.ShowSavings && reductionPct > 0.0 {
		footer := fmt.Sprintf("\n[Token Diet: %s | Original: ~%d tokens ➔ Condensed: ~%d tokens (%.1f%% reduction)]",
			detectedFormat, origTokens, condensedTokens, reductionPct)
		condensedText += footer
		condensedBytes = len(condensedText)
		condensedTokens = EstimateTokens(condensedText)
	}

	return &CondenseResult{
		Condensed:       condensedText,
		OriginalBytes:   origBytes,
		CondensedBytes:  condensedBytes,
		OriginalTokens:  origTokens,
		CondensedTokens: condensedTokens,
		ReductionPct:    reductionPct,
		DetectedFormat:  detectedFormat,
		Duration:        time.Since(start),
	}, nil
}
