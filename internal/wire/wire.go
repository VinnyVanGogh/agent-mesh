package wire

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// Message represents an inter-agent broadcast or note on Mesh Wire.
type Message struct {
	ID         int64  `json:"id"`
	Channel    string `json:"channel"`
	Author     string `json:"author"`
	RepoPath   string `json:"repo_path,omitempty"`
	Content    string `json:"content"`
	TTLSeconds int    `json:"ttl_seconds"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
}

// Post broadcasts a new message to the wire.
func Post(db *sql.DB, channel, author, repoPath, content string, ttlSeconds int) (*Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("wire content cannot be empty")
	}
	if channel == "" {
		channel = "global"
	}
	if author == "" {
		author = "agent"
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 86400 // 24h default
	}

	if repoPath != "" {
		if abs, err := filepath.Abs(repoPath); err == nil {
			repoPath = filepath.Clean(abs)
		}
	}

	query := `
		INSERT INTO wire_messages (channel, author, repo_path, content, ttl_seconds, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		        strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '+' || ? || ' seconds'))
	`
	res, err := db.Exec(query, channel, author, repoPath, content, ttlSeconds, ttlSeconds)
	if err != nil {
		return nil, fmt.Errorf("failed to post wire message: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get message id: %w", err)
	}

	return Get(db, id)
}

// Get fetches a single wire message by ID.
func Get(db *sql.DB, id int64) (*Message, error) {
	query := `
		SELECT id, channel, author, repo_path, content, ttl_seconds, created_at, expires_at
		FROM wire_messages
		WHERE id = ?
	`
	var m Message
	var repo sql.NullString
	err := db.QueryRow(query, id).Scan(
		&m.ID, &m.Channel, &m.Author, &repo, &m.Content, &m.TTLSeconds, &m.CreatedAt, &m.ExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("message not found: %w", err)
	}
	if repo.Valid {
		m.RepoPath = repo.String
	}
	return &m, nil
}

// GetUnread retrieves all messages newer than consumer's cursor that match currentRepo or are global,
// and automatically advances the consumer's watermark.
func GetUnread(db *sql.DB, consumerKey, currentRepo string) ([]Message, error) {
	if consumerKey == "" {
		consumerKey = "default"
	}

	// 1. Fetch current cursor
	var lastID int64
	_ = db.QueryRow("SELECT last_read_id FROM wire_cursors WHERE consumer_key = ?", consumerKey).Scan(&lastID)

	cleanRepo := ""
	if currentRepo != "" {
		if abs, err := filepath.Abs(currentRepo); err == nil {
			cleanRepo = filepath.Clean(abs)
		}
	}

	query := `
		SELECT id, channel, author, repo_path, content, ttl_seconds, created_at, expires_at
		FROM wire_messages
		WHERE id > ?
		  AND datetime(expires_at) > datetime('now')
		  AND (repo_path IS NULL OR repo_path = '' OR ? = '' OR repo_path = ? OR ? LIKE repo_path || '/%')
		ORDER BY id ASC
		LIMIT 25;
	`
	rows, err := db.Query(query, lastID, cleanRepo, cleanRepo, cleanRepo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	var maxID = lastID

	for rows.Next() {
		var m Message
		var repo sql.NullString
		if err := rows.Scan(&m.ID, &m.Channel, &m.Author, &repo, &m.Content, &m.TTLSeconds, &m.CreatedAt, &m.ExpiresAt); err == nil {
			if repo.Valid {
				m.RepoPath = repo.String
			}
			msgs = append(msgs, m)
			if m.ID > maxID {
				maxID = m.ID
			}
		}
	}

	// 2. Advance watermark cursor if new messages were found
	if maxID > lastID {
		advanceQuery := `
			INSERT INTO wire_cursors (consumer_key, last_read_id, updated_at)
			VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
			ON CONFLICT(consumer_key) DO UPDATE SET
				last_read_id = excluded.last_read_id,
				updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');
		`
		_, _ = db.Exec(advanceQuery, consumerKey, maxID)
	}

	return msgs, nil
}

// List returns recent wire messages, optionally filtered by channel.
func List(db *sql.DB, channel string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}

	var query string
	var args []interface{}

	if channel != "" && channel != "all" {
		query = `
			SELECT id, channel, author, repo_path, content, ttl_seconds, created_at, expires_at
			FROM wire_messages
			WHERE channel = ? AND datetime(expires_at) > datetime('now')
			ORDER BY id DESC
			LIMIT ?
		`
		args = []interface{}{channel, limit}
	} else {
		query = `
			SELECT id, channel, author, repo_path, content, ttl_seconds, created_at, expires_at
			FROM wire_messages
			WHERE datetime(expires_at) > datetime('now')
			ORDER BY id DESC
			LIMIT ?
		`
		args = []interface{}{limit}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var repo sql.NullString
		if err := rows.Scan(&m.ID, &m.Channel, &m.Author, &repo, &m.Content, &m.TTLSeconds, &m.CreatedAt, &m.ExpiresAt); err == nil {
			if repo.Valid {
				m.RepoPath = repo.String
			}
			msgs = append(msgs, m)
		}
	}

	return msgs, nil
}

// Prune deletes expired wire messages.
func Prune(db *sql.DB) (int64, error) {
	query := `DELETE FROM wire_messages WHERE datetime(expires_at) <= datetime('now');`
	res, err := db.Exec(query)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
