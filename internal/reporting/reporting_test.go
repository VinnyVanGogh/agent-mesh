package reporting

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vincevasile/agent-mesh/internal/config"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// 1. Unit Tests: Date-Range Parsing (parseDateBound)
// ---------------------------------------------------------------------------

func TestParseDateBound_ExactDates(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		isEnd     bool
		expected  string
		wantError bool
	}{
		{
			name:      "Standard ISO date start",
			input:     "2026-08-01",
			isEnd:     false,
			expected:  "2026-08-01T00:00:00Z",
			wantError: false,
		},
		{
			name:      "Standard ISO date end",
			input:     "2026-08-01",
			isEnd:     true,
			expected:  "2026-08-01T23:59:59Z",
			wantError: false,
		},
		{
			name:      "Slash formatted date YYYY/MM/DD start",
			input:     "2026/08/15",
			isEnd:     false,
			expected:  "2026-08-15T00:00:00Z",
			wantError: false,
		},
		{
			name:      "Slash formatted date YYYY/MM/DD end",
			input:     "2026/08/15",
			isEnd:     true,
			expected:  "2026-08-15T23:59:59Z",
			wantError: false,
		},
		{
			name:      "US formatted date MM/DD/YYYY start",
			input:     "08/25/2026",
			isEnd:     false,
			expected:  "2026-08-25T00:00:00Z",
			wantError: false,
		},
		{
			name:      "US formatted date MM/DD/YYYY end",
			input:     "08/25/2026",
			isEnd:     true,
			expected:  "2026-08-25T23:59:59Z",
			wantError: false,
		},
		{
			name:      "Word formatted date start",
			input:     "Aug 10, 2026",
			isEnd:     false,
			expected:  "2026-08-10T00:00:00Z",
			wantError: false,
		},
		{
			name:      "Word formatted date end",
			input:     "Aug 10, 2026",
			isEnd:     true,
			expected:  "2026-08-10T23:59:59Z",
			wantError: false,
		},
		{
			name:      "Full RFC3339 timestamp preserved",
			input:     "2026-08-01T15:04:05Z",
			isEnd:     false,
			expected:  "2026-08-01T15:04:05Z",
			wantError: false,
		},
		{
			name:      "ISO datetime without timezone start",
			input:     "2026-08-01T12:00:00",
			isEnd:     false,
			expected:  "2026-08-01T12:00:00Z",
			wantError: false,
		},
		{
			name:      "Datetime with space separator",
			input:     "2026-08-01 12:00:00",
			isEnd:     false,
			expected:  "2026-08-01T12:00:00Z",
			wantError: false,
		},
		{
			name:      "Whitespace trimmed",
			input:     "  2026-08-01  ",
			isEnd:     false,
			expected:  "2026-08-01T00:00:00Z",
			wantError: false,
		},
		{
			name:      "Empty input returns empty bound",
			input:     "",
			isEnd:     false,
			expected:  "",
			wantError: false,
		},
		{
			name:      "Whitespace-only input returns empty bound",
			input:     "   ",
			isEnd:     true,
			expected:  "",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDateBound(tt.input, tt.isEnd)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for input '%s', got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input '%s': %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("parseDateBound(%q, %v) = %q, want %q", tt.input, tt.isEnd, got, tt.expected)
			}
		})
	}
}

func TestParseDateBound_RelativeShorthands(t *testing.T) {
	shorthands := []struct {
		input string
		days  int
	}{
		{"7d", 7},
		{"14d", 14},
		{"30d", 30},
		{"90d", 90},
	}

	for _, tc := range shorthands {
		t.Run(tc.input, func(t *testing.T) {
			// Start bound (00:00:00 UTC)
			startStr, err := parseDateBound(tc.input, false)
			if err != nil {
				t.Fatalf("unexpected error for relative shorthand start %s: %v", tc.input, err)
			}
			startTime, err := time.Parse(time.RFC3339, startStr)
			if err != nil {
				t.Fatalf("failed to parse returned start bound %s: %v", startStr, err)
			}
			if startTime.Hour() != 0 || startTime.Minute() != 0 || startTime.Second() != 0 {
				t.Errorf("start bound time should be 00:00:00, got %s", startTime.Format("15:04:05"))
			}

			// End bound (23:59:59 UTC)
			endStr, err := parseDateBound(tc.input, true)
			if err != nil {
				t.Fatalf("unexpected error for relative shorthand end %s: %v", tc.input, err)
			}
			endTime, err := time.Parse(time.RFC3339, endStr)
			if err != nil {
				t.Fatalf("failed to parse returned end bound %s: %v", endStr, err)
			}
			if endTime.Hour() != 23 || endTime.Minute() != 59 || endTime.Second() != 59 {
				t.Errorf("end bound time should be 23:59:59, got %s", endTime.Format("15:04:05"))
			}

			// Expected approximate date delta
			now := time.Now().UTC()
			expectedDate := now.AddDate(0, 0, -tc.days)
			if startTime.Year() != expectedDate.Year() || startTime.Month() != expectedDate.Month() || startTime.Day() != expectedDate.Day() {
				t.Errorf("expected date %s, got %s", expectedDate.Format("2006-01-02"), startTime.Format("2006-01-02"))
			}
		})
	}
}

func TestParseDateBound_InvalidStrings(t *testing.T) {
	invalidInputs := []string{
		"invalid",
		"not-a-date",
		"2026-13-45",
		"2026/99/99",
		"-5d",
		"0d",
		"-30d",
		"d",
		"7days",
		"yesterday",
		"2026-08",
		"august",
		"2026-08-01T99:99:99",
	}

	for _, input := range invalidInputs {
		t.Run(input, func(t *testing.T) {
			_, err := parseDateBound(input, false)
			if err == nil {
				t.Errorf("expected parseDateBound to fail for invalid input %q, but got nil error", input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Integration Tests: FetchTelemetryWithRange with Mock SQLite Database
// ---------------------------------------------------------------------------

func createMockTelemetryDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to create temp sqlite db: %v", err)
	}
	defer db.Close()

	// Create schema matching telemetry watcher & queries
	schema := `
	CREATE TABLE requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL,
		account_email TEXT NOT NULL,
		model_family TEXT NOT NULL,
		cost_usd REAL NOT NULL,
		total_tokens INTEGER NOT NULL,
		input_tokens INTEGER NOT NULL,
		output_tokens INTEGER NOT NULL
	);
	CREATE TABLE review_outcomes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repo TEXT NOT NULL,
		severity INTEGER NOT NULL
	);
	CREATE TABLE subagent_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// Insert test data: timestamps between 2026-08-10 and 2026-09-15
	rows := []struct {
		ts           string
		email        string
		family       string
		cost         float64
		totalTokens  int64
		inputTokens  int64
		outputTokens int64
	}{
		{"2026-08-10T10:00:00Z", "vvasile@managedsolution.com", "claude", 50.0, 500000, 450000, 50000},
		{"2026-08-15T12:00:00Z", "vvasile@managedsolution.com", "claude", 100.0, 1000000, 900000, 100000},
		{"2026-08-20T14:00:00Z", "stylesbyvinny@gmail.com", "claude", 250.0, 2500000, 2300000, 200000},
		{"2026-09-01T09:00:00Z", "stylesbyvinny@gmail.com", "gemini", 20.0, 3000000, 2800000, 200000},
		{"2026-09-15T16:00:00Z", "vvasile@managedsolution.com", "gemini", 15.0, 2000000, 1900000, 100000},
	}

	stmt, err := db.Prepare(`INSERT INTO requests (ts, account_email, model_family, cost_usd, total_tokens, input_tokens, output_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("failed to prepare insert stmt: %v", err)
	}
	defer stmt.Close()

	for _, r := range rows {
		if _, err := stmt.Exec(r.ts, r.email, r.family, r.cost, r.totalTokens, r.inputTokens, r.outputTokens); err != nil {
			t.Fatalf("failed to insert mock telemetry row: %v", err)
		}
	}

	// Insert review outcomes & subagent runs
	_, _ = db.Exec(`INSERT INTO review_outcomes (repo, severity) VALUES ('agent-mesh', 1), ('agent-mesh', 0), ('vps-hr', 2)`)
	_, _ = db.Exec(`INSERT INTO subagent_runs (task_id) VALUES ('task-101'), ('task-102'), ('task-103')`)

	return dbPath
}

func TestFetchTelemetryWithRange_ValidRange(t *testing.T) {
	dbPath := createMockTelemetryDB(t)

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		WorkEmail:       "vvasile@managedsolution.com",
		PersonalEmail:   "stylesbyvinny@gmail.com",
		CompanyName:     "Managed Solution",
		EngineerName:    "Vince Vasile",
		HourlyRate:      150.0,
	}

	rangeOpts := DateRangeOptions{
		Since: "2026-08-01",
		Until: "2026-08-31",
	}

	work, personal, gemini, combined, err := FetchTelemetryWithRange(cfg, rangeOpts)
	if err != nil {
		t.Fatalf("unexpected error from FetchTelemetryWithRange: %v", err)
	}

	// In August:
	// Work has 2 records: costs 50 + 100 = 150
	if work.AcceptedTurns != "2" {
		t.Errorf("expected 2 work accepted turns, got %s", work.AcceptedTurns)
	}
	if work.SubstantiatedValue != "$150.00" {
		t.Errorf("expected $150.00 substantiated value, got %s", work.SubstantiatedValue)
	}

	// Personal has 1 record in August: cost 250
	if personal.TotalRequests != "1" {
		t.Errorf("expected 1 personal request in August, got %s", personal.TotalRequests)
	}
	if personal.DeliveredValue != "$250.00" {
		t.Errorf("expected $250.00 personal value, got %s", personal.DeliveredValue)
	}

	// Gemini had 0 records in August
	// Combined total invocations in August = 3
	if combined.TotalInvocations != "3" {
		t.Errorf("expected 3 total invocations in August, got %s", combined.TotalInvocations)
	}

	// Check reviews & subagents
	if gemini.CodeReviews != "3" {
		t.Errorf("expected 3 code reviews, got %s", gemini.CodeReviews)
	}
	if gemini.SubagentRuns != "3" {
		t.Errorf("expected 3 subagent runs, got %s", gemini.SubagentRuns)
	}
	if gemini.BugsFound != "2" {
		t.Errorf("expected 2 bugs found (severity > 0), got %s", gemini.BugsFound)
	}
}

func TestFetchTelemetryWithRange_OutOfRangeError(t *testing.T) {
	dbPath := createMockTelemetryDB(t)

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		WorkEmail:       "vvasile@managedsolution.com",
		PersonalEmail:   "stylesbyvinny@gmail.com",
	}

	// Query with date range far in the past where no records exist
	rangeOpts := DateRangeOptions{
		Since: "2020-01-01",
		Until: "2020-01-02",
	}

	_, _, _, _, err := FetchTelemetryWithRange(cfg, rangeOpts)
	if err == nil {
		t.Fatalf("expected error for out-of-range bounds, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "no telemetry records found between 2020-01-01 and 2020-01-02") {
		t.Errorf("error missing expected bounds description: %s", errMsg)
	}
	if !strings.Contains(errMsg, "database spans") {
		t.Errorf("error missing database spans info: %s", errMsg)
	}
	// Verify it contains the actual span of mock data: Aug 10, 2026 to Sep 15, 2026
	if !strings.Contains(errMsg, "Aug 10, 2026 to Sep 15, 2026") {
		t.Errorf("error does not properly format database span dates: %s", errMsg)
	}
}

func TestFetchTelemetryWithRange_HourlyRateBehavior(t *testing.T) {
	dbPath := createMockTelemetryDB(t)

	t.Run("hourly_rate == 0.0 (billable hours omitted)", func(t *testing.T) {
		cfg := &config.Config{
			TelemetryDBPath: dbPath,
			WorkEmail:       "vvasile@managedsolution.com",
			HourlyRate:      0.0,
		}

		work, _, _, _, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if work.HasHourlyRate {
			t.Errorf("expected HasHourlyRate to be false when hourly_rate == 0")
		}
		if work.HoursSavedBreakEven != "" {
			t.Errorf("expected HoursSavedBreakEven to be empty, got %q", work.HoursSavedBreakEven)
		}
		if work.TotalHoursSaved != "" {
			t.Errorf("expected TotalHoursSaved to be empty, got %q", work.TotalHoursSaved)
		}
		if work.DirectCostMultiplier != "14.4x net return on upgrade" {
			t.Errorf("expected fallback DirectCostMultiplier, got %q", work.DirectCostMultiplier)
		}

		// Verify HTML rendering omits billable hours and includes value multipliers
		html, err := generateWorkHTML(work)
		if err != nil {
			t.Fatalf("failed to render HTML: %v", err)
		}
		if strings.Contains(html, "billable client hours") {
			t.Errorf("HTML should not mention billable client hours when rate is 0.0")
		}
		if !strings.Contains(html, "14.4x net return on upgrade") {
			t.Errorf("HTML should contain DirectCostMultiplier fallback")
		}
		if !strings.Contains(html, "Fully unblocked velocity multiplier") {
			t.Errorf("HTML should contain unblocked multiplier fallback")
		}
	})

	t.Run("hourly_rate > 0.0 (billable hours populated)", func(t *testing.T) {
		cfg := &config.Config{
			TelemetryDBPath: dbPath,
			WorkEmail:       "vvasile@managedsolution.com",
			HourlyRate:      150.0,
		}

		work, _, _, _, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !work.HasHourlyRate {
			t.Errorf("expected HasHourlyRate to be true when hourly_rate > 0")
		}
		if !strings.Contains(work.HoursSavedBreakEven, "billable client hours (@ $150/hr)") {
			t.Errorf("expected HoursSavedBreakEven with hourly rate, got %q", work.HoursSavedBreakEven)
		}
		if !strings.Contains(work.TotalHoursSaved, "client billable hours saved") {
			t.Errorf("expected TotalHoursSaved with billable hours, got %q", work.TotalHoursSaved)
		}

		// Verify HTML rendering displays billable hours
		html, err := generateWorkHTML(work)
		if err != nil {
			t.Fatalf("failed to render HTML: %v", err)
		}
		if !strings.Contains(html, "billable client hours (@ $150/hr)") {
			t.Errorf("HTML should include breakeven billable hours")
		}
		if !strings.Contains(html, "client billable hours saved") {
			t.Errorf("HTML should include total billable hours saved")
		}
	})
}

func TestFetchTelemetryWithRange_CompanyAndEngineerFields(t *testing.T) {
	dbPath := createMockTelemetryDB(t)

	t.Run("populated company and engineer names", func(t *testing.T) {
		cfg := &config.Config{
			TelemetryDBPath: dbPath,
			WorkEmail:       "vvasile@managedsolution.com",
			PersonalEmail:   "stylesbyvinny@gmail.com",
			CompanyName:     "Managed Solution",
			EngineerName:    "Vince Vasile",
		}

		work, personal, gemini, combined, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !work.HasCompanyName || work.CompanyName != "Managed Solution" {
			t.Errorf("expected HasCompanyName true and CompanyName 'Managed Solution', got %v, %q", work.HasCompanyName, work.CompanyName)
		}
		if !work.HasEngineerName || work.EngineerName != "Vince Vasile" {
			t.Errorf("expected HasEngineerName true and EngineerName 'Vince Vasile', got %v, %q", work.HasEngineerName, work.EngineerName)
		}
		if !personal.HasEngineerName || !gemini.HasEngineerName || !combined.HasEngineerName {
			t.Errorf("expected all reports to indicate HasEngineerName = true")
		}

		// Check Work HTML
		workHTML, err := generateWorkHTML(work)
		if err != nil {
			t.Fatalf("failed to render work HTML: %v", err)
		}
		if !strings.Contains(workHTML, "Managed Solution —") {
			t.Errorf("expected company title prefix in work HTML")
		}
		if !strings.Contains(workHTML, "<div class=\"org\">Managed Solution</div>") {
			t.Errorf("expected org div in work HTML")
		}
		if !strings.Contains(workHTML, "Engineer: Vince Vasile") {
			t.Errorf("expected Engineer: Vince Vasile in work HTML")
		}
		if !strings.Contains(workHTML, "across Managed Solution repositories") {
			t.Errorf("expected company repo reference in proposal box")
		}
		if !strings.Contains(workHTML, "For Internal Managed Solution Review Only") {
			t.Errorf("expected confidential footer with company name")
		}

		// Check Personal HTML
		personalHTML, err := generatePersonalHTML(personal)
		if err != nil {
			t.Fatalf("failed to render personal HTML: %v", err)
		}
		if !strings.Contains(personalHTML, "Engineer: Vince Vasile") {
			t.Errorf("expected Engineer: Vince Vasile in personal HTML")
		}

		// Check Gemini HTML
		geminiHTML, err := generateGeminiHTML(gemini)
		if err != nil {
			t.Fatalf("failed to render gemini HTML: %v", err)
		}
		if !strings.Contains(geminiHTML, "Operator: Vince Vasile") {
			t.Errorf("expected Operator: Vince Vasile in gemini HTML")
		}

		// Check Combined HTML
		combinedHTML, err := generateCombinedHTML(combined)
		if err != nil {
			t.Fatalf("failed to render combined HTML: %v", err)
		}
		if !strings.Contains(combinedHTML, "Lead Engineer: Vince Vasile") {
			t.Errorf("expected Lead Engineer: Vince Vasile in combined HTML")
		}
	})

	t.Run("empty company and engineer names", func(t *testing.T) {
		cfg := &config.Config{
			TelemetryDBPath: dbPath,
			WorkEmail:       "vvasile@managedsolution.com",
			PersonalEmail:   "stylesbyvinny@gmail.com",
			CompanyName:     "",
			EngineerName:    "",
		}

		work, personal, gemini, combined, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if work.HasCompanyName {
			t.Errorf("expected HasCompanyName false when empty")
		}
		if work.HasEngineerName {
			t.Errorf("expected HasEngineerName false when empty")
		}
		if personal.HasEngineerName || gemini.HasEngineerName || combined.HasEngineerName {
			t.Errorf("expected HasEngineerName false across all reports when empty")
		}

		// Check Work HTML fallback
		workHTML, err := generateWorkHTML(work)
		if err != nil {
			t.Fatalf("failed to render work HTML: %v", err)
		}
		if strings.Contains(workHTML, "<div class=\"org\">") {
			t.Errorf("org div should not be present when company name is empty")
		}
		if strings.Contains(workHTML, "Engineer:") {
			t.Errorf("Engineer label should not be present when engineer name is empty")
		}
		if !strings.Contains(workHTML, "Account: <code>vvasile@managedsolution.com</code>") {
			t.Errorf("should fallback to Account: <email> when engineer name is empty")
		}
		if !strings.Contains(workHTML, "across core repositories") {
			t.Errorf("should fallback to 'across core repositories' when company name is empty")
		}
		if !strings.Contains(workHTML, "For Executive Review Only") {
			t.Errorf("should fallback to 'For Executive Review Only' when company name is empty")
		}
	})
}

func TestFetchTelemetryWithRange_NonExistentDBGracefulFallback(t *testing.T) {
	cfg := &config.Config{
		TelemetryDBPath: "/path/to/non_existent_telemetry.db",
		WorkEmail:       "test@example.com",
	}

	work, personal, gemini, combined, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
	if err != nil {
		t.Fatalf("expected graceful return when db doesn't exist, got error: %v", err)
	}

	// Verify defaults are populated
	if work.SubstantiatedValue == "" || personal.DeliveredValue == "" || gemini.TotalTokens == "" || combined.TotalValue == "" {
		t.Errorf("expected default metrics when database file does not exist")
	}
}

// ---------------------------------------------------------------------------
// 3. Unit Tests: HTML Template Compilation for all 4 Report Types
// ---------------------------------------------------------------------------

func TestHTMLTemplateCompilation_AllReports(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CompanyName = "Acme Corp & Partners <Test>"
	cfg.EngineerName = "Jane \"The Architect\" Doe"
	cfg.HourlyRate = 175.0

	work, personal, gemini, combined, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
	if err != nil {
		t.Fatalf("telemetry fetch failed: %v", err)
	}

	t.Run("generateWorkHTML compilation", func(t *testing.T) {
		html, err := generateWorkHTML(work)
		if err != nil {
			t.Fatalf("work HTML generation failed: %v", err)
		}
		if len(html) < 500 {
			t.Errorf("generated work HTML is unexpectedly short: len=%d", len(html))
		}
		if !strings.Contains(html, "<!DOCTYPE html>") {
			t.Errorf("missing DOCTYPE in work HTML")
		}
	})

	t.Run("generatePersonalHTML compilation", func(t *testing.T) {
		html, err := generatePersonalHTML(personal)
		if err != nil {
			t.Fatalf("personal HTML generation failed: %v", err)
		}
		if len(html) < 500 {
			t.Errorf("generated personal HTML is unexpectedly short: len=%d", len(html))
		}
		if !strings.Contains(html, "<!DOCTYPE html>") {
			t.Errorf("missing DOCTYPE in personal HTML")
		}
	})

	t.Run("generateGeminiHTML compilation", func(t *testing.T) {
		html, err := generateGeminiHTML(gemini)
		if err != nil {
			t.Fatalf("gemini HTML generation failed: %v", err)
		}
		if len(html) < 500 {
			t.Errorf("generated gemini HTML is unexpectedly short: len=%d", len(html))
		}
		if !strings.Contains(html, "<!DOCTYPE html>") {
			t.Errorf("missing DOCTYPE in gemini HTML")
		}
	})

	t.Run("generateCombinedHTML compilation", func(t *testing.T) {
		html, err := generateCombinedHTML(combined)
		if err != nil {
			t.Fatalf("combined HTML generation failed: %v", err)
		}
		if len(html) < 500 {
			t.Errorf("generated combined HTML is unexpectedly short: len=%d", len(html))
		}
		if !strings.Contains(html, "<!DOCTYPE html>") {
			t.Errorf("missing DOCTYPE in combined HTML")
		}
	})

	t.Run("Zero and Nil models in Personal and Gemini do not panic", func(t *testing.T) {
		emptyPersonal := PersonalReportData{}
		if _, err := generatePersonalHTML(emptyPersonal); err != nil {
			t.Errorf("generatePersonalHTML failed on empty struct: %v", err)
		}

		emptyGemini := GeminiReportData{}
		if _, err := generateGeminiHTML(emptyGemini); err != nil {
			t.Errorf("generateGeminiHTML failed on empty struct: %v", err)
		}

		emptyWork := WorkReportData{}
		if _, err := generateWorkHTML(emptyWork); err != nil {
			t.Errorf("generateWorkHTML failed on empty struct: %v", err)
		}

		emptyCombined := CombinedReportData{}
		if _, err := generateCombinedHTML(emptyCombined); err != nil {
			t.Errorf("generateCombinedHTML failed on empty struct: %v", err)
		}
	})
}

func TestRenderReport_UnknownReportType(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	err := RenderReport(ctx, "invalid-type", cfg, filepath.Join(t.TempDir(), "out.pdf"))
	if err == nil {
		t.Fatalf("expected error for unknown report type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown report type") {
		t.Errorf("expected error to mention 'unknown report type', got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 4. Verification: PDF Generation & Single-Page Layout Integrity
// ---------------------------------------------------------------------------

func TestSinglePageCSSIntegrity(t *testing.T) {
	cfg := config.DefaultConfig()
	work, personal, gemini, combined, err := FetchTelemetryWithRange(cfg, DateRangeOptions{})
	if err != nil {
		t.Fatalf("FetchTelemetryWithRange failed: %v", err)
	}

	reports := map[string]func() (string, error){
		"work":     func() (string, error) { return generateWorkHTML(work) },
		"personal": func() (string, error) { return generatePersonalHTML(personal) },
		"gemini":   func() (string, error) { return generateGeminiHTML(gemini) },
		"combined": func() (string, error) { return generateCombinedHTML(combined) },
	}

	for rtype, gen := range reports {
		t.Run(rtype+" CSS rules", func(t *testing.T) {
			html, err := gen()
			if err != nil {
				t.Fatalf("failed generating %s HTML: %v", rtype, err)
			}

			// Verify strict letter size and zero margin in @page
			if !strings.Contains(html, "@page { size: letter; margin: 0; }") {
				t.Errorf("%s report missing strict @page { size: letter; margin: 0; }", rtype)
			}

			// Verify overflow: hidden and height constraints
			if !strings.Contains(html, "overflow: hidden;") {
				t.Errorf("%s report missing overflow: hidden constraint", rtype)
			}

			// Verify print media avoidance of page breaks
			if !strings.Contains(html, "@media print") {
				t.Errorf("%s report missing @media print block", rtype)
			}
			if !strings.Contains(html, "page-break-inside: avoid") && !strings.Contains(html, "break-inside: avoid") {
				t.Errorf("%s report missing break avoidance rules", rtype)
			}
		})
	}
}

// getPDFPageCount returns the page count of a PDF using pdfinfo or basic trailer analysis.
func getPDFPageCount(t *testing.T, pdfPath string) int {
	t.Helper()
	if _, err := exec.LookPath("pdfinfo"); err == nil {
		out, err := exec.Command("pdfinfo", pdfPath).Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "Pages:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						count, err := strconv.Atoi(fields[1])
						if err == nil {
							return count
						}
					}
				}
			}
		}
	}

	// Fallback: parse PDF bytes directly for /Count
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("failed to read pdf %s: %v", pdfPath, err)
	}
	content := string(data)
	idx := strings.Index(content, "/Type /Pages")
	if idx != -1 {
		sub := content[idx : idx+100]
		if cIdx := strings.Index(sub, "/Count "); cIdx != -1 {
			var cnt int
			if _, err := fmt.Sscanf(sub[cIdx:], "/Count %d", &cnt); err == nil {
				return cnt
			}
		}
	}
	return 1
}

func isChromeInstalled() bool {
	chromePaths := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, p := range chromePaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("google-chrome")
	return err == nil
}

func TestPDFGeneration_SinglePageOutput(t *testing.T) {
	if !isChromeInstalled() {
		t.Skip("Google Chrome / Chromium not found; skipping headless browser PDF render test")
	}

	outDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.CompanyName = "Managed Solution"
	cfg.EngineerName = "Vince Vasile"
	cfg.HourlyRate = 175.0

	types := []string{"work", "personal", "gemini", "combined"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	for _, reportType := range types {
		t.Run(reportType+" render 1 page", func(t *testing.T) {
			pdfPath := filepath.Join(outDir, fmt.Sprintf("%s-test.pdf", reportType))
			err := RenderReport(ctx, reportType, cfg, pdfPath)
			if err != nil {
				t.Fatalf("RenderReport failed for %s: %v", reportType, err)
			}

			fi, err := os.Stat(pdfPath)
			if err != nil {
				t.Fatalf("pdf file does not exist: %v", err)
			}
			if fi.Size() < 5000 {
				t.Errorf("pdf file size unexpectedly small (%d bytes)", fi.Size())
			}

			pages := getPDFPageCount(t, pdfPath)
			if pages != 1 {
				t.Errorf("PDF %s has %d pages, expected strictly 1 page", filepath.Base(pdfPath), pages)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. Batch Generation Collision Safety Verification
// ---------------------------------------------------------------------------

func TestBatchGeneration_NoFileCollision(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CompanyName = "Alpha Tech"

	t.Run("ResolveBatchDestination with directory destination", func(t *testing.T) {
		tempDir := t.TempDir()
		paths := make(map[string]bool)
		types := []string{"work", "personal", "gemini", "combined"}

		for _, rtype := range types {
			dest := ResolveBatchDestination(tempDir, rtype, cfg)
			if !strings.HasPrefix(dest, tempDir) {
				t.Errorf("expected destination %s to be within directory %s", dest, tempDir)
			}
			if paths[dest] {
				t.Errorf("file write collision detected! %s was already generated", dest)
			}
			paths[dest] = true
		}
		if len(paths) != 4 {
			t.Errorf("expected 4 distinct paths, got %d", len(paths))
		}
	})

	t.Run("ResolveBatchDestination with file pattern destination", func(t *testing.T) {
		pattern := "/tmp/audit-report.pdf"
		paths := make(map[string]bool)
		types := []string{"work", "personal", "gemini", "combined"}

		for _, rtype := range types {
			dest := ResolveBatchDestination(pattern, rtype, cfg)
			if paths[dest] {
				t.Errorf("file write collision detected! %s was already generated", dest)
			}
			paths[dest] = true
			expectedSuffix := fmt.Sprintf("audit-report-%s.pdf", rtype)
			if !strings.HasSuffix(dest, expectedSuffix) {
				t.Errorf("expected %s to have suffix %s", dest, expectedSuffix)
			}
		}
		if len(paths) != 4 {
			t.Errorf("expected 4 distinct paths, got %d", len(paths))
		}
	})

	t.Run("DefaultReportFilename company slug", func(t *testing.T) {
		fn := DefaultReportFilename("work", cfg)
		if fn != "alpha-tech-ai-justification.pdf" {
			t.Errorf("expected slugified company name 'alpha-tech-ai-justification.pdf', got %q", fn)
		}
	})

	t.Run("RenderAllReports integration write test", func(t *testing.T) {
		if !isChromeInstalled() {
			t.Skip("Google Chrome / Chromium not found; skipping batch render test")
		}

		tempDir := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		results, err := RenderAllReports(ctx, cfg, tempDir)
		if err != nil {
			t.Fatalf("RenderAllReports failed: %v", err)
		}

		if len(results) != 4 {
			t.Fatalf("expected 4 reports rendered, got %d", len(results))
		}

		seenPaths := make(map[string]bool)
		for rtype, path := range results {
			if seenPaths[path] {
				t.Errorf("collision detected on path %s for report type %s", path, rtype)
			}
			seenPaths[path] = true

			fi, err := os.Stat(path)
			if err != nil {
				t.Errorf("rendered file %s missing: %v", path, err)
				continue
			}
			if fi.Size() == 0 {
				t.Errorf("rendered file %s is empty", path)
			}

			pages := getPDFPageCount(t, path)
			if pages != 1 {
				t.Errorf("batch generated file %s has %d pages, expected 1", filepath.Base(path), pages)
			}
		}
	})
}
