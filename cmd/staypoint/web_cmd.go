package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/spf13/cobra"
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Open the StayPoint web UI in the default browser",
	Long: `Opens the StayPoint web UI in your default browser.

On first visit the server sets a 30-day session cookie, so you can bookmark
http://127.0.0.1:<port>/ and open it directly without a token next time.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		daemonURL, _ := cmd.Flags().GetString("daemon-url")

		cfg, _ := config.LoadConfig()
		token := ""
		if cfg != nil {
			tokenPath := filepath.Join(cfg.DataDir, "auth_token")
			if data, err := os.ReadFile(tokenPath); err == nil {
				token = strings.TrimSpace(string(data))
			}
		}
		if token == "" {
			return fmt.Errorf("auth token not found — is staypointd running?\nCheck: ~/.staypoint/auth_token")
		}

		openURL := daemonURL + "/?token=" + token
		bookmarkURL := daemonURL + "/"

		fmt.Printf("Opening StayPoint web UI...\n")
		fmt.Printf("  %s\n\n", bookmarkURL)
		fmt.Printf("After this visit, bookmark %s — the token won't be needed again.\n", bookmarkURL)

		if err := exec.Command("open", openURL).Start(); err != nil {
			fmt.Printf("\nCould not auto-open browser. Visit this URL manually:\n  %s\n", openURL)
		}

		return nil
	},
}

func init() {
	webCmd.Flags().String("daemon-url", "http://127.0.0.1:41421", "StayPoint daemon base URL")
	rootCmd.AddCommand(webCmd)
}
