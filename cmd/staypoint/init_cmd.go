package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/db"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize staypoint directories and SQLite storage engine",
	Run: func(cmd *cobra.Command, args []string) {
		shellFlag, _ := cmd.Flags().GetBool("shell")
		if shellFlag {
			fmt.Print(`# Staypoint Shell Integration
# Add to ~/.zshrc or ~/.bashrc: eval "$(staypoint init --shell)"

alias ai-status="staypoint status"
alias ai-memo="staypoint report --pdf --type work"
alias ai-report="staypoint report --pdf --type combined"
alias ai-personal="staypoint report --pdf --type personal"
alias ai-gemini="staypoint report --pdf --type gemini"
alias ai-all="staypoint report --pdf --type all"
alias agy-status="staypoint statusline"
alias ai-shot="staypoint screenshot"
alias ai-snap="staypoint screenshot -i"
alias ai-pull-shot="staypoint screenshot --pull"
alias ai-scp="staypoint scp"

ai() {
  eval "$(staypoint route "$PWD" --eval 2>/dev/null)"
  local TARGET_MODEL="${STAYPOINT_ROUTE_MODEL:-${MESH_ROUTE_MODEL:-gemini-3.8-flash-high}}"
  local TARGET_CMD="${STAYPOINT_ROUTE_COMMAND:-${MESH_ROUTE_COMMAND:-agy}}"

  echo -e "\033[1;36m[Staypoint]\033[0m Target: \033[1;32m$TARGET_MODEL\033[0m ($TARGET_CMD)"
  echo -e "\033[0;33m[Context]\033[0m ${STAYPOINT_ROUTE_REASON:-$MESH_ROUTE_REASON}"

  staypoint statusline

  if [[ "${STAYPOINT_ROUTE_TARGET:-$MESH_ROUTE_TARGET}" == "remote-claude" ]]; then
    staypoint bridge launch "$PWD" "$@"
  elif [[ "$TARGET_CMD" == "claude" ]]; then
    command claude "$@"
  else
    command agy --model "$TARGET_MODEL" "$@"
  fi
}

claude() {
  local force=false
  local clean_args=()
  for arg in "$@"; do
    if [[ "$arg" == "--force" ]]; then
      force=true
    else
      clean_args+=("$arg")
    fi
  done

  if [[ "$force" == true ]]; then
    command claude "${clean_args[@]}"
  else
    staypoint --claude "${clean_args[@]}"
  fi
}

agy() {
  local force=false
  local clean_args=()
  for arg in "$@"; do
    if [[ "$arg" == "--force" ]]; then
      force=true
    else
      clean_args+=("$arg")
    fi
  done

  if [[ "$force" == true ]]; then
    command agy "${clean_args[@]}"
  else
    staypoint --gemini "${clean_args[@]}"
  fi
}
`)
			return
		}

		if err := config.EnsureDataDir(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating data dir: %v\n", err)
			os.Exit(1)
		}

		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing staypoint.db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		fmt.Printf("\033[1;32m✔ Staypoint initialized at: %s\033[0m\n", cfg.DataDir)
		fmt.Printf("✔ SQLite database active with WAL mode: %s\n", cfg.DBPath)

		hooksFlag, _ := cmd.Flags().GetBool("hooks")
		if hooksFlag {
			installHooks()
		} else {
			fmt.Println("\nTo install shell aliases & auto-router, add this to your ~/.zshrc:")
			fmt.Println("  \033[1;36meval \"$(staypoint init --shell)\"\033[0m")
			fmt.Println("\nOptional: To install cross-agent review and prompt hooks for Antigravity & Claude Code, run:")
			fmt.Println("  \033[1;36mstaypoint init --hooks\033[0m")
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().Bool("shell", false, "Print shell integration hook code for ~/.zshrc or ~/.bashrc")
	initCmd.Flags().Bool("hooks", false, "Install Antigravity and Claude Code lifecycle hooks for bidirectional review and context injection")
}
