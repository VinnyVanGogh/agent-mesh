package main

import (
	"fmt"
	"github.com/spf13/cobra"
)

var taskE2ESampleCmd = &cobra.Command{
	Use:   "e2e-sample",
	Short: "Generate an E2E sample task for testing purposes",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("E2E Sample Task generated successfully.")
	},
}

func init() {
	taskCmd.AddCommand(taskE2ESampleCmd)
}
