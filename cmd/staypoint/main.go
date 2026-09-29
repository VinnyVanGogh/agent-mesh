package main

import (
	"os"
	"path/filepath"
)

func main() {
	base := filepath.Base(os.Args[0])
	if base == "gemini-paperclip-bridge" {
		os.Args = append([]string{"staypoint", "adapter", "gemini"}, os.Args[1:]...)
	} else if base == "claude-paperclip-bridge" {
		os.Args = append([]string{"staypoint", "adapter", "claude"}, os.Args[1:]...)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
