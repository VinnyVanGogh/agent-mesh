package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/adapter"
	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/ipc"
	"github.com/VinnyVanGogh/staypoint/internal/logging"
	"github.com/VinnyVanGogh/staypoint/internal/mcp"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
	"github.com/VinnyVanGogh/staypoint/internal/server"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
)

var (
	version   = "0.3.0"
	commit    = "none"
	GitCommit = "none"
	date      = "unknown"
)

func init() {
	if GitCommit != "none" && commit == "none" {
		commit = GitCommit
	} else if commit != "none" && GitCommit == "none" {
		GitCommit = commit
	}
}

func main() {
	// Subcommands dispatch before flag.Parse so they own their own flag sets.
	if len(os.Args) > 1 && os.Args[1] == "eval-contracts" {
		if err := runEvalContracts(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "eval-contracts:", err)
			os.Exit(1)
		}
		return
	}

	var (
		flagService    = flag.Bool("service", false, "Run as a Windows service (SCM-managed)")
		flagInstallSvc = flag.Bool("install-service", false, "Register staypointd with the Windows SCM")
		flagRemoveSvc  = flag.Bool("remove-service", false, "Unregister staypointd from the Windows SCM")
	)
	flag.Parse()

	if *flagInstallSvc {
		exe, err := filepath.Abs(os.Args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "install-service: %v\n", err)
			os.Exit(1)
		}
		if err := installService(exe); err != nil {
			fmt.Fprintf(os.Stderr, "install-service: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *flagRemoveSvc {
		if err := removeService(); err != nil {
			fmt.Fprintf(os.Stderr, "remove-service: %v\n", err)
			os.Exit(1)
		}
		return
	}

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

	// When running under the Windows SCM, log as JSON so the Windows Event Log
	// or a log collector can parse structured fields.
	isSvc, _ := isWindowsService()
	if isSvc || *flagService {
		logFormat = "json"
	}
	logging.SetupLogger(logLevel, logFormat, os.Stderr)

	slog.Info("Starting Staypoint Background Daemon...",
		slog.String("version", version),
		slog.String("commit", commit),
		slog.String("build_date", date),
	)

	if isSvc || *flagService {
		if err := runAsService(runDaemon); err != nil {
			slog.Error("Service exited with error", slog.Any("error", err))
			os.Exit(1)
		}
		return
	}

	// Interactive / console path: wire signal handler.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		slog.Warn("Received signal, shutting down...", slog.Any("signal", sig))
		cancel()
	}()

	if err := runDaemon(ctx); err != nil {
		slog.Error("Daemon exited with error", slog.Any("error", err))
	}
	slog.Info("Daemon shutdown complete.")
}

// runDaemon is the core daemon logic shared by interactive and service modes.
func runDaemon(ctx context.Context) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := config.EnsureDataDir(cfg); err != nil {
		return fmt.Errorf("ensure data dir: %w", err)
	}

	// 0. Open persistent DB; used by recovery scan, harness, and HTTP server.
	dbStore, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	_ = orchestrator.RecoveryScan(ctx, dbStore.DB())

	// 1. Start Rate Limit Notifier
	notifier := telemetry.NewNotifier()
	go notifier.Start(ctx)
	slog.Info("Rate limit monitoring active")

	// 2. Start IPC listener (Unix socket on POSIX, named pipe on Windows).
	socketPath := ipc.SocketPath()
	mcpServer := mcp.NewServer(mcp.WithConfig(cfg))
	go func() {
		handleConn := func(conn net.Conn) {
			defer conn.Close()
			if err := mcpServer.Serve(ctx, conn, conn); err != nil {
				slog.Debug("IPC connection closed", slog.Any("error", err))
			}
		}
		if err := ipc.Listen(ctx, socketPath, handleConn); err != nil && ctx.Err() == nil {
			slog.Warn("IPC listener exited", slog.Any("error", err))
		}
	}()
	slog.Info("IPC listener active", slog.String("path", socketPath))

	// 3. Start File Watcher and Ingestion Engine
	watcher, err := telemetry.NewWatcher(cfg)
	if err != nil {
		return fmt.Errorf("init watcher: %w", err)
	}

	// 4. Start HTTP & SSE Local Daemon Server (127.0.0.1 only)
	tokenPath := filepath.Join(cfg.DataDir, "auth_token")
	httpServer, err := server.New(server.Options{
		BindHost:     "127.0.0.1",
		Port:         41421,
		TokenPath:    tokenPath,
		DB:           dbStore.DB(),
		GitCommit:    GitCommit,
		CORSAllowAll: cfg.CORSAllowAll,
	})
	if err != nil {
		slog.Warn("Failed to initialize HTTP server", slog.Any("error", err))
	} else if err := httpServer.Start(); err != nil {
		slog.Warn("Failed to start HTTP server", slog.Any("error", err))
	} else {
		slog.Info("HTTP and SSE server active",
			slog.String("url", httpServer.URL()),
			slog.String("token_path", tokenPath),
		)
	}

	// 5. Wire GlobalDispatcher.OnWake to launch harness runs.
	repoRoot := cfg.WorkRepoRoot
	wireOnWake(ctx, dbStore, repoRoot)
	slog.Info("agent wake dispatcher wired", slog.String("repo_root", repoRoot))

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if httpServer != nil {
			_ = httpServer.Shutdown(shutCtx)
		}
		_ = dbStore.Close()
	}()

	slog.Info("Background daemon ready and running")
	return watcher.Start(ctx)
}

// wireOnWake assigns GlobalDispatcher.OnWake so every Wake call launches a
// harness run for the woken task. Extracted for testability.
func wireOnWake(ctx context.Context, dbStore *db.Store, repoRoot string) {
	h := orchestrator.NewHarness(dbStore.DB(), repoRoot)
	orchestrator.GlobalDispatcher.OnWake = func(taskID, reason string) {
		var agentID string
		_ = dbStore.DB().QueryRowContext(ctx,
			"SELECT COALESCE(assignee_agent_id,'') FROM tasks WHERE id=?", taskID,
		).Scan(&agentID)
		if agentID == "" {
			agentID = "local"
		}

		sessID := "paperclip-" + taskID
		if len(taskID) >= 8 {
			sessID = "paperclip-" + taskID[:8]
		}

		adapterFn := func(runCtx context.Context, cwd, prov string, rawArgs, extraEnv []string, stdout, stderr io.Writer) error {
			agentType := prov
			if agentType == "" {
				agentType = "claude"
			}
			_ = telemetry.HeartbeatSession(dbStore.DB(), telemetry.AgentSession{
				ID:        sessID,
				AgentType: agentType,
				RepoPath:  cwd,
				PID:       os.Getpid(),
			})
			if len(extraEnv) > 0 {
				runCtx = adapter.WithExtraEnv(runCtx, extraEnv)
			}
			return adapter.RunAdapter(runCtx, cwd, nil, prov, rawArgs, nil, stdout, stderr)
		}

		runCfg := orchestrator.RunConfig{
			AgentID:    agentID,
			WakeReason: reason,
			RunAdapter: adapterFn,
		}
		result, runErr := h.Run(ctx, taskID, runCfg)
		if runErr != nil {
			slog.Error("harness run failed", slog.String("task", taskID), slog.Any("error", runErr))
			return
		}
		slog.Info("harness run complete",
			slog.String("task", taskID),
			slog.String("disposition", result.Disposition),
			slog.Int("turns", result.Turns),
		)
	}
}
