package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/ai"
	"github.com/VinnyVanGogh/staypoint/internal/bridge"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
	"github.com/VinnyVanGogh/staypoint/internal/ui"
)

var taskCreateCmd = &cobra.Command{
	Use:     "create [comment]",
	Aliases: []string{"generate", "new"},
	Short:   "Generate a structured engineering task using Gemini & Claude with TUI or CLI input",
	Long: `Dual-mode dynamic task generator.
In TUI mode (no arguments or --tui), launches an interactive modal textarea.
In CLI mode, accepts raw text arguments or piped stdin.
Infers structured fields via Gemini 3.8 Flash (falling back to Claude Sonnet),
dispatches to the Paperclip API, renders a markdown summary via glamour,
and outputs token telemetry alongside pacing status.`,
	RunE: runTaskCreate,
}

func init() {
	taskCmd.AddCommand(taskCreateCmd)
	taskCreateCmd.Flags().Bool("tui", false, "Force launch Bubble Tea TUI interactive textarea")
	taskCreateCmd.Flags().Bool("dry-run", false, "Infer task and render summary without dispatching to Paperclip API")
	taskCreateCmd.Flags().String("company", "", "Target Paperclip company ID (defaults to PAPERCLIP_COMPANY_ID)")
	taskCreateCmd.Flags().String("project", "", "Target project ID (defaults to current project)")
	taskCreateCmd.Flags().String("priority", "", "Override priority (low, medium, high, urgent)")
	taskCreateCmd.Flags().Float64("budget", 0.0, "Maximum budget limit in USD")
	taskCreateCmd.Flags().Int("max-turns", 0, "Maximum allowed turns")
	taskCreateCmd.Flags().Bool("ai", true, "Force dynamic AI inference")
}

func runTaskCreate(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	tuiFlag, _ := cmd.Flags().GetBool("tui")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	companyFlag, _ := cmd.Flags().GetString("company")
	projectFlag, _ := cmd.Flags().GetString("project")
	priorityOverride, _ := cmd.Flags().GetString("priority")

	var rawComment string

	// Determine whether to use TUI mode or CLI mode
	isTerminal := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())

	if tuiFlag || (len(args) == 0 && isTerminal) {
		// TUI Mode
		val, err := ui.RunTextareaModal()
		if err != nil {
			if err == ui.ErrCancelled {
				fmt.Fprintln(out, "\n\033[1;33m[StayPoint Task Generator]\033[0m Task creation cancelled.")
				return nil
			}
			return fmt.Errorf("TUI error: %w", err)
		}
		rawComment = val
	} else if len(args) > 0 {
		// CLI arguments mode
		rawComment = strings.Join(args, " ")
	} else if !isTerminal {
		// Piped stdin mode
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}
		rawComment = string(data)
	}

	rawComment = sanitizeComment(rawComment)
	if strings.TrimSpace(rawComment) == "" {
		fmt.Fprintln(out, "\033[1;33m[StayPoint Task Generator]\033[0m Empty comment provided. No task created.")
		return nil
	}

	fmt.Fprintln(out, "\033[1;36m[StayPoint :: Task Generation Engine]\033[0m Ingesting input & structuring issue...")

	// 1. Run AI Inference
	genCfg := ai.DefaultGeneratorConfig()
	generator := ai.NewGenerator(genCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	genResult, err := generator.GenerateTask(ctx, rawComment)
	if err != nil {
		return fmt.Errorf("inference error: %w", err)
	}

	if priorityOverride != "" {
		genResult.Task.Priority = strings.ToLower(priorityOverride)
	}

	// Output immediate confirmation for CLI and e2e tests
	fmt.Fprintf(out, "\033[1;32m✔ Task created:\033[0m %s\n\n", genResult.Task.Title)

	// 2. Render Markdown Summary via Glamour
	renderedCard := renderMarkdownSummary(genResult.Task)
	fmt.Fprint(out, renderedCard)

	// 3. Dispatch to Paperclip API
	paperclipClient := paperclip.NewClient("", "")
	companyID := companyFlag
	var companyPrefix string

	if companyID != "" {
		if comp, compErr := paperclipClient.GetCompany(ctx, companyID); compErr == nil && comp != nil {
			companyPrefix = comp.IssuePrefix
		}
	} else {
		// Dynamically resolve company based on inferred Organization first
		if genResult.Task.Organization != "" {
			if comp, compErr := paperclipClient.ResolveCompany(ctx, genResult.Task.Organization); compErr == nil && comp != nil {
				companyID = comp.ID
				companyPrefix = comp.IssuePrefix
			}
		}
		// If still empty, fall back to environment variable
		if companyID == "" {
			companyID = os.Getenv("PAPERCLIP_COMPANY_ID")
			if comp, compErr := paperclipClient.GetCompany(ctx, companyID); compErr == nil && comp != nil {
				companyPrefix = comp.IssuePrefix
			}
		}
	}

	projectID := projectFlag
	if projectID == "" {
		projectID = os.Getenv("PAPERCLIP_PROJECT_ID")
	}
	if projectID == "" && companyID != "" {
		if resolvedProjID, projErr := paperclipClient.ResolveProject(ctx, companyID, genResult.Task.Project); projErr == nil && resolvedProjID != "" {
			projectID = resolvedProjID
		}
	}

	var issueResp *paperclip.IssueResponse
	var issueURL string

	if !dryRun && companyID != "" {
		req := paperclip.CreateIssueRequest{
			Title:       genResult.Task.Title,
			Description: genResult.Task.Description,
			Priority:    genResult.Task.Priority,
			ProjectId:   projectID,
			Labels:      genResult.Task.Labels,
		}

		resp, err := paperclipClient.CreateIssue(ctx, companyID, req)
		if err != nil {
			fmt.Fprintf(out, "\033[1;33m⚠ Paperclip dispatch notice:\033[0m %v (saving locally)\n", err)
		} else {
			issueResp = resp
			if companyPrefix == "" {
				companyPrefix = "STA"
			}
			issueURL = paperclipClient.IssueURL(companyPrefix, issueResp.ID)
		}
	} else if dryRun {
		fmt.Fprintln(out, "\033[1;33m[Dry Run Mode]\033[0m Skipped Paperclip API dispatch.")
	} else if companyID == "" {
		fmt.Fprintln(out, "\033[1;33m⚠ Paperclip dispatch notice:\033[0m Paperclip company could not be resolved or server unreachable (saving locally).")
	}

	// 4. Save to local StayPoint DB if initialized
	if cfg != nil && cfg.DBPath != "" {
		if store, err := db.Open(cfg.DBPath); err == nil {
			defer store.Close()
			cwd, _ := os.Getwd()
			branch := meshContext.GetCurrentGitBranch(cwd)
			role := "personal"
			if bridge.IsWorkRepo(cwd) {
				role = "work"
			}
			budget, _ := cmd.Flags().GetFloat64("budget")
			maxTurns, _ := cmd.Flags().GetInt("max-turns")
			_, _ = meshContext.CreateTaskWithOptions(store.DB(), meshContext.TaskCreateOptions{
				Name:         genResult.Task.Title,
				RepoPath:     cwd,
				GitBranch:    branch,
				AccountRole:  role,
				MaxBudgetUSD: budget,
				MaxTurns:     maxTurns,
			})
		}
	}

	// 5. Output Post-Run Information & Presentation
	fmt.Fprintln(out)
	printTokenTelemetry(out, genResult)

	fmt.Fprintln(out)
	if issueResp != nil {
		printClickableLink(out, issueResp.Identifier, issueURL)
		fmt.Fprintln(out)
	}

	// 6. Fleet Status & Pacing Engine
	statusCmd.Run(cmd, nil)

	return nil
}

func sanitizeComment(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
			(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
			trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		}
	}
	return trimmed
}

func renderMarkdownSummary(task ai.InferredTask) string {
	tags := strings.Join(task.Labels, ", ")
	if tags == "" {
		tags = "none"
	}

	card := fmt.Sprintf("# %s\n\n**Organization:** %s | **Project:** %s\n**Priority:** `%s` | **Assignee Role:** `%s` | **Labels:** `%s`\n\n---\n\n%s\n",
		task.Title,
		task.Organization,
		task.Project,
		strings.ToUpper(task.Priority),
		task.AssigneeRole,
		tags,
		task.Description,
	)

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return card
	}

	out, err := renderer.Render(card)
	if err != nil {
		return card
	}
	return out
}

func printTokenTelemetry(out io.Writer, res *ai.GenerationResult) {
	fmt.Fprintln(out, "\033[1;36m[StayPoint :: Inference Telemetry & Cost Engine]\033[0m")
	modelTag := res.Model
	if res.FallbackUsed {
		modelTag += " \033[1;33m(Fallback Downshift)\033[0m"
	} else {
		modelTag += " \033[1;32m(Primary)\033[0m"
	}
	fmt.Fprintf(out, "  • Inference Engine:        %s\n", modelTag)
	fmt.Fprintf(out, "  • Input Tokens:            %d\n", res.InputTokens)
	fmt.Fprintf(out, "  • Output Tokens:           %d\n", res.OutputTokens)
	fmt.Fprintf(out, "  • Cached Tokens:           %d\n", res.CachedTokens)
	fmt.Fprintf(out, "  • Estimated Turn Cost:     \033[1;32m$%.6f USD\033[0m\n", res.EstimatedCostUSD)
}

func printClickableLink(out io.Writer, identifier, issueURL string) {
	fmt.Fprintln(out, "\033[1;36m[StayPoint :: Paperclip Issue Dispatch]\033[0m")
	fmt.Fprintf(out, "  • Issue Identifier:        \033[1;32m%s\033[0m\n", identifier)
	fmt.Fprintf(out, "  • Clickable Web Link:      \033[1;34m\033[4m%s\033[0m\n", issueURL)
}
