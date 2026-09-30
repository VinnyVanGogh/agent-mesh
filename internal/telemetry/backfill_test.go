package telemetry

import (
	"database/sql"
	"testing"
)

func TestBackfillCosts_GeminiFlash(t *testing.T) {
	db := openTestDB(t)

	// Insert a Gemini hook record without cost (simulates token-telemetry hook insert).
	_, err := db.Exec(`
		INSERT INTO requests (idempotency_key, detected_via, ts, model, model_family,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
			session_id, account_email)
		VALUES ('backfill-test-flash', 'hook', '2026-09-30T12:00:00Z', 'gemini-3.8-flash', 'gemini',
			100000, 5000, 0, 0, 105000,
			'session-backfill', 'test@example.com')`,
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	updated, err := BackfillCosts(db)
	if err != nil {
		t.Fatalf("BackfillCosts error: %v", err)
	}
	if updated != 1 {
		t.Errorf("expected 1 row updated, got %d", updated)
	}

	var costUSD float64
	var costMicros int64
	err = db.QueryRow(`SELECT cost_usd, cost_usd_micros FROM requests WHERE idempotency_key = 'backfill-test-flash'`).
		Scan(&costUSD, &costMicros)
	if err != nil {
		t.Fatalf("query after backfill: %v", err)
	}

	// gemini-3.8-flash: $0.75/Mtok input + $3.75/Mtok output
	// 100k * 0.75/1M + 5k * 3.75/1M = 0.075 + 0.01875 = 0.09375
	if costUSD < 0.09 || costUSD > 0.10 {
		t.Errorf("expected ~$0.09375, got %f", costUSD)
	}
	if costMicros <= 0 {
		t.Errorf("expected positive cost_usd_micros, got %d", costMicros)
	}
}

func TestBackfillCosts_SkipsZeroTokens(t *testing.T) {
	db := openTestDB(t)

	// Insert a Gemini error record (zero tokens, no cost) — should not be backfilled.
	_, err := db.Exec(`
		INSERT INTO requests (idempotency_key, detected_via, ts, model, model_family,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
			session_id, account_email)
		VALUES ('backfill-test-zero', 'hook', '2026-09-30T12:00:00Z', 'gemini-3.8-flash', 'gemini',
			0, 0, 0, 0, 0,
			'session-zero', 'test@example.com')`,
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	updated, err := BackfillCosts(db)
	if err != nil {
		t.Fatalf("BackfillCosts error: %v", err)
	}
	if updated != 0 {
		t.Errorf("expected 0 rows updated for zero-token record, got %d", updated)
	}
}

func TestBackfillCosts_SkipsAlreadyPriced(t *testing.T) {
	db := openTestDB(t)

	// Insert a record that already has cost_usd set.
	_, err := db.Exec(`
		INSERT INTO requests (idempotency_key, detected_via, ts, model, model_family,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
			session_id, account_email, cost_usd, cost_usd_micros)
		VALUES ('backfill-test-priced', 'transcript', '2026-09-30T12:00:00Z', 'gemini-3.8-flash', 'gemini',
			100000, 5000, 0, 0, 105000,
			'session-priced', 'test@example.com', 0.123, 123000)`,
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	updated, err := BackfillCosts(db)
	if err != nil {
		t.Fatalf("BackfillCosts error: %v", err)
	}
	if updated != 0 {
		t.Errorf("expected 0 rows updated for already-priced record, got %d", updated)
	}

	// Verify cost unchanged.
	var cost float64
	_ = db.QueryRow(`SELECT cost_usd FROM requests WHERE idempotency_key = 'backfill-test-priced'`).Scan(&cost)
	if cost != 0.123 {
		t.Errorf("cost was modified from 0.123 to %f", cost)
	}
}

func TestBackfillCosts_FallbackEstimateForUnknownModel(t *testing.T) {
	db := openTestDB(t)

	// Insert a record with a model not in the ratecard.
	_, err := db.Exec(`
		INSERT INTO requests (idempotency_key, detected_via, ts, model, model_family,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
			session_id, account_email)
		VALUES ('backfill-test-unknown', 'hook', '2026-09-30T12:00:00Z', 'gemini-1.5-flash', 'gemini',
			1000000, 1000000, 0, 0, 2000000,
			'session-unknown', 'test@example.com')`,
	)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	updated, err := BackfillCosts(db)
	if err != nil {
		t.Fatalf("BackfillCosts error: %v", err)
	}
	// gemini-1.5-flash is not in ratecard; EstimateModelCost flash: $0.10+$0.40 = $0.50
	if updated != 1 {
		t.Errorf("expected 1 row updated, got %d", updated)
	}
	var costUSD float64
	_ = db.QueryRow(`SELECT cost_usd FROM requests WHERE idempotency_key = 'backfill-test-unknown'`).Scan(&costUSD)
	if costUSD < 0.49 || costUSD > 0.51 {
		t.Errorf("expected ~$0.50 fallback estimate, got %f", costUSD)
	}
}

// openTestDB creates an in-memory SQLite DB with the minimal requests schema.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE requests (
			id                  INTEGER PRIMARY KEY,
			idempotency_key     TEXT NOT NULL UNIQUE,
			detected_via        TEXT NOT NULL,
			ts                  TEXT NOT NULL,
			model               TEXT,
			model_family        TEXT,
			input_tokens        INTEGER,
			output_tokens       INTEGER,
			cache_read_tokens   INTEGER,
			cache_creation_tokens INTEGER,
			total_tokens        INTEGER,
			session_id          TEXT,
			account_email       TEXT,
			cost_usd            REAL,
			cost_usd_micros     INTEGER,
			raw_json            TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}
