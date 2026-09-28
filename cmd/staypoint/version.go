package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the staypoint version",
	Run: func(cmd *cobra.Command, args []string) {
		if commit != "" && commit != "none" {
			fmt.Printf("staypoint version %s (%s, built %s)\n", version, commit, date)
		} else {
			fmt.Printf("staypoint version %s\n", version)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
