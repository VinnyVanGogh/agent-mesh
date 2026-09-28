package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:     "verify",
	Aliases: []string{"dod"},
	Short:   "Execute Definition of Done verification",
	Long:    "Runs go test -race, build verification, formatting/linting checks, and ensures git cleanliness.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("\033[1;34m[StayPoint DoD Verifier]\033[0m Starting verification...")

		steps := []struct {
			name string
			exec func() error
		}{
			{"Formatting Check (go fmt)", runGoFmt},
			{"Build Verification (go build)", runGoBuild},
			{"Test Suite with Race Detector (go test -race)", runGoTest},
			{"Git Cleanliness Check", runGitCleanliness},
		}

		for _, step := range steps {
			fmt.Printf(" -> \033[1m%s\033[0m... ", step.name)
			if err := step.exec(); err != nil {
				fmt.Printf("\033[0;31mFAILED\033[0m\n")
				fmt.Printf("\nError Details:\n%v\n", err)
				os.Exit(1)
			}
			fmt.Printf("\033[0;32mOK\033[0m\n")
		}

		fmt.Println("\n\033[1;32m[StayPoint DoD Verifier] All checks passed successfully!\033[0m")
	},
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}

func runGoFmt() error {
	cmd := exec.Command("go", "fmt", "./...")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s\n%w", string(output), err)
	}
	if len(strings.TrimSpace(string(output))) > 0 {
		return fmt.Errorf("files need formatting: \n%s", string(output))
	}
	return nil
}

func runGoBuild() error {
	cmd := exec.Command("go", "build", "./...")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s\n%w", string(output), err)
	}
	return nil
}

func runGoTest() error {
	cmd := exec.Command("go", "test", "-race", "./...")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s\n%w", string(output), err)
	}
	return nil
}

func runGitCleanliness() error {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s\n%w", string(output), err)
	}
	if len(strings.TrimSpace(string(output))) > 0 {
		return fmt.Errorf("git working directory is not clean. Uncommitted changes:\n%s", string(output))
	}
	return nil
}
