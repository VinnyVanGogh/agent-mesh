package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/ui/board"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var boardCmd = &cobra.Command{
	Use:   "board",
	Short: "Interactive Kanban board TUI with live daemon updates and thread view",
	Long: `Launch an interactive Kanban board TUI with columns for todo, in_progress,
in_review, and done. Supports live push updates over SSE from staypointd and
detailed task thread views.

Features:
  - 4 Kanban columns (todo, in_progress, in_review, done) matching SQLite state
  - Detail Thread View with comments, deliverables, and activity log
  - Real-time reactive updates from the daemon over SSE without polling
  - In-place task stage moving and comment submission`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath, _ := cmd.Flags().GetString("db")
		daemonURL, _ := cmd.Flags().GetString("daemon-url")
		token, _ := cmd.Flags().GetString("token")
		standalone, _ := cmd.Flags().GetBool("standalone")

		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		if dbPath == "" && cfg != nil {
			dbPath = cfg.DBPath
		}
		if token == "" && cfg != nil {
			tokenPath := filepath.Join(cfg.DataDir, "auth_token")
			if data, err := os.ReadFile(tokenPath); err == nil {
				token = strings.TrimSpace(string(data))
			}
		}

		boardCfg := board.Config{
			DBPath:     dbPath,
			DaemonURL:  daemonURL,
			Token:      token,
			Standalone: standalone,
			RepoPath:   cwd,
		}

		m, err := board.NewModel(boardCfg)
		if err != nil {
			return fmt.Errorf("failed to initialize board: %w", err)
		}

		p := tea.NewProgram(
			m,
			tea.WithAltScreen(),
			tea.WithMouseCellMotion(),
		)

		if _, err := p.Run(); err != nil {
			return fmt.Errorf("board session error: %w", err)
		}
		return nil
	},
}

func init() {
	boardCmd.Flags().String("db", "", "Path to SQLite database")
	boardCmd.Flags().String("daemon-url", "http://127.0.0.1:41421", "Daemon HTTP/SSE server URL")
	boardCmd.Flags().String("token", "", "Daemon authentication token")
	boardCmd.Flags().Bool("standalone", false, "Force standalone mode without connecting to daemon SSE")

	rootCmd.AddCommand(boardCmd)
}
