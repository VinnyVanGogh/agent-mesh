package telemetry

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/vincevasile/agent-mesh/internal/bridge"
	"github.com/vincevasile/agent-mesh/internal/config"
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

type Watcher struct {
	cfg     *config.Config
	db      *sql.DB
	cursors *Cursors
	watcher *fsnotify.Watcher
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

	cursorsPath := filepath.Join(cfg.DataDir, "ingest-cursors.json")
	cursors := LoadCursors(cursorsPath)

	return &Watcher{
		cfg:     cfg,
		db:      conn,
		cursors: cursors,
		watcher: fsw,
	}, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	defer w.watcher.Close()
	defer w.db.Close()

	home, _ := os.UserHomeDir()
	claudeProjects := filepath.Join(home, ".claude", "projects")
	agyBrain := filepath.Join(home, ".gemini", "antigravity-cli", "brain")

	_ = w.addRecursiveWatch(claudeProjects)
	_ = w.addRecursiveWatch(agyBrain)

	log.Printf("[meshd] Watcher active on %s and %s", claudeProjects, agyBrain)

	saveTicker := time.NewTicker(30 * time.Second)
	defer saveTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.cursors.Save()
			return nil

		case <-saveTicker.C:
			w.cursors.Save()

		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}

			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				if strings.HasSuffix(event.Name, ".jsonl") {
					w.processFile(event.Name)
				} else {
					// Check if a new project directory was created
					if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
						_ = w.watcher.Add(event.Name)
					}
				}
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("[meshd] Watcher error: %v", err)
		}
	}
}

func (w *Watcher) addRecursiveWatch(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return w.watcher.Add(path)
		}
		return nil
	})
}

func (w *Watcher) processFile(filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	lastOffset := w.cursors.Get(filePath)
	stat, err := file.Stat()
	if err != nil || stat.Size() <= lastOffset {
		return
	}

	_, err = file.Seek(lastOffset, io.SeekStart)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(file)
	// Allow large token payload lines (up to 4MB)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var newOffset int64 = lastOffset

	for scanner.Scan() {
		line := scanner.Bytes()
		newOffset += int64(len(line)) + 1 // +1 for newline

		if len(line) == 0 {
			continue
		}

		w.ingestLine(line, filePath)
	}

	w.cursors.Set(filePath, newOffset)
}

func (w *Watcher) ingestLine(line []byte, sourcePath string) {
	var record map[string]interface{}
	if err := json.Unmarshal(line, &record); err != nil {
		return
	}

	// Look for assistant message with token usage
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

	// Session ID
	sessionID, _ := record["sessionId"].(string)
	if sessionID == "" {
		sessionID, _ = record["session_id"].(string)
	}

	// Model name
	model, _ := msg["model"].(string)

	// Timestamp
	ts, _ := record["timestamp"].(string)
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	// Directory context & account attribution
	cwd, _ := record["cwd"].(string)
	accountEmail := w.cfg.PersonalEmail

	// Classify: if work repo (from scan-repos or path), attribute to work
	if bridge.IsWorkRepo(cwd) || strings.Contains(sourcePath, "mansol") || strings.Contains(sourcePath, "partner") || strings.Contains(sourcePath, "vps-hr") {
		accountEmail = w.cfg.WorkEmail
	}

	// Idempotency key
	hasher := sha256.New()
	hasher.Write(line)
	idempotencyKey := fmt.Sprintf("hook:%s", hex.EncodeToString(hasher.Sum(nil)))

	// Insert into telemetry database
	query := `
	INSERT OR IGNORE INTO requests (
		idempotency_key, detected_via, ts, model, model_family,
		input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
		session_id, account_email, raw_json
	) VALUES (?, 'transcript', ?, ?, 'claude', ?, ?, ?, ?, ?, ?, ?, ?);
	`

	_, _ = w.db.Exec(query,
		idempotencyKey,
		ts,
		model,
		int64(inputTokens),
		int64(outputTokens),
		int64(cacheRead),
		int64(cacheCreation),
		totalTokens,
		sessionID,
		accountEmail,
		string(line),
	)
}
