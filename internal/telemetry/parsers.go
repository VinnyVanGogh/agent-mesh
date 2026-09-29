package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// Transcript sources understood by the watcher.
const (
	sourceClaude = "claude"
	sourceCursor = "cursor"
	sourceCodex  = "codex"
)

// usageRecord is one normalized LLM usage event extracted from a transcript line.
//
// Count-once invariant: DedupKey identifies the underlying API call, not the
// line it was read from. The same call can surface in several places (a Claude
// message split over content-block lines, a sidechain echoed into the parent
// transcript, a Codex fork replaying its parent's history). Every such copy
// yields the same DedupKey, so INSERT OR IGNORE counts the call exactly once.
type usageRecord struct {
	Source      string
	SessionID   string // root session the tokens are attributed to (parent for subagents)
	AgentID     string // subagent id, empty for the root agent
	IsSidechain bool
	Model       string
	Timestamp   string
	CWD         string
	Input       int64
	Output      int64
	CacheRead   int64
	CacheCreate int64
	DedupKey    string
}

func (u usageRecord) total() int64 { return u.Input + u.Output + u.CacheRead + u.CacheCreate }

// fileState carries per-file context for formats where usage lines do not repeat
// session metadata (Codex rollouts).
type fileState struct {
	sessionID string
	rootID    string
	agentID   string
	isSub     bool
	model     string
	cwd       string
	lastTotal int64
}

func detectSource(path string) string {
	p := filepath.ToSlash(path)
	switch {
	case strings.Contains(p, "/.codex/sessions/"):
		return sourceCodex
	case strings.Contains(p, "/.cursor/projects/") && strings.Contains(p, "/agent-transcripts/"):
		return sourceCursor
	default:
		return sourceClaude
	}
}

// subagentPathInfo extracts the parent session id and agent id from a subagent
// transcript path such as <parent-session>/subagents/agent-<id>.jsonl.
func subagentPathInfo(path string) (parent, agentID string, ok bool) {
	p := filepath.ToSlash(path)
	idx := strings.LastIndex(p, "/subagents/")
	if idx < 0 {
		return "", "", false
	}
	parent = filepath.Base(p[:idx])
	base := strings.TrimSuffix(filepath.Base(p), ".jsonl")
	agentID = strings.TrimPrefix(base, "agent-")
	return parent, agentID, true
}

func num(m map[string]interface{}, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k].(float64); ok {
			return int64(v)
		}
	}
	return 0
}

func hashKey(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return prefix + ":" + hex.EncodeToString(h[:])
}

// parseClaudeUsage handles Claude Code transcripts, including sidechain and
// subagents/agent-*.jsonl files. Tokens from a subagent are attributed to the
// parent session; the parent's own transcript only records the Task tool_use and
// tool_result (never the child's usage), and any sidechain line echoed there
// shares the message id and therefore the DedupKey.
func parseClaudeUsage(record map[string]interface{}, sourcePath string, line []byte) (usageRecord, bool) {
	msg, ok := record["message"].(map[string]interface{})
	if !ok {
		return usageRecord{}, false
	}
	usage, ok := msg["usage"].(map[string]interface{})
	if !ok {
		return usageRecord{}, false
	}
	u := usageRecord{
		Source:      sourceClaude,
		Input:       num(usage, "input_tokens"),
		Output:      num(usage, "output_tokens"),
		CacheRead:   num(usage, "cache_read_input_tokens"),
		CacheCreate: num(usage, "cache_creation_input_tokens"),
	}
	u.Model, _ = msg["model"].(string)
	if u.Model == "" {
		u.Model, _ = record["model"].(string)
	}
	u.Timestamp, _ = record["timestamp"].(string)
	u.CWD, _ = record["cwd"].(string)

	u.SessionID = firstString(record, "sessionId", "session_id")
	u.IsSidechain, _ = record["isSidechain"].(bool)
	u.AgentID, _ = record["agentId"].(string)
	if parent, agent, ok := subagentPathInfo(sourcePath); ok {
		u.IsSidechain = true
		if u.AgentID == "" {
			u.AgentID = agent
		}
		if u.SessionID == "" {
			u.SessionID = parent
		}
	}

	msgID, _ := msg["id"].(string)
	reqID, _ := record["requestId"].(string)
	if msgID != "" {
		u.DedupKey = "claude:" + msgID + ":" + reqID
	} else {
		sum := sha256.Sum256(line)
		u.DedupKey = "hook:" + hex.EncodeToString(sum[:])
	}
	return u, true
}

// parseCursorUsage handles ~/.cursor/projects/<slug>/agent-transcripts/**.jsonl.
// Cursor transcripts are {role, message:{content}} lines that usually carry no
// token counts; usage is only emitted when a line actually provides it, so no
// tokens are ever fabricated. Subagent transcripts
// (agent-transcripts/<parent>/subagents/<id>.jsonl) are attributed to the parent.
func parseCursorUsage(record map[string]interface{}, sourcePath string, line []byte) (usageRecord, bool) {
	u := usageRecord{Source: sourceCursor}
	u.SessionID = strings.TrimSuffix(filepath.Base(sourcePath), ".jsonl")
	if parent, agent, ok := subagentPathInfo(sourcePath); ok {
		u.SessionID, u.AgentID, u.IsSidechain = parent, agent, true
	}

	var usage map[string]interface{}
	if msg, ok := record["message"].(map[string]interface{}); ok {
		usage, _ = msg["usage"].(map[string]interface{})
		u.Model, _ = msg["model"].(string)
	}
	if usage == nil {
		usage, _ = record["usage"].(map[string]interface{})
	}
	if usage == nil {
		return u, false
	}
	if u.Model == "" {
		u.Model, _ = record["model"].(string)
	}
	u.Input = num(usage, "input_tokens", "prompt_tokens", "inputTokens")
	u.Output = num(usage, "output_tokens", "completion_tokens", "outputTokens")
	u.CacheRead = num(usage, "cache_read_input_tokens", "cacheReadTokens")
	u.CacheCreate = num(usage, "cache_creation_input_tokens", "cacheWriteTokens")
	u.Timestamp, _ = record["timestamp"].(string)
	if id, _ := record["id"].(string); id != "" {
		u.DedupKey = "cursor:" + u.SessionID + ":" + id
	} else {
		u.DedupKey = hashKey("cursor", u.SessionID, string(line))
	}
	return u, true
}

// parseCodexRecord handles Codex rollout files. It updates st from session_meta
// and turn_context lines and returns usage for token_count events.
//
// Subagent threads are forks of their parent and replay the parent's history,
// including its token_count events. The DedupKey therefore deliberately excludes
// the session id: a replayed event has the same timestamp, cwd and usage as the
// original and collapses onto it. Repeated token_count events with an unchanged
// cumulative total inside a single rollout are skipped as well.
func (w *Watcher) parseCodexRecord(record map[string]interface{}, st *fileState) (usageRecord, bool) {
	typ, _ := record["type"].(string)
	payload, _ := record["payload"].(map[string]interface{})
	if payload == nil && typ == "" {
		payload = record // legacy first line: bare session header
	}

	switch typ {
	case "session_meta", "":
		if payload == nil {
			return usageRecord{}, false
		}
		id, _ := payload["id"].(string)
		if id == "" {
			return usageRecord{}, false
		}
		st.sessionID, st.rootID = id, id
		if cwd, _ := payload["cwd"].(string); cwd != "" {
			st.cwd = cwd
		}
		forked, _ := payload["forked_from_id"].(string)
		threadSrc, _ := payload["thread_source"].(string)
		_, srcIsSub := payload["source"].(map[string]interface{})
		st.isSub = forked != "" || threadSrc == "subagent" || srcIsSub
		if forked != "" {
			st.rootID = w.codexRootOf(forked)
			st.agentID = id
		}
		w.rememberCodexRoot(id, st.rootID)
		return usageRecord{}, false

	case "turn_context":
		if payload != nil {
			if m, _ := payload["model"].(string); m != "" {
				st.model = m
			}
			if cwd, _ := payload["cwd"].(string); cwd != "" {
				st.cwd = cwd
			}
		}
		return usageRecord{}, false

	case "event_msg":
		if payload == nil {
			return usageRecord{}, false
		}
		if pt, _ := payload["type"].(string); pt != "token_count" {
			return usageRecord{}, false
		}
		info, _ := payload["info"].(map[string]interface{})
		if info == nil {
			return usageRecord{}, false // rate-limit-only event
		}
		cumulative, _ := info["total_token_usage"].(map[string]interface{})
		last, _ := info["last_token_usage"].(map[string]interface{})
		if last == nil {
			return usageRecord{}, false
		}
		if cumulative != nil {
			total := num(cumulative, "total_tokens")
			if total != 0 && total == st.lastTotal {
				return usageRecord{}, false // repeated event, nothing new
			}
			st.lastTotal = total
		}

		// OpenAI input_tokens already includes cached_input_tokens, and
		// output_tokens already includes reasoning tokens.
		cached := num(last, "cached_input_tokens")
		u := usageRecord{
			Source:      sourceCodex,
			SessionID:   st.rootID,
			AgentID:     "",
			IsSidechain: st.isSub,
			Model:       st.model,
			CWD:         st.cwd,
			Input:       num(last, "input_tokens") - cached,
			Output:      num(last, "output_tokens"),
			CacheRead:   cached,
		}
		if u.Input < 0 {
			u.Input = 0
		}
		if st.isSub {
			u.AgentID = st.sessionID
		}
		u.Timestamp, _ = record["timestamp"].(string)
		u.DedupKey = hashKey("codex", u.Timestamp, u.CWD,
			fmt.Sprint(u.Input, u.Output, u.CacheRead, num(cumulative, "total_tokens")))
		return u, true
	}
	return usageRecord{}, false
}

func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, _ := m[k].(string); s != "" {
			return s
		}
	}
	return ""
}

func (w *Watcher) codexRootOf(id string) string {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if root, ok := w.codexRoots[id]; ok {
		return root
	}
	return id
}

func (w *Watcher) rememberCodexRoot(id, root string) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.codexRoots == nil {
		w.codexRoots = make(map[string]string)
	}
	w.codexRoots[id] = root
}

func (w *Watcher) fileStateFor(path string) *fileState {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.fileStates == nil {
		w.fileStates = make(map[string]*fileState)
	}
	st, ok := w.fileStates[path]
	if !ok {
		st = &fileState{}
		w.fileStates[path] = st
	}
	return st
}

func modelFamilyOf(model string) string {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "gemini"):
		return "gemini"
	case strings.Contains(lower, "gpt") || strings.Contains(lower, "codex") ||
		strings.Contains(lower, "o1") || strings.Contains(lower, "o3"):
		return "openai"
	}
	return "claude"
}
