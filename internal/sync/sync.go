package sync

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/bridge"
	"github.com/VinnyVanGogh/agent-mesh/internal/config"
	meshContext "github.com/VinnyVanGogh/agent-mesh/internal/context"
	_ "modernc.org/sqlite"
)

// SyncResult captures the summary of a sync operation.
type SyncResult struct {
	Host             string        `json:"host"`
	Duration         time.Duration `json:"duration"`
	ClaudeTranscripts int          `json:"claude_transcripts"`
	BrainLogs        int           `json:"brain_logs"`
	RecordsIngested  int64         `json:"records_ingested"`
}

// PullTranscripts pulls remote transcripts from host over rsync/SSH and triggers local DB ingestion.
func PullTranscripts(ctx context.Context, host string, cfg *config.Config) (*SyncResult, error) {
	if host == "" {
		host = cfg.RemoteHost
	}
	if host == "" {
		host = "company-mbp"
	}

	start := time.Now()

	// Probe host first
	probe := bridge.ProbeSSH(ctx, host, 3*time.Second)
	if !probe.Reachable {
		return nil, fmt.Errorf("remote host '%s' unreachable (%s)", host, probe.Error)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// 1. Rsync Claude Code transcripts
	localClaudeDir := filepath.Join(home, ".claude", "projects")
	_ = os.MkdirAll(localClaudeDir, 0755)
	remoteClaudeDir := fmt.Sprintf("%s:~/.claude/projects/", host)

	claudeCmd := exec.CommandContext(ctx, "rsync", "-az", "--update", "--exclude=*.lock", remoteClaudeDir, localClaudeDir)
	_ = claudeCmd.Run()

	// 2. Rsync Antigravity brain logs
	localBrainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	_ = os.MkdirAll(localBrainDir, 0755)
	remoteBrainDir := fmt.Sprintf("%s:~/.gemini/antigravity-cli/brain/", host)

	brainCmd := exec.CommandContext(ctx, "rsync", "-az", "--update", remoteBrainDir, localBrainDir)
	_ = brainCmd.Run()

	// 3. Ingest into telemetry DB
	inserted, err := IngestTranscripts(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest synced transcripts: %w", err)
	}

	return &SyncResult{
		Host:            host,
		Duration:        time.Since(start),
		RecordsIngested: inserted,
	}, nil
}

// IngestTranscripts scans local transcript files and imports unrecorded telemetry rows into SQLite.
func IngestTranscripts(cfg *config.Config) (int64, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", cfg.TelemetryDBPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, fmt.Errorf("failed to open telemetry db: %w", err)
	}
	defer db.Close()

	home, _ := os.UserHomeDir()
	claudeProjects := filepath.Join(home, ".claude", "projects")

	var insertedCount int64

	_ = filepath.Walk(claudeProjects, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var record map[string]interface{}
			if err := json.Unmarshal(line, &record); err != nil {
				continue
			}

			msg, ok := record["message"].(map[string]interface{})
			if !ok {
				continue
			}
			usage, ok := msg["usage"].(map[string]interface{})
			if !ok {
				continue
			}

			inputTokens, _ := usage["input_tokens"].(float64)
			outputTokens, _ := usage["output_tokens"].(float64)
			cacheRead, _ := usage["cache_read_input_tokens"].(float64)
			cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)
			totalTokens := int64(inputTokens + outputTokens + cacheRead + cacheCreation)
			if totalTokens == 0 {
				continue
			}

			sessionID, _ := record["sessionId"].(string)
			if sessionID == "" {
				sessionID, _ = record["session_id"].(string)
			}
			model, _ := msg["model"].(string)
			ts, _ := record["timestamp"].(string)
			if ts == "" {
				ts = time.Now().UTC().Format(time.RFC3339)
			}

			cwd, _ := record["cwd"].(string)
			accountEmail := cfg.PersonalEmail

			switch strings.ToLower(strings.TrimSpace(cfg.MachineRole)) {
			case "work":
				accountEmail = cfg.WorkEmail
			case "personal":
				accountEmail = cfg.PersonalEmail
			default:
				if bridge.IsWorkRepo(cwd) {
					accountEmail = cfg.WorkEmail
				}
			}

			query := `
			INSERT OR IGNORE INTO requests (
				idempotency_key, detected_via, ts, model, model_family,
				input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
				session_id, account_email, raw_json
			) VALUES (?, 'transcript', ?, ?, 'claude', ?, ?, ?, ?, ?, ?, ?, ?);
			`

			res, err := db.Exec(query,
				fmt.Sprintf("sync:%s:%s", sessionID, ts),
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
			if err == nil {
				if ra, _ := res.RowsAffected(); ra > 0 {
					insertedCount += ra
				}
			}
		}
		return nil
	})

	return insertedCount, nil
}

// BundleMetadata describes the exported bundle.
type BundleMetadata struct {
	Version     string    `json:"version"`
	ExportedAt  time.Time `json:"exported_at"`
	Hostname    string    `json:"hostname"`
	MachineRole string    `json:"machine_role"`
	RecordCount int64     `json:"record_count"`
}

// ExportBundle creates a portable .tar.gz bundle of telemetry records for air-gapped or non-SSH transfer.
func ExportBundle(outputPath string, cfg *config.Config) (string, int64, error) {
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "node"
		}
		outputPath = filepath.Join(home, "Desktop", fmt.Sprintf("agent-mesh-telemetry-%s-%s.tar.gz", hostname, time.Now().Format("20060102-150405")))
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", cfg.TelemetryDBPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open telemetry db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT COALESCE(idempotency_key, ''), COALESCE(detected_via, ''), COALESCE(ts, ''), COALESCE(model, ''), COALESCE(model_family, ''),
		       COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(cache_read_tokens, 0), COALESCE(cache_creation_tokens, 0), COALESCE(total_tokens, 0),
		       COALESCE(session_id, ''), COALESCE(account_email, ''), COALESCE(raw_json, '')
		FROM requests ORDER BY ts ASC
	`)
	if err != nil {
		return "", 0, fmt.Errorf("failed to query requests: %w", err)
	}
	defer rows.Close()

	var records []map[string]interface{}
	for rows.Next() {
		var idKey, detectedVia, ts, model, modelFamily, sessionID, accountEmail, rawJSON string
		var inTok, outTok, cacheRead, cacheCreate, totalTok int64

		if err := rows.Scan(&idKey, &detectedVia, &ts, &model, &modelFamily,
			&inTok, &outTok, &cacheRead, &cacheCreate, &totalTok,
			&sessionID, &accountEmail, &rawJSON); err != nil {
			continue
		}

		records = append(records, map[string]interface{}{
			"idempotency_key":       idKey,
			"detected_via":          detectedVia,
			"ts":                    ts,
			"model":                 model,
			"model_family":          modelFamily,
			"input_tokens":          inTok,
			"output_tokens":         outTok,
			"cache_read_tokens":     cacheRead,
			"cache_creation_tokens": cacheCreate,
			"total_tokens":          totalTok,
			"session_id":            sessionID,
			"account_email":         accountEmail,
			"raw_json":              rawJSON,
		})
	}

	hostname, _ := os.Hostname()
	meta := BundleMetadata{
		Version:     "0.1.0",
		ExportedAt:  time.Now().UTC(),
		Hostname:    hostname,
		MachineRole: cfg.MachineRole,
		RecordCount: int64(len(records)),
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create export file: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// 1. Write metadata.json
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeTarEntry(tw, "metadata.json", metaBytes); err != nil {
		return "", 0, err
	}

	// 2. Write requests.jsonl
	var jsonlBuf strings.Builder
	for _, rec := range records {
		line, _ := json.Marshal(rec)
		jsonlBuf.Write(line)
		jsonlBuf.WriteString("\n")
	}
	if err := writeTarEntry(tw, "requests.jsonl", []byte(jsonlBuf.String())); err != nil {
		return "", 0, err
	}

	return outputPath, int64(len(records)), nil
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// ImportBundle uncompresses an exported .tar.gz bundle and inserts records into local telemetry DB.
func ImportBundle(bundlePath string, cfg *config.Config) (int64, error) {
	inFile, err := os.Open(bundlePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open bundle: %w", err)
	}
	defer inFile.Close()

	gr, err := gzip.NewReader(inFile)
	if err != nil {
		return 0, fmt.Errorf("failed to read gzip bundle: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", cfg.TelemetryDBPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, fmt.Errorf("failed to open telemetry db: %w", err)
	}
	defer db.Close()

	query := `
	INSERT OR IGNORE INTO requests (
		idempotency_key, detected_via, ts, model, model_family,
		input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
		session_id, account_email, raw_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`

	var insertedCount int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return insertedCount, fmt.Errorf("tar read error: %w", err)
		}

		if hdr.Name == "requests.jsonl" {
			scanner := bufio.NewScanner(tr)
			scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

			for scanner.Scan() {
				var rec map[string]interface{}
				if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
					continue
				}

				idKey, _ := rec["idempotency_key"].(string)
				detectedVia, _ := rec["detected_via"].(string)
				ts, _ := rec["ts"].(string)
				model, _ := rec["model"].(string)
				modelFamily, _ := rec["model_family"].(string)
				inputTokens, _ := rec["input_tokens"].(float64)
				outputTokens, _ := rec["output_tokens"].(float64)
				cacheRead, _ := rec["cache_read_tokens"].(float64)
				cacheCreation, _ := rec["cache_creation_tokens"].(float64)
				totalTokens, _ := rec["total_tokens"].(float64)
				sessionID, _ := rec["session_id"].(string)
				accountEmail, _ := rec["account_email"].(string)
				rawJSON, _ := rec["raw_json"].(string)

				res, err := db.Exec(query,
					idKey, detectedVia, ts, model, modelFamily,
					int64(inputTokens), int64(outputTokens), int64(cacheRead), int64(cacheCreation), int64(totalTokens),
					sessionID, accountEmail, rawJSON,
				)
				if err == nil {
					if ra, _ := res.RowsAffected(); ra > 0 {
						insertedCount += ra
					}
				}
			}
		}
	}

	return insertedCount, nil
}

// PushHandoff pushes the local handoff context to remoteHost over SSH/scp and stages it in remote clipboard.
func PushHandoff(ctx context.Context, host string, record *meshContext.HandoffRecord) error {
	if host == "" {
		host = "company-mbp"
	}

	probe := bridge.ProbeSSH(ctx, host, 3*time.Second)
	if !probe.Reachable {
		return fmt.Errorf("remote host '%s' unreachable (%s)", host, probe.Error)
	}

	jsonData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}

	// 1. Write to remote ~/.agent-mesh/handoff.json and /tmp/ai-handoff.md
	remoteScript := fmt.Sprintf(`
mkdir -p ~/.agent-mesh
cat << 'EOF' > ~/.agent-mesh/handoff.json
%s
EOF
cat << 'EOF' > /tmp/ai-handoff.md
%s
EOF
if command -v pbcopy >/dev/null 2>&1; then
    cat /tmp/ai-handoff.md | pbcopy
fi
`, string(jsonData), record.HandoffPrompt)

	cmd := exec.CommandContext(ctx, "ssh", host, "bash -s")
	cmd.Stdin = strings.NewReader(remoteScript)
	return cmd.Run()
}

// PullHandoff retrieves the active handoff context from remoteHost over SSH and loads it into local clipboard.
func PullHandoff(ctx context.Context, host string) (*meshContext.HandoffRecord, error) {
	if host == "" {
		host = "company-mbp"
	}

	probe := bridge.ProbeSSH(ctx, host, 3*time.Second)
	if !probe.Reachable {
		return nil, fmt.Errorf("remote host '%s' unreachable (%s)", host, probe.Error)
	}

	cmd := exec.CommandContext(ctx, "ssh", host, "cat ~/.agent-mesh/handoff.json 2>/dev/null || cat /tmp/ai-handoff.md")
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil, fmt.Errorf("no active handoff record found on %s", host)
	}

	var record meshContext.HandoffRecord
	if err := json.Unmarshal(out, &record); err == nil {
		// Save locally
		home, _ := os.UserHomeDir()
		if home != "" {
			_ = os.WriteFile(filepath.Join(home, ".agent-mesh", "handoff.json"), out, 0644)
		}
		_ = os.WriteFile("/tmp/ai-handoff.md", []byte(record.HandoffPrompt), 0644)
		_ = meshContext.CopyToClipboard(record.HandoffPrompt)
		return &record, nil
	}

	// Fallback if raw text
	promptText := string(out)
	_ = os.WriteFile("/tmp/ai-handoff.md", []byte(promptText), 0644)
	_ = meshContext.CopyToClipboard(promptText)

	return &meshContext.HandoffRecord{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		HandoffPrompt: promptText,
	}, nil
}
