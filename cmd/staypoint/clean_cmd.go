package main

import (
	"fmt"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/ipc"
	"github.com/VinnyVanGogh/staypoint/internal/wire"
	"github.com/spf13/cobra"
	"os"
	"os/exec"
	"strings"
	"time"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Garbage-collect stale resources",
	Long:  "Garbage-collects expired git checkpoint refs, pruned wire records, and stale IPC sockets.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("\033[1;36m[StayPoint GC]\033[0m Starting cleanup...")

		// 1. Stale IPC socket
		fmt.Print(" -> Checking IPC socket... ")
		socketPath := ipc.SocketPath()
		if _, err := os.Stat(socketPath); err == nil {
			conn, dialErr := ipc.Dial(socketPath)
			if dialErr != nil {
				// Socket file exists but no daemon responding
				_ = os.Remove(socketPath)
				fmt.Printf("\033[0;33mREMOVED\033[0m (stale)\n")
			} else {
				conn.Close()
				fmt.Printf("\033[0;32mACTIVE\033[0m (daemon running)\n")
			}
		} else {
			fmt.Printf("\033[0;90mNONE\033[0m\n")
		}

		// 2. Wire records pruning
		fmt.Print(" -> Pruning expired wire records... ")
		if cfg != nil && cfg.DBPath != "" {
			database, err := db.Open(cfg.DBPath)
			if err != nil {
				fmt.Printf("\033[0;31mFAILED\033[0m (db open error: %v)\n", err)
			} else {
				count, err := wire.Prune(database.DB())
				database.Close()
				if err != nil {
					fmt.Printf("\033[0;31mFAILED\033[0m (%v)\n", err)
				} else {
					fmt.Printf("\033[0;32mOK\033[0m (pruned %d messages)\n", count)
				}
			}
		} else {
			fmt.Printf("\033[0;33mSKIPPED\033[0m (db path not configured)\n")
		}

		// 3. Git Checkpoint references
		fmt.Print(" -> Cleaning up expired git checkpoints (older than 7 days)... ")
		cmdRefs := exec.Command("git", "for-each-ref", "--format=%(refname) %(committerdate:unix)", "refs/staypoint/checkpoints/")
		output, err := cmdRefs.CombinedOutput()
		if err != nil {
			fmt.Printf("\033[0;33mSKIPPED\033[0m (not in a git repo or error: %v)\n", err)
		} else {
			refs := strings.Split(strings.TrimSpace(string(output)), "\n")
			deleted := 0
			nowSec := time.Now().Unix()
			for _, line := range refs {
				if line == "" {
					continue
				}
				parts := strings.Split(line, " ")
				if len(parts) == 2 {
					ref := parts[0]
					var ts int64
					fmt.Sscanf(parts[1], "%d", &ts)
					// Delete if older than 7 days (604800 seconds)
					if nowSec-ts > 604800 {
						delCmd := exec.Command("git", "update-ref", "-d", ref)
						if err := delCmd.Run(); err == nil {
							deleted++
						}
					}
				}
			}
			fmt.Printf("\033[0;32mOK\033[0m (deleted %d expired refs)\n", deleted)
		}

		fmt.Println("\n\033[1;32m[StayPoint GC] Cleanup complete.\033[0m")
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
