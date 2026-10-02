package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
)

func TestWireOnWake_SetsOnWake(t *testing.T) {
	// Reset global dispatcher to a clean state.
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()
	if orchestrator.GlobalDispatcher.OnWake != nil {
		t.Fatal("pre-condition: OnWake should be nil before wiring")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wireOnWake(ctx, store, os.TempDir())

	if orchestrator.GlobalDispatcher.OnWake == nil {
		t.Fatal("wireOnWake did not set GlobalDispatcher.OnWake")
	}
}
