package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/wire"
	"github.com/spf13/cobra"
)

var wireCmd = &cobra.Command{
	Use:     "wire",
	Aliases: []string{"broadcast"},
	Short:   "Cross-agent live scratchpad and broadcast wire",
}

var wirePostCmd = &cobra.Command{
	Use:   "post [message]",
	Short: "Post a message or status update to the staypoint wire",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		channel, _ := cmd.Flags().GetString("channel")
		ttlSec, _ := cmd.Flags().GetInt("ttl")
		author, _ := cmd.Flags().GetString("author")
		if author == "" {
			author = os.Getenv("USER")
			if author == "" {
				author = "agent"
			}
		}

		cwd, _ := os.Getwd()
		msgText := strings.Join(args, " ")
		msg, err := wire.Post(store.DB(), channel, author, cwd, msgText, ttlSec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error posting to wire: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Broadcast posted\033[0m [id: %d, channel: %s, ttl: %ds]\n", msg.ID, msg.Channel, msg.TTLSeconds)
	},
}

var wireListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"read"},
	Short:   "Read recent messages from the staypoint wire",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		channel, _ := cmd.Flags().GetString("channel")
		limit, _ := cmd.Flags().GetInt("limit")

		msgs, err := wire.List(store.DB(), channel, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing wire messages: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\033[1;36m[Staypoint Wire Broadcasts]\033[0m")
		if len(msgs) == 0 {
			fmt.Println("  No active wire messages.")
			return
		}
		for _, m := range msgs {
			ts := m.CreatedAt
			if t, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err == nil {
				ts = t.Local().Format("15:04:05")
			} else if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
				ts = t.Local().Format("15:04:05")
			}
			fmt.Printf("  • \033[1;34m[%s]\033[0m \033[1m<%s>\033[0m (%s): %s\n", m.Channel, m.Author, ts, m.Content)
		}
	},
}

var wirePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Prune expired messages from the staypoint wire",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		count, err := wire.Prune(store.DB())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error pruning wire: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Pruned %d expired wire messages\033[0m\n", count)
	},
}

func init() {
	rootCmd.AddCommand(wireCmd)
	wireCmd.AddCommand(wirePostCmd)
	wireCmd.AddCommand(wireListCmd)
	wireCmd.AddCommand(wirePruneCmd)
	wirePostCmd.Flags().StringP("channel", "c", "global", "Message broadcast channel")
	wirePostCmd.Flags().IntP("ttl", "t", 86400, "Time-to-live in seconds")
	wirePostCmd.Flags().StringP("author", "a", "", "Author handle or identifier")
	wireListCmd.Flags().StringP("channel", "c", "", "Filter by channel")
	wireListCmd.Flags().BoolP("all", "a", false, "Include messages outside current repo")
	wireListCmd.Flags().IntP("limit", "l", 20, "Maximum messages to retrieve")
}
