package telemetry

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	_ "modernc.org/sqlite"
)

func newCostTestWatcher(t *testing.T, schema string) (*Watcher, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return &Watcher{
		cfg:     &config.Config{PersonalEmail: "p@x.com", MachineRole: "personal", DataDir: dir},
		db:      conn,
		cursors: LoadCursors(filepath.Join(dir, "c.json")),
	}, conn
}

const fullRequestsSchema = `CREATE TABLE requests (
	id INTEGER PRIMARY KEY AUTOINCREMENT, idempotency_key TEXT UNIQUE, detected_via TEXT, ts TEXT,
	model TEXT, model_family TEXT, input_tokens INTEGER, output_tokens INTEGER,
	cache_read_tokens INTEGER, cache_creation_tokens INTEGER, total_tokens INTEGER,
	session_id TEXT, account_email TEXT, cost_usd REAL, cost_usd_micros INTEGER, raw_json TEXT);`

func TestIngestStoresIntegerMicroCost(t *testing.T) {
	w, conn := newCostTestWatcher(t, fullRequestsSchema)

	// sonnet-4-5: 1000*3 + 2000*15 + 10000*0.3 + 3000(5m)*3.75 + 1000(1h)*6 micro-USD
	line := `{"sessionId":"s","message":{"model":"claude-sonnet-4-5","usage":{
		"input_tokens":1000,"output_tokens":2000,"cache_read_input_tokens":10000,
		"cache_creation_input_tokens":4000,
		"cache_creation":{"ephemeral_5m_input_tokens":3000,"ephemeral_1h_input_tokens":1000}}}}`
	w.ingestLine([]byte(line), "/p/t.jsonl")

	var micros int64
	var usd float64
	if err := conn.QueryRow("SELECT cost_usd_micros, cost_usd FROM requests").Scan(&micros, &usd); err != nil {
		t.Fatal(err)
	}
	want := int64(3000 + 30000 + 3000 + 11250 + 6000)
	if micros != want {
		t.Errorf("cost_usd_micros = %d, want %d", micros, want)
	}
	if math.Abs(usd-float64(want)/1e6) > 1e-12 {
		t.Errorf("cost_usd = %v, want %v", usd, float64(want)/1e6)
	}
}

func TestIngestFallsBackOnLegacySchema(t *testing.T) {
	// No cost columns at all: the row must still be recorded.
	w, conn := newCostTestWatcher(t, `CREATE TABLE requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT, idempotency_key TEXT UNIQUE, detected_via TEXT, ts TEXT,
		model TEXT, model_family TEXT, input_tokens INTEGER, output_tokens INTEGER,
		cache_read_tokens INTEGER, cache_creation_tokens INTEGER, total_tokens INTEGER,
		session_id TEXT, account_email TEXT, raw_json TEXT);`)
	w.ingestLine([]byte(`{"sessionId":"s","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":10}}}`), "/p/t.jsonl")
	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM requests").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, err = %v", n, err)
	}
}

func TestIngestUnknownModelKeepsLegacyEstimate(t *testing.T) {
	w, conn := newCostTestWatcher(t, fullRequestsSchema)
	w.ingestLine([]byte(`{"sessionId":"s","message":{"model":"claude-3-7-sonnet","usage":{"input_tokens":1000000,"output_tokens":1000000}}}`), "/p/t.jsonl")
	var micros int64
	if err := conn.QueryRow("SELECT cost_usd_micros FROM requests").Scan(&micros); err != nil {
		t.Fatal(err)
	}
	if micros != 18_000_000 {
		t.Errorf("legacy estimate micros = %d, want 18000000", micros)
	}
}
