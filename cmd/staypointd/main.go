package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/logging"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
)

func main() {
	logLevel := os.Getenv("STAYPOINT_LOG_LEVEL")
	if logLevel == "" {
		if legacy := os.Getenv("MESH_LOG_LEVEL"); legacy != "" {
			fmt.Fprintf(os.Stderr, "DEPRECATION WARNING: MESH_LOG_LEVEL is deprecated and will be removed in v0.3.0. Use STAYPOINT_LOG_LEVEL instead.\n")
			logLevel = legacy
		} else {
			logLevel = "INFO"
		}
	}
	logFormat := os.Getenv("STAYPOINT_LOG_FORMAT")
	if logFormat == "" {
		if legacy := os.Getenv("MESH_LOG_FORMAT"); legacy != "" {
			fmt.Fprintf(os.Stderr, "DEPRECATION WARNING: MESH_LOG_FORMAT is deprecated and will be removed in v0.3.0. Use STAYPOINT_LOG_FORMAT instead.\n")
			logFormat = legacy
		} else {
			logFormat = "text"
		}
	}
	logging.SetupLogger(logLevel, logFormat, os.Stderr)

	slog.Info("Starting Staypoint Background Daemon...")

	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	if err := config.EnsureDataDir(cfg); err != nil {
		slog.Error("Failed to ensure data dir", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		slog.Warn("Received signal, shutting down...", slog.Any("signal", sig))
		cancel()
	}()

	// 1. Start Rate Limit Notifier
	notifier := telemetry.NewNotifier()
	go notifier.Start(ctx)
	slog.Info("Rate limit monitoring active")

	// 2. Start File Watcher and Ingestion Engine
	watcher, err := telemetry.NewWatcher(cfg)
	if err != nil {
		slog.Error("Failed to initialize watcher", slog.Any("error", err))
		os.Exit(1)
	}

	slog.Info("Background daemon ready and running")
	if err := watcher.Start(ctx); err != nil {
		slog.Error("Watcher exited with error", slog.Any("error", err))
	}

	slog.Info("Daemon shutdown complete.")
}
