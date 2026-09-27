package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/bridge"
	"github.com/VinnyVanGogh/staypoint/internal/config"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/wire"
	"github.com/fsnotify/fsnotify"
	_ "modernc.org/sqlite"
)

type Cursors struct {
	mu      sync.Mutex
	path    string
	Offsets map[string]int64 `json:"offsets"`
}

func LoadCursors(path string) *Cursors {
	c := &Cursors{
		path:    path,
		Offsets: make(map[string]int64),
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &c.Offsets)
	}
	return c
}

func (c *Cursors) Save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.MarshalIndent(c.Offsets, "", "  ")
	if err == nil {
		_ = os.WriteFile(c.path, data, 0644)
	}
}

func (c *Cursors) Get(filePath string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Offsets[filePath]
}

func (c *Cursors) Set(filePath string, offset int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Offsets[filePath] = offset
}

type handoffTask struct {
	sessionID string
	cwd       string
	lastSeen  time.Time
}

type Watcher struct {
	cfg            *config.Config
	db             *sql.DB
	meshDB         *sql.DB
	cursors        *Cursors
	watcher        *fsnotify.Watcher
	breaker        *BreakerTracker
	handoffMu      sync.Mutex
	pendingHandoff map[string]handoffTask
}

func NewWatcher(cfg *config.Config) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	// Open telemetry database in WAL mode
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", cfg.TelemetryDBPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		fsw.Close()
		return nil, fmt.Errorf("failed to open telemetry db: %w", err)
	}

	var meshDB *sql.DB
	if cfg.DBPath != "" {
		if store, err := db.Open(cfg.DBPath); err == nil {
			meshDB = store.DB()
		}
	}

	cursorsPath := filepath.Join(cfg.DataDir, "ingest-cursors.json")
	cursors := LoadCursors(cursorsPath)

	return &Watcher{
		cfg:            cfg,
		db:             conn,
		meshDB:         meshDB,
		cursors:        cursors,
		watcher:        fsw,
		breaker:        NewBreakerTracker(),
		pendingHandoff: make(map[string]handoffTask),
	}, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	defer w.watcher.Close()
	defer w.db.Close()
	if w.meshDB != nil {
		defer w.meshDB.Close()
	}

	home, _ := os.UserHomeDir()
	claudeProjects := filepath.Join(home, ".claude", "projects")
	agyBrain := filepath.Join(home, ".gemini", "antigravity-cli", "brain")

	_ = w.addRecursiveWatch(claudeProjects)
	_ = w.addRecursiveWatch(agyBrain)

	slog.Info("Watcher active",
		slog.String("path", claudeProjects),
		slog.String("secondary_path", agyBrain),
	)

	saveTicker := time.NewTicker(30 * time.Second)
	defer saveTicker.Stop()

	handoffTicker := time.NewTicker(4 * time.Second)
	defer handoffTicker.Stop()

	pruneTicker := time.NewTicker(5 * time.Minute)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.cursors.Save()
			w.flushPendingHandoffs(true)
			return nil

		case <-saveTicker.C:
			w.cursors.Save()

		case <-handoffTicker.C:
			w.flushPendingHandoffs(false)

		case <-pruneTicker.C:
			if w.meshDB != nil {
				_ = PruneWorkingFiles(w.meshDB)
				_, _ = wire.Prune(w.meshDB)
			}

		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}

			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				if strings.HasSuffix(event.Name, ".jsonl") {
					slog.Debug("Detected file event", slog.String("path", event.Name), slog.String("op", event.Op.String()))
					w.processFile(event.Name)
				} else {
					// Check if a new project directory was created
					if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
						_ = w.watcher.Add(event.Name)
						slog.Debug("Added directory to watch", slog.String("path", event.Name))
					}
				}
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			slog.Error("Watcher error", slog.Any("error", err))
		}
	}
}

func (w *Watcher) flushPendingHandoffs(force bool) {
	w.handoffMu.Lock()
	var toProcess []handoffTask
	now := time.Now()
	for id, task := range w.pendingHandoff {
		if force || now.Sub(task.lastSeen) >= 3*time.Second {
			toProcess = append(toProcess, task)
			delete(w.pendingHandoff, id)
		}
	}
	w.handoffMu.Unlock()

	maxKeep := 3
	if w.cfg != nil && w.cfg.MaxHandoffsPerRepo > 0 {
		maxKeep = w.cfg.MaxHandoffsPerRepo
	}
	dataDir := ""
	if w.cfg != nil {
		dataDir = w.cfg.DataDir
	}

	for _, task := range toProcess {
		if task.cwd != "" {
			slog.Debug("Auto-generating handoff for session", slog.String("session_id", task.sessionID), slog.String("path", task.cwd))
			_, _ = meshContext.AutoGenerateHandoffForSession(task.sessionID, task.cwd, "auto_daemon", w.meshDB, dataDir, maxKeep)
		}
	}
}

func (w *Watcher) addRecursiveWatch(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Debug("Failed accessing path during walk", slog.String("path", path), slog.Any("error", err))
			return nil
		}
		if info.IsDir() {
			if err := w.watcher.Add(path); err != nil {
				slog.Debug("Failed to add watch path", slog.String("path", path), slog.Any("error", err))
			}
		}
		return nil
	})
}

func (w *Watcher) processFile(filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		slog.Warn("Failed to open file", slog.String("path", filePath), slog.Any("error", err))
		return
	}
	defer file.Close()

	lastOffset := w.cursors.Get(filePath)
	stat, err := file.Stat()
	if err != nil {
		slog.Warn("Failed to stat file", slog.String("path", filePath), slog.Any("error", err))
		return
	}
	if stat.Size() <= lastOffset {
		return
	}

	_, err = file.Seek(lastOffset, io.SeekStart)
	if err != nil {
		slog.Warn("Failed to seek file", slog.String("path", filePath), slog.Int64("offset", lastOffset), slog.Any("error", err))
		return
	}

	slog.Debug("Processing file from offset", slog.String("path", filePath), slog.Int64("offset", lastOffset))

	reader := bufio.NewReaderSize(file, 64*1024)
	const MaxLineSize = 2 * 1024 * 1024
	var newOffset int64 = lastOffset

	for {
		lineStartOffset := newOffset
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// If EOF or error is encountered before finding '\n', this is an incomplete partial line.
			// Unless the line has already exceeded MaxLineSize, preserve cursor at lineStartOffset so slow writers can finish.
			if int64(len(line)) > MaxLineSize {
				newOffset = lineStartOffset + int64(len(line))
				w.cursors.Set(filePath, newOffset)
				slog.Warn("Discarded oversized unterminated line exceeding 2MB", slog.String("path", filePath))
			} else {
				w.cursors.Set(filePath, lineStartOffset)
				slog.Debug("Partial line or EOF reached, preserving cursor at lineStartOffset",
					slog.String("path", filePath),
					slog.Int64("offset", lineStartOffset))
			}
			return
		}

		newOffset = lineStartOffset + int64(len(line))
		if len(line) > MaxLineSize {
			slog.Warn("Skipped oversized line exceeding 2MB", slog.String("path", filePath), slog.Int("bytes", len(line)))
			w.cursors.Set(filePath, newOffset)
			continue
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			w.cursors.Set(filePath, newOffset)
			continue
		}

		w.ingestLine(line, filePath)
		w.cursors.Set(filePath, newOffset)
	}
}

func (w *Watcher) ingestLine(line []byte, sourcePath string) {
	var record map[string]interface{}
	if err := json.Unmarshal(line, &record); err != nil {
		slog.Debug("Failed to unmarshal JSON line", slog.String("path", sourcePath), slog.Any("error", err))
		return
	}

	// 1. Session ID extraction
	sessionID, _ := record["sessionId"].(string)
	if sessionID == "" {
		sessionID, _ = record["session_id"].(string)
	}

	// 2. Directory context extraction
	cwd, _ := record["cwd"].(string)
	if cwd == "" {
		if att, ok := record["attachment"].(map[string]interface{}); ok {
			if snap, ok := att["snapshot"].(map[string]interface{}); ok {
				cwd, _ = snap["workingDirectory"].(string)
			}
		}
	}

	// 3. Model name and family
	var model string
	if msg, ok := record["message"].(map[string]interface{}); ok {
		model, _ = msg["model"].(string)
	}
	if model == "" {
		model, _ = record["model"].(string)
	}

	modelFamily := "claude"
	lowerModel := strings.ToLower(model)
	if strings.Contains(lowerModel, "gemini") {
		modelFamily = "gemini"
	} else if strings.Contains(lowerModel, "gpt") || strings.Contains(lowerModel, "o1") || strings.Contains(lowerModel, "o3") {
		modelFamily = "openai"
	}

	// 4. Process tool activity, working files, and loop breaker
	w.processRecordActivity(record, sessionID, cwd, modelFamily)

	// 5. Look for assistant message with token usage
	msg, ok := record["message"].(map[string]interface{})
	if !ok {
		return
	}

	usage, ok := msg["usage"].(map[string]interface{})
	if !ok {
		return
	}

	inputTokens, _ := usage["input_tokens"].(float64)
	outputTokens, _ := usage["output_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)

	totalTokens := int64(inputTokens + outputTokens + cacheRead + cacheCreation)
	if totalTokens == 0 {
		return
	}

	// Calculate estimated token cost
	costUSD := EstimateModelCost(model, int64(inputTokens), int64(outputTokens), int64(cacheRead), int64(cacheCreation))

	// Timestamp
	ts, _ := record["timestamp"].(string)
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	// Account attribution
	accountEmail := w.cfg.PersonalEmail
	switch strings.ToLower(strings.TrimSpace(w.cfg.MachineRole)) {
	case "work":
		accountEmail = w.cfg.WorkEmail
	case "personal":
		accountEmail = w.cfg.PersonalEmail
	default:
		if bridge.IsWorkRepo(cwd) || bridge.IsWorkRepo(sourcePath) {
			accountEmail = w.cfg.WorkEmail
		}
	}

	// Idempotency key
	hasher := sha256.New()
	hasher.Write(line)
	idempotencyKey := fmt.Sprintf("hook:%s", hex.EncodeToString(hasher.Sum(nil)))

	// Try inserting with cost_usd; fallback without cost_usd if table doesn't have it (older test schemas)
	insertWithCost := `
	INSERT OR IGNORE INTO requests (
		idempotency_key, detected_via, ts, model, model_family,
		input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
		session_id, account_email, cost_usd, raw_json
	) VALUES (?, 'transcript', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := w.db.Exec(insertWithCost,
		idempotencyKey,
		ts,
		model,
		modelFamily,
		int64(inputTokens),
		int64(outputTokens),
		int64(cacheRead),
		int64(cacheCreation),
		totalTokens,
		sessionID,
		accountEmail,
		costUSD,
		string(line),
	)
	if err != nil {
		slog.Debug("Failed to insert request with cost, attempting fallback", slog.String("path", sourcePath), slog.Any("error", err))
		// Fallback without cost_usd
		insertWithoutCost := `
		INSERT OR IGNORE INTO requests (
			idempotency_key, detected_via, ts, model, model_family,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
			session_id, account_email, raw_json
		) VALUES (?, 'transcript', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
		`
		if _, fallbackErr := w.db.Exec(insertWithoutCost,
			idempotencyKey,
			ts,
			model,
			modelFamily,
			int64(inputTokens),
			int64(outputTokens),
			int64(cacheRead),
			int64(cacheCreation),
			totalTokens,
			sessionID,
			accountEmail,
			string(line),
		); fallbackErr != nil {
			slog.Error("Failed to insert request", slog.String("path", sourcePath), slog.Any("error", fallbackErr))
		}
	}

	// 6. Record spend to active task in mesh.db
	if w.meshDB != nil && cwd != "" {
		var taskID string
		err := w.meshDB.QueryRow(`
			SELECT id FROM tasks
			WHERE status = 'active' AND (repo_path = ? OR ? LIKE repo_path || '%')
			ORDER BY updated_at DESC LIMIT 1;
		`, cwd, cwd).Scan(&taskID)
		if err == nil && taskID != "" {
			_ = meshContext.RecordTaskSpend(w.meshDB, taskID, totalTokens, costUSD, 1)
		}
	}
}

func (w *Watcher) processRecordActivity(record map[string]interface{}, sessionID, cwd, agentType string) {
	if w.meshDB == nil || sessionID == "" {
		return
	}

	if agentType == "" {
		agentType = "claude"
	}

	// 1. Session Heartbeat
	_ = HeartbeatSession(w.meshDB, AgentSession{
		ID:        sessionID,
		AgentType: agentType,
		RepoPath:  cwd,
	})

	// 2. Queue for background auto-handoff generation as work progresses
	if cwd != "" {
		w.handoffMu.Lock()
		w.pendingHandoff[sessionID] = handoffTask{
			sessionID: sessionID,
			cwd:       cwd,
			lastSeen:  time.Now(),
		}
		w.handoffMu.Unlock()
	}

	// 3. Claude message content blocks inspection
	if msg, ok := record["message"].(map[string]interface{}); ok {
		if contentSlice, ok := msg["content"].([]interface{}); ok {
			for _, item := range contentSlice {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				itemType, _ := itemMap["type"].(string)

				if itemType == "tool_use" {
					toolName, _ := itemMap["name"].(string)
					input, _ := itemMap["input"].(map[string]interface{})
					for _, key := range []string{"path", "file_path", "target_file", "file", "TargetFile", "AbsolutePath"} {
						if p, ok := input[key].(string); ok && p != "" {
							_ = RecordWorkingFile(w.meshDB, sessionID, cwd, p, "write", 15*time.Minute)
						}
					}
					if cmdStr, ok := input["command"].(string); ok && cmdStr != "" {
						_ = RecordWorkingFile(w.meshDB, sessionID, cwd, toolName, "lock", 5*time.Minute)
					}
				} else if itemType == "tool_result" {
					isError, _ := itemMap["is_error"].(bool)
					resText := fmt.Sprintf("%v", itemMap["content"])
					if isError || strings.Contains(resText, "Error:") || strings.Contains(resText, "exit status") {
						if w.breaker != nil {
							tripped, reason, bErr := w.breaker.RecordFailure(w.meshDB, sessionID, cwd, agentType, "tool", "", resText)
							if bErr != nil {
								slog.Error("Failed to record tool failure", slog.String("session_id", sessionID), slog.Any("error", bErr))
							}
							if tripped {
								slog.Warn("Circuit breaker tripped", slog.String("session_id", sessionID), slog.String("agent_type", agentType), slog.String("reason", reason))
								maxKeep := 3
								if w.cfg != nil && w.cfg.MaxHandoffsPerRepo > 0 {
									maxKeep = w.cfg.MaxHandoffsPerRepo
								}
								dataDir := ""
								if w.cfg != nil {
									dataDir = w.cfg.DataDir
								}
								_, _ = meshContext.AutoGenerateHandoffForSession(sessionID, cwd, "breaker_tripped", w.meshDB, dataDir, maxKeep)
							}
						}
					} else {
						if w.breaker != nil {
							_ = w.breaker.RecordSuccess(w.meshDB, sessionID)
						}
					}
				}
			}
		}
	}

	// 4. Antigravity brain step inspection
	if toolCalls, ok := record["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}
			args, _ := tcMap["args"].(map[string]interface{})
			for _, key := range []string{"AbsolutePath", "TargetFile", "file_path", "path"} {
				if p, ok := args[key].(string); ok && p != "" {
					_ = RecordWorkingFile(w.meshDB, sessionID, cwd, p, "write", 15*time.Minute)
				}
			}
		}
	}

	status, _ := record["status"].(string)
	if status == "ERROR" {
		errContent := fmt.Sprintf("%v", record["content"])
		if w.breaker != nil {
			tripped, reason, bErr := w.breaker.RecordFailure(w.meshDB, sessionID, cwd, "gemini", "step", "", errContent)
			if bErr != nil {
				slog.Error("Failed to record step failure", slog.String("session_id", sessionID), slog.Any("error", bErr))
			}
			if tripped {
				slog.Warn("Circuit breaker tripped", slog.String("session_id", sessionID), slog.String("agent_type", "gemini"), slog.String("reason", reason))
			}
			maxKeep := 3
			if w.cfg != nil && w.cfg.MaxHandoffsPerRepo > 0 {
				maxKeep = w.cfg.MaxHandoffsPerRepo
			}
			dataDir := ""
			if w.cfg != nil {
				dataDir = w.cfg.DataDir
			}
			trigger := "crash"
			if tripped {
				trigger = "breaker_tripped"
			}
			_, _ = meshContext.AutoGenerateHandoffForSession(sessionID, cwd, trigger, w.meshDB, dataDir, maxKeep)
		}
	} else if status == "DONE" && record["tool_calls"] != nil {
		if w.breaker != nil {
			_ = w.breaker.RecordSuccess(w.meshDB, sessionID)
		}
	}
}
