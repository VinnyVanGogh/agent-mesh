package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/wire"
	"github.com/spf13/cobra"
)

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Real-time terminal feed of incoming wire broadcasts and cross-agent mentions",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		fmt.Println("\033[1;36m[Staypoint Inbox Feed]\033[0m")
		fmt.Println("Listening for incoming wire broadcasts and directives... (Ctrl+C to stop)")

		lastID := int64(0)

		// Get the last ID to only show new messages
		msgs, err := wire.List(store.DB(), "", 1)
		if err == nil && len(msgs) > 0 {
			lastID = msgs[0].ID
		}

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-sigChan:
				fmt.Println("\nExiting inbox feed.")
				return
			case <-ticker.C:
				newMsgs, err := wire.List(store.DB(), "", 50)
				if err != nil {
					continue
				}

				// reverse to print oldest first among the new
				for i := len(newMsgs)/2 - 1; i >= 0; i-- {
					opp := len(newMsgs) - 1 - i
					newMsgs[i], newMsgs[opp] = newMsgs[opp], newMsgs[i]
				}

				for _, m := range newMsgs {
					if m.ID > lastID {
						ts := m.CreatedAt
						if t, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err == nil {
							ts = t.Local().Format("15:04:05")
						}
						fmt.Printf("\033[1;32m[NEW]\033[0m \033[1;34m[%s]\033[0m \033[1m<%s>\033[0m (%s): %s\n", m.Channel, m.Author, ts, m.Content)
						lastID = m.ID
					}
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(inboxCmd)
}
