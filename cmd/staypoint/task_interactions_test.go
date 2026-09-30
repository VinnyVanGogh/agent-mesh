package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
)

func TestTaskInteractionsCLI(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_cli_interactions.db")
	cfg.DBPath = dbPath

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	task, err := meshContext.CreateTask(store.DB(), "CLI Task", "/tmp/test", "main", "personal")
	if err != nil {
		store.Close()
		t.Fatalf("failed to create task: %v", err)
	}
	store.Close()

	// 1. Test doc add command
	taskDocAddCmd.Run(taskDocAddCmd, []string{task.ID, "plan", "# My Plan Content v1"})

	store2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen test db: %v", err)
	}
	doc, err := meshContext.GetLatestTaskDocument(store2.DB(), task.ID, "plan")
	if err != nil || doc.Version != 1 {
		store2.Close()
		t.Fatalf("expected doc version 1, got %v", doc)
	}
	store2.Close()

	// 2. Test confirm command (creates confirmation interaction)
	taskConfirmCmd.Run(taskConfirmCmd, []string{task.ID, "Approve plan v1"})

	store3, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen test db: %v", err)
	}
	interactions, err := meshContext.ListInteractions(store3.DB(), task.ID)
	if err != nil || len(interactions) != 1 {
		store3.Close()
		t.Fatalf("expected 1 interaction, got %d (err: %v)", len(interactions), err)
	}
	if interactions[0].InteractionKind != meshContext.KindRequestConfirmation {
		store3.Close()
		t.Fatalf("expected request_confirmation kind, got %s", interactions[0].InteractionKind)
	}

	// 3. Test interactions list command
	taskInteractionsCmd.Run(taskInteractionsCmd, []string{task.ID})

	// 4. Update document to v2 and verify stale confirmation rejection
	if err := meshContext.AddTaskDocument(store3.DB(), task.ID, "plan", "# Updated Plan Content v2"); err != nil {
		store3.Close()
		t.Fatalf("failed to update document to v2: %v", err)
	}

	stalePayload := meshContext.RequestConfirmationPayload{
		Prompt: "Approve stale v1",
		Target: meshContext.ConfirmationTarget{
			Type:       "issue_document",
			Key:        "plan",
			RevisionId: 1,
		},
	}
	staleBytes, _ := json.Marshal(stalePayload)

	_, err = meshContext.CreateInteraction(store3.DB(), &meshContext.TaskInteraction{
		TaskID:          task.ID,
		InteractionKind: meshContext.KindRequestConfirmation,
		Payload:         string(staleBytes),
		IdempotencyKey:  "stale-key",
	})
	store3.Close()

	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale revision error, got: %v", err)
	}
}
