package board

import (
	"database/sql"
	"net/http"
)

// Config holds runtime options for the board TUI.
type Config struct {
	DBPath          string
	DaemonURL       string
	Token           string
	RepoPath        string
	Standalone      bool
	CustomDB        *sql.DB
	CustomSSEClient SSEClientInterface
	HTTPClient      *http.Client
}
