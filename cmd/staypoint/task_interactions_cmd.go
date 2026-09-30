package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/ui"
	"github.com/spf13/cobra"
)

var taskInteractionsCmd = &cobra.Command{
	Use:     "interactions [id|name]",
	Aliases: []string{"cards"},
	Short:   "List all interactions and cards for a task",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		task, err := meshContext.GetTask(store.DB(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting task: %v\n", err)
			os.Exit(1)
		}

		interactions, err := meshContext.ListInteractions(store.DB(), task.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing interactions: %v\n", err)
			os.Exit(1)
		}

		if len(interactions) == 0 {
			fmt.Printf("No interactions found for task %q (%s).\n", task.Name, task.ID)
			return
		}

		fmt.Printf("\033[1;36m[Task Interactions :: %s]\033[0m\n", task.ID)
		for _, in := range interactions {
			statusColor := "\033[1;33m" // pending: yellow
			if in.Status == meshContext.InteractionStatusAccepted {
				statusColor = "\033[1;32m" // accepted: green
			} else if in.Status == meshContext.InteractionStatusRejected {
				statusColor = "\033[1;31m" // rejected: red
			} else if in.Status == meshContext.InteractionStatusSuperseded {
				statusColor = "\033[0;37m" // superseded: dim gray
			}

			idempInfo := ""
			if in.IdempotencyKey != "" {
				idempInfo = fmt.Sprintf(" (idempotency: %s)", in.IdempotencyKey)
			}

			fmt.Printf("  • #%d %s[%s]\033[0m \033[1m%s\033[0m%s\n",
				in.ID, statusColor, in.Status, in.InteractionKind, idempInfo)
			if in.ResolvedAt != nil {
				fmt.Printf("      Resolved at: %s\n", *in.ResolvedAt)
			}
			if in.Response != "" {
				fmt.Printf("      Response: %s\n", in.Response)
			}
		}
	},
}

var taskInteractCmd = &cobra.Command{
	Use:     "interact [id|name]",
	Aliases: []string{"card"},
	Short:   "Open the pending interaction card for a task in an interactive Bubble Tea modal",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		task, err := meshContext.GetTask(store.DB(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting task: %v\n", err)
			os.Exit(1)
		}

		pending, err := meshContext.GetPendingInteraction(store.DB(), task.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying pending interaction: %v\n", err)
			os.Exit(1)
		}
		if pending == nil {
			fmt.Printf("No pending interactions for task %q (%s).\n", task.Name, task.ID)
			return
		}

		switch pending.InteractionKind {
		case meshContext.KindRequestConfirmation:
			var p meshContext.RequestConfirmationPayload
			if err := json.Unmarshal([]byte(pending.Payload), &p); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to parse confirmation payload: %v\n", err)
				os.Exit(1)
			}

			latestVer := 0
			isStale := false
			if p.Target.Key != "" {
				doc, err := meshContext.GetLatestTaskDocument(store.DB(), task.ID, p.Target.Key)
				if err == nil && doc != nil {
					latestVer = doc.Version
					if p.Target.RevisionId < latestVer {
						isStale = true
					}
				}
			}

			choice, err := ui.RunConfirmationCard(p, latestVer, isStale)
			if err != nil {
				if errors.Is(err, ui.ErrInteractionCancelled) {
					fmt.Println("Confirmation card cancelled.")
					return
				}
				fmt.Fprintf(os.Stderr, "Error running confirmation card: %v\n", err)
				os.Exit(1)
			}

			status := meshContext.InteractionStatusAccepted
			if choice == ui.ConfirmationReject {
				status = meshContext.InteractionStatusRejected
			}

			resp := meshContext.ConfirmationResponse{
				Confirmed: choice == ui.ConfirmationAccept,
			}
			if _, err := meshContext.ResolveInteraction(store.DB(), pending.ID, status, resp); err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving interaction: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("\033[1;32m✔ Interaction #%d resolved as %s\033[0m\n", pending.ID, status)

		case meshContext.KindAskUserQuestions:
			var p meshContext.AskUserQuestionsPayload
			if err := json.Unmarshal([]byte(pending.Payload), &p); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to parse questions payload: %v\n", err)
				os.Exit(1)
			}

			answers, err := ui.RunAskQuestionsCard(p)
			if err != nil {
				if errors.Is(err, ui.ErrInteractionCancelled) {
					fmt.Println("Questions card cancelled.")
					return
				}
				fmt.Fprintf(os.Stderr, "Error running questions card: %v\n", err)
				os.Exit(1)
			}

			if _, err := meshContext.ResolveInteraction(store.DB(), pending.ID, meshContext.InteractionStatusAccepted, answers); err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving questions: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("\033[1;32m✔ Questions answered and interaction #%d resolved\033[0m\n", pending.ID)

		case meshContext.KindSuggestTasks:
			var p meshContext.SuggestTasksPayload
			if err := json.Unmarshal([]byte(pending.Payload), &p); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to parse suggest tasks payload: %v\n", err)
				os.Exit(1)
			}

			accepted, err := ui.RunSuggestTasksCard(p)
			if err != nil {
				if errors.Is(err, ui.ErrInteractionCancelled) {
					fmt.Println("Suggested tasks card cancelled.")
					return
				}
				fmt.Fprintf(os.Stderr, "Error running suggested tasks card: %v\n", err)
				os.Exit(1)
			}

			resp := meshContext.SuggestTasksResponse{
				AcceptedTasks: accepted,
			}
			if _, err := meshContext.ResolveInteraction(store.DB(), pending.ID, meshContext.InteractionStatusAccepted, resp); err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving suggested tasks: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("\033[1;32m✔ Accepted %d suggested task(s). Interaction #%d resolved.\033[0m\n", len(accepted), pending.ID)
		}
	},
}

var taskConfirmCmd = &cobra.Command{
	Use:   "confirm [id|name] [prompt]",
	Short: "Create a plan revision confirmation card for a task",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		task, err := meshContext.GetTask(store.DB(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting task: %v\n", err)
			os.Exit(1)
		}

		docKey, _ := cmd.Flags().GetString("doc")
		rev, _ := cmd.Flags().GetInt("rev")
		prompt := "Confirm and approve the proposed plan?"
		if len(args) > 1 {
			prompt = args[1]
		}

		if docKey == "" {
			docKey = "plan"
		}
		if rev <= 0 {
			// Find latest revision
			doc, err := meshContext.GetLatestTaskDocument(store.DB(), task.ID, docKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No %q document found for task %s: %v\n", docKey, task.ID, err)
				os.Exit(1)
			}
			rev = doc.Version
		}

		payload := meshContext.RequestConfirmationPayload{
			Prompt: prompt,
			Target: meshContext.ConfirmationTarget{
				Type:       "issue_document",
				Key:        docKey,
				RevisionId: rev,
			},
		}
		pBytes, _ := json.Marshal(payload)

		in, err := meshContext.CreateInteraction(store.DB(), &meshContext.TaskInteraction{
			TaskID:          task.ID,
			InteractionKind: meshContext.KindRequestConfirmation,
			Payload:         string(pBytes),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating confirmation: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Confirmation interaction #%d created for task %s (doc: %s, rev: %d)\033[0m\n",
			in.ID, task.ID, docKey, rev)
	},
}

var taskDocCmd = &cobra.Command{
	Use:   "doc",
	Short: "Manage versioned task documents (plan, bundle, architecture)",
}

var taskDocAddCmd = &cobra.Command{
	Use:   "add [id|name] [doc-key] [content]",
	Short: "Add or update a versioned document for a task",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		task, err := meshContext.GetTask(store.DB(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting task: %v\n", err)
			os.Exit(1)
		}

		if err := meshContext.AddTaskDocument(store.DB(), task.ID, args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "Error adding document: %v\n", err)
			os.Exit(1)
		}

		latest, _ := meshContext.GetLatestTaskDocument(store.DB(), task.ID, args[1])
		v := 1
		if latest != nil {
			v = latest.Version
		}
		fmt.Printf("\033[1;32m✔ Document %q saved as revision v%d for task %s\033[0m\n", args[1], v, task.ID)
	},
}

var taskDocListCmd = &cobra.Command{
	Use:   "list [id|name]",
	Short: "List all document revisions for a task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		task, err := meshContext.GetTask(store.DB(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting task: %v\n", err)
			os.Exit(1)
		}

		docs, err := meshContext.ListTaskDocuments(store.DB(), task.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing documents: %v\n", err)
			os.Exit(1)
		}

		if len(docs) == 0 {
			fmt.Printf("No documents found for task %s.\n", task.ID)
			return
		}

		fmt.Printf("\033[1;36m[Task Documents :: %s]\033[0m\n", task.ID)
		for _, d := range docs {
			fmt.Printf("  • \033[1m%s\033[0m (v%d, %s)\n", d.DocKey, d.Version, d.CreatedAt)
		}
	},
}

func init() {
	taskCmd.AddCommand(taskInteractionsCmd)
	taskCmd.AddCommand(taskInteractCmd)
	taskCmd.AddCommand(taskConfirmCmd)
	taskCmd.AddCommand(taskDocCmd)

	taskDocCmd.AddCommand(taskDocAddCmd)
	taskDocCmd.AddCommand(taskDocListCmd)

	taskConfirmCmd.Flags().String("doc", "plan", "Document key to bind confirmation to")
	taskConfirmCmd.Flags().Int("rev", 0, "Specific revision ID (defaults to latest)")
}
