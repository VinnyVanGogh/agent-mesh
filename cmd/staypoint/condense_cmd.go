package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/VinnyVanGogh/staypoint/internal/condenser"
	"github.com/spf13/cobra"
)

var condenseCmd = &cobra.Command{
	Use:   "condense [flags] [-- command...]",
	Short: "Zero-token error condenser: compress compiler dumps and stack traces (80-95% reduction)",
	Run: func(cmd *cobra.Command, args []string) {
		maxLines, _ := cmd.Flags().GetInt("lines")
		formatStr, _ := cmd.Flags().GetString("format")

		// If arguments provided after --, run command and filter its output
		if len(args) > 0 {
			c := exec.Command(args[0], args[1:]...)
			var combined bytes.Buffer
			c.Stdout = &combined
			c.Stderr = &combined
			err := c.Run()
			raw := combined.String()
			res, condenseErr := condenser.Condense(raw, condenser.CondenseOptions{
				Format:      condenser.Format(formatStr),
				MaxLines:    maxLines,
				ShowSavings: true,
			})
			if condenseErr == nil && len(res.Condensed) > 0 {
				fmt.Print(res.Condensed)
			} else {
				fmt.Print(raw)
			}
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				os.Exit(1)
			}
			return
		}

		// Pipe mode: read stdin
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}

		res, err := condenser.Condense(string(input), condenser.CondenseOptions{
			Format:      condenser.Format(formatStr),
			MaxLines:    maxLines,
			ShowSavings: true,
		})
		if err != nil {
			fmt.Print(string(input))
			return
		}
		fmt.Print(res.Condensed)
	},
}

func init() {
	rootCmd.AddCommand(condenseCmd)
	condenseCmd.Flags().IntP("lines", "l", 80, "Maximum output lines")
	condenseCmd.Flags().StringP("format", "f", "auto", "Log format: auto, typescript, go, python, generic")
}
