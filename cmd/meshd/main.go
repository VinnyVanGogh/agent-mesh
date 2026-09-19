package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/vincevasile/agent-mesh/internal/config"
	"github.com/vincevasile/agent-mesh/internal/telemetry"
)

func main() {
	log.Println("[meshd] Starting Agent-Mesh Background Daemon...")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("[meshd] Failed to load config: %v", err)
	}

	if err := config.EnsureDataDir(cfg); err != nil {
		log.Fatalf("[meshd] Failed to ensure data dir: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("[meshd] Received signal %v, shutting down...", sig)
		cancel()
	}()

	// 1. Start Rate Limit Notifier
	notifier := telemetry.NewNotifier()
	go notifier.Start(ctx)
	log.Println("[meshd] Rate limit monitoring active")

	// 2. Start File Watcher & Ingestion Engine
	watcher, err := telemetry.NewWatcher(cfg)
	if err != nil {
		log.Fatalf("[meshd] Failed to initialize watcher: %v", err)
	}

	log.Println("[meshd] Background daemon ready and running")
	if err := watcher.Start(ctx); err != nil {
		log.Printf("[meshd] Watcher exited with error: %v", err)
	}

	log.Println("[meshd] Daemon shutdown complete.")
}
