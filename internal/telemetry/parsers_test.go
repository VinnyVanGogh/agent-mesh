package telemetry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	_ "modernc.org/sqlite"
)

func newParserTestWatcher(t *testing.T) (*Watcher, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite", filepath.Join(dir, "telemetry.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	_, err = conn.Exec(`CREATE TABLE requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		idempotency_key TEXT UNIQUE,
		detected_via TEXT, ts TEXT, model TEXT, model_family TEXT,
		input_tokens INTEGER, output_tokens INTEGER,
		cache_read_tokens INTEGER, cache_creation_tokens INTEGER, total_tokens INTEGER,
		session_id TEXT, account_email TEXT, raw_json TEXT
	);`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	w := &Watcher{
		cfg: &config.Config{
			MachineRole:   "personal",
			PersonalEmail: "p@example.com",
			DataDir:       dir,
		},
		db:      conn,
		cursors: LoadCursors(filepath.Join(dir, "cursors.json")),
	}
	return w, conn, dir
}

type sessionTotal struct {
	rows   int
	tokens int64
}

func totalsBySession(t *testing.T, conn *sql.DB) map[string]sessionTotal {
	t.Helper()
	rows, err := conn.Query(`SELECT session_id, COUNT(*), SUM(total_tokens) FROM requests GROUP BY session_id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	out := map[string]sessionTotal{}
	for rows.Next() {
		var id string
		var st sessionTotal
		if err := rows.Scan(&id, &st.rows, &st.tokens); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = st
	}
	return out
}

func claudeLine(sessionID, msgID, reqID string, sidechain bool, in, out int) string {
	sc := ""
	if sidechain {
		sc = `"isSidechain":true,"agentId":"a1",`
	}
	sid := ""
	if sessionID != "" {
		sid = fmt.Sprintf(`"sessionId":%q,`, sessionID)
	}
	return fmt.Sprintf(`{%s%s"requestId":%q,"timestamp":"2026-09-28T10:00:00Z","message":{"id":%q,"model":"claude-sonnet-4","usage":{"input_tokens":%d,"output_tokens":%d}}}`,
		sid, sc, reqID, msgID, in, out)
}

func TestClaudeSubagentAttributionAndCountOnce(t *testing.T) {
	parentPath := "/home/u/.claude/projects/proj/sess-parent.jsonl"
	subPath := "/home/u/.claude/projects/proj/sess-parent/subagents/agent-a1.jsonl"

	tests := []struct {
		name  string
		lines []struct{ path, line string }
		want  map[string]sessionTotal
	}{
		{
			name: "subagent tokens roll up to parent session",
			lines: []struct{ path, line string }{
				{parentPath, claudeLine("sess-parent", "m1", "r1", false, 100, 50)},
				{subPath, claudeLine("sess-parent", "m2", "r2", true, 10, 5)},
			},
			want: map[string]sessionTotal{"sess-parent": {2, 165}},
		},
		{
			name: "subagent file without sessionId derives parent from path",
			lines: []struct{ path, line string }{
				{subPath, claudeLine("", "m3", "r3", false, 7, 3)},
			},
			want: map[string]sessionTotal{"sess-parent": {1, 10}},
		},
		{
			name: "sidechain call echoed in parent transcript counted once",
			lines: []struct{ path, line string }{
				{subPath, claudeLine("sess-parent", "m4", "r4", true, 20, 10)},
				{parentPath, claudeLine("sess-parent", "m4", "r4", true, 20, 10)},
			},
			want: map[string]sessionTotal{"sess-parent": {1, 30}},
		},
		{
			name: "same message split over content-block lines counted once",
			lines: []struct{ path, line string }{
				{parentPath, claudeLine("sess-parent", "m5", "r5", false, 40, 20)},
				{parentPath, claudeLine("sess-parent", "m5", "r5", false, 40, 20) + " "},
				{parentPath, strings.Replace(claudeLine("sess-parent", "m5", "r5", false, 40, 20), "2026-09-28T10:00:00Z", "2026-09-28T10:00:01Z", 1)},
			},
			want: map[string]sessionTotal{"sess-parent": {1, 60}},
		},
		{
			name: "distinct messages with identical usage both counted",
			lines: []struct{ path, line string }{
				{parentPath, claudeLine("sess-parent", "m6", "r6", false, 5, 5)},
				{parentPath, claudeLine("sess-parent", "m7", "r7", false, 5, 5)},
			},
			want: map[string]sessionTotal{"sess-parent": {2, 20}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, conn, _ := newParserTestWatcher(t)
			for _, l := range tc.lines {
				w.ingestLine([]byte(l.line), l.path)
			}
			got := totalsBySession(t, conn)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// Re-reading the whole transcript (cursor reset, daemon restart) must not inflate totals.
func TestDoubleCountRegressionReingest(t *testing.T) {
	w, conn, _ := newParserTestWatcher(t)
	subPath := "/home/u/.claude/projects/proj/sess-parent/subagents/agent-a1.jsonl"
	lines := []string{
		claudeLine("sess-parent", "m1", "r1", false, 100, 50),
		claudeLine("sess-parent", "m2", "r2", true, 10, 5),
	}
	for i := 0; i < 3; i++ {
		for _, l := range lines {
			w.ingestLine([]byte(l), subPath)
		}
	}
	got := totalsBySession(t, conn)["sess-parent"]
	if got.rows != 2 || got.tokens != 165 {
		t.Fatalf("expected 2 rows/165 tokens, got %+v", got)
	}
}

func codexMeta(id, forkedFrom string, subagent bool) string {
	extra := ""
	if forkedFrom != "" {
		extra += fmt.Sprintf(`,"forked_from_id":%q`, forkedFrom)
	}
	if subagent {
		extra += `,"thread_source":"subagent"`
	}
	return fmt.Sprintf(`{"timestamp":"2026-09-28T10:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/work/repo"%s}}`, id, extra)
}

const codexTurn = `{"timestamp":"2026-09-28T10:00:01Z","type":"turn_context","payload":{"cwd":"/work/repo","model":"gpt-5-codex"}}`

func codexTokens(ts string, in, cached, out, total int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}}`,
		ts, total, in, cached, out, in+out)
}

func TestCodexParser(t *testing.T) {
	w, conn, dir := newParserTestWatcher(t)
	path := filepath.Join(dir, ".codex", "sessions", "2026", "09", "rollout-a.jsonl")

	for _, l := range []string{
		codexMeta("cx-root", "", false),
		codexTurn,
		`{"timestamp":"2026-09-28T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":null}}`,
		codexTokens("2026-09-28T10:00:03Z", 1000, 400, 100, 1100),
		codexTokens("2026-09-28T10:00:03Z", 1000, 400, 100, 1100), // repeated event, same cumulative total
		codexTokens("2026-09-28T10:00:09Z", 500, 0, 50, 1650),
	} {
		w.ingestLine([]byte(l), path)
	}

	var in, cacheRead, out, total int64
	var family, model, session string
	if err := conn.QueryRow(`SELECT SUM(input_tokens), SUM(cache_read_tokens), SUM(output_tokens), SUM(total_tokens), model_family, model, session_id FROM requests`).
		Scan(&in, &cacheRead, &out, &total, &family, &model, &session); err != nil {
		t.Fatalf("query: %v", err)
	}
	// input is stored net of cached tokens; total must equal OpenAI input+output.
	if in != 600+500 || cacheRead != 400 || out != 150 || total != 1650 {
		t.Fatalf("tokens in=%d cache=%d out=%d total=%d", in, cacheRead, out, total)
	}
	if family != "openai" || model != "gpt-5-codex" || session != "cx-root" {
		t.Fatalf("family=%s model=%s session=%s", family, model, session)
	}
}

func TestCodexSubagentForkReplayCountedOnce(t *testing.T) {
	w, conn, dir := newParserTestWatcher(t)
	root := filepath.Join(dir, ".codex", "sessions", "2026", "09", "rollout-root.jsonl")
	child := filepath.Join(dir, ".codex", "sessions", "2026", "09", "rollout-child.jsonl")
	grand := filepath.Join(dir, ".codex", "sessions", "2026", "09", "rollout-grand.jsonl")

	parentEvent := codexTokens("2026-09-28T10:00:03Z", 1000, 400, 100, 1100)
	for _, l := range []string{codexMeta("cx-root", "", false), codexTurn, parentEvent} {
		w.ingestLine([]byte(l), root)
	}
	// The forked subagent replays the parent's history, then does its own work.
	for _, l := range []string{
		codexMeta("cx-child", "cx-root", true),
		codexTurn,
		parentEvent,
		codexTokens("2026-09-28T10:05:00Z", 200, 0, 30, 230),
	} {
		w.ingestLine([]byte(l), child)
	}
	// Grandchild forks the child and replays both.
	for _, l := range []string{
		codexMeta("cx-grand", "cx-child", true),
		codexTurn,
		parentEvent,
		codexTokens("2026-09-28T10:05:00Z", 200, 0, 30, 230),
		codexTokens("2026-09-28T10:06:00Z", 60, 0, 40, 100),
	} {
		w.ingestLine([]byte(l), grand)
	}

	got := totalsBySession(t, conn)
	want := map[string]sessionTotal{"cx-root": {3, 1100 + 230 + 100}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCodexResumeFromCursorPrimesState(t *testing.T) {
	w, conn, dir := newParserTestWatcher(t)
	path := filepath.Join(dir, ".codex", "sessions", "2026", "09", "rollout-resume.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	head := codexMeta("cx-resume", "", false) + "\n" + codexTurn + "\n"
	if err := os.WriteFile(path, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	w.cursors.Set(path, int64(len(head))) // daemon restarted after head was processed

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(codexTokens("2026-09-28T10:00:03Z", 90, 0, 10, 100) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	w.processFile(path)
	got := totalsBySession(t, conn)
	if got["cx-resume"].tokens != 100 {
		t.Fatalf("expected 100 tokens on cx-resume, got %v", got)
	}
}

func TestCursorParser(t *testing.T) {
	base := "/home/u/.cursor/projects/my-proj/agent-transcripts"
	tests := []struct {
		name string
		path string
		line string
		want map[string]sessionTotal
	}{
		{
			name: "transcript line without usage records nothing",
			path: base + "/sess-c.jsonl",
			line: `{"role":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			want: map[string]sessionTotal{},
		},
		{
			name: "usage line is attributed to transcript session",
			path: base + "/sess-c.jsonl",
			line: `{"role":"assistant","id":"e1","message":{"model":"claude-sonnet-4","usage":{"input_tokens":30,"output_tokens":12}}}`,
			want: map[string]sessionTotal{"sess-c": {1, 42}},
		},
		{
			name: "subagent transcript rolls up to parent",
			path: base + "/sess-c/subagents/agent-s1.jsonl",
			line: `{"role":"assistant","id":"e2","usage":{"prompt_tokens":8,"completion_tokens":2}}`,
			want: map[string]sessionTotal{"sess-c": {1, 10}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, conn, _ := newParserTestWatcher(t)
			w.ingestLine([]byte(tc.line), tc.path)
			w.ingestLine([]byte(tc.line), tc.path) // duplicate delivery
			got := totalsBySession(t, conn)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestDetectSourceAndShouldWatchDir(t *testing.T) {
	src := map[string]string{
		"/h/.codex/sessions/2026/r.jsonl":                 sourceCodex,
		"/h/.cursor/projects/p/agent-transcripts/a.jsonl": sourceCursor,
		"/h/.cursor/projects/p/terminals/a.jsonl":         sourceClaude,
		"/h/.claude/projects/p/s/subagents/agent-1.jsonl": sourceClaude,
	}
	for p, want := range src {
		if got := detectSource(p); got != want {
			t.Errorf("detectSource(%s)=%s want %s", p, got, want)
		}
	}
	dirs := map[string]bool{
		"/h/.claude/projects/p":                       true,
		"/h/.cursor/projects/p":                       true,
		"/h/.cursor/projects/p/terminals":             false,
		"/h/.cursor/projects/p/agent-transcripts":     true,
		"/h/.cursor/projects/p/agent-transcripts/s/x": true,
	}
	for p, want := range dirs {
		if got := shouldWatchDir(p); got != want {
			t.Errorf("shouldWatchDir(%s)=%v want %v", p, got, want)
		}
	}
}
