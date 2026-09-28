# Staypoint Architecture

Staypoint is a local-first autonomous AI agent operations harness, quota pacer, and context continuity bridge. It operates as a bridge between the developer, host operating system, and autonomous coding agents (Claude Code and Google Antigravity).

```
                                  SYSTEM DATA FLOW
                                  
   [Agent Transcripts]           [Developer / Shell]          [Remote Enterprise Host]
  Claude: ~/.claude/projects     staypoint status / route     SSH Reverse Tunnel (:42124)
  Gemini: ~/.gemini/brain        staypoint checkpoint / undo  Persistent Remote tmux
            │                             │                             │
            ▼                             ▼                             ▼
   ┌───────────────────┐         ┌───────────────────┐         ┌───────────────────┐
   │    staypointd     │         │     staypoint     │         │   Remote Bridge   │
   │ Background Daemon │         │    Unified CLI    │         │  Path Translator  │
   └────────┬──────────┘         └────────┬──────────┘         └────────┬──────────┘
            │ Byte-offset tail            │ In-process commands         │ Token & SCP sync
            ▼                             ▼                             ▼
   ┌───────────────────────────────────────────────────────────────────────────────┐
   │                     Local SQLite Storage (staypoint.db)                       │
   │  modernc.org/sqlite (Zero CGO, WAL mode, single writer, busy_timeout=5000)    │
   │  • requests: idempotency keys, token counts, model attribution, timestamps    │
   │  • tasks: dollar budgets, turn limits, active spend meters                    │
   │  • wire_messages: inter-agent peer broadcast channel with TTL pruning         │
   │  • agent_circuit_breakers: trip counts, failure signatures, consecutive loops │
   │  • agent_working_files: active touch locks with 15-minute expiration          │
   └───────────────────────────────────────────────────────────────────────────────┘
                                          │
                                          ▼
   ┌───────────────────────────────────────────────────────────────────────────────┐
   │                   Model Context Protocol (MCP) Stdio Server                   │
   │  • Tools: staypoint_checkpoint, staypoint_undo, staypoint_wire_post/list,     │
   │           staypoint_task_list, staypoint_condense, staypoint_status           │
   └───────────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Core Principles

1. **Zero CGO**: Statically compiled binary with zero dynamic library dependencies. Uses `modernc.org/sqlite` pure Go SQLite driver.
2. **Sub-Millisecond Execution**: Core paths such as `statusline` execute in under 2 milliseconds to avoid prompt lag.
3. **Zero Outbound Telemetry**: All data remains local in `~/.staypoint/`. No telemetry is ever transmitted to external servers.
4. **Idempotent Ingestion**: All transcript entries generate deterministic SHA-256 idempotency keys to ensure multiple sync passes never double-count tokens.
5. **Decoupled Supervision**: The CLI uses `syscall.Exec` to yield terminal control to interactive agent CLIs, while the background daemon (`staypointd`) provides supervisory guardrails.

---

## 2. Component Topology

### CLI (`cmd/staypoint`)

The unified CLI provides 17 modular command entrypoints decomposed into dedicated per-command Go files:

- `root.go`: Configuration loading, persistent flags, and smart dispatch engine (`runSmartLaunch`).
- `checkpoint_cmd.go`: Micro-checkpointing (`checkpoint`, `undo`, `redo`, `checkpoints`, `migrate-legacy-refs`).
- `condense_cmd.go`: Token diet compiler and panic log compressor.
- `wire_cmd.go`: Peer broadcast scratchpad (`post`, `list`, `prune`).
- `breaker_cmd.go`: Circuit breaker listing and manual resets.
- `task_cmd.go`: Task creation, listing, status updates, and budget enforcement.
- `route_cmd.go`: Quota-aware model recommendation engine.
- `status.go`: Fleet status display and sub-2ms Tokyo Night statusline widget.
- `report.go`: Executive ROI PDF briefing generator ("The Boss Card") powered by `chromedp`.
- `bridge_cmd.go`: SSH connectivity probe, latency check, and remote tmux supervisor.
- `handoff_cmd.go`: Zero-token session manifest creation and clipboard handoff generator.
- `sync_cmd.go`: Cross-machine telemetry synchronization (SSH pull and tar.gz air-gapped export/import).
- `hook_cmd.go`: Prompt submit hooks for Claude Code and Antigravity.
- `where_cmd.go`: Context pickup showing active tasks, git status, and directive trails.
- `doctor.go`: System diagnostic probe.
- `mcp.go`: Model Context Protocol stdio server daemon runner.
- `version.go`: Build stamping surface displaying version, git commit, and build date.

#### Process Execution Model

When `staypoint` dispatches a command to Claude Code (`claude`) or Google Antigravity (`agy`), it invokes `syscall.Exec` on Unix systems. This replaces the running process image in-place, granting the agent direct access to stdin, stdout, and the terminal TTY. This eliminates terminal emulation lag, PTY escape sequence corruption, and signal forwarding issues. Because the CLI process terminates upon handoff, crash recovery and loop detection are deferred to the background daemon.

### Daemon (`cmd/staypointd`)

The background daemon runs as a continuous service (managed via macOS launchd or Linux systemd):

1. **File Watcher**: Uses `fsnotify` to monitor `~/.claude/projects/` and `~/.gemini/antigravity-cli/brain/` for `.jsonl` modifications.
2. **Byte-Offset Cursor Tracking**: Maintains persistent byte offsets in `~/.staypoint/ingest-cursors.json`.
3. **Delimiter-Bounded Reading**: Reads incoming transcript lines using `reader.ReadBytes('\n')`. If a write ends without a newline delimiter (such as a slow agent writing a chunk), the cursor remains at the start of the uncompleted line, preventing partial record corruption.
4. **Token Attribution**: Computes token costs against dynamic provider pricing matrices.
5. **Circuit Breakers**: Evaluates failure signatures in real time. Trips when an agent encounters 3 consecutive identical tool errors, or 5 varied errors within 5 minutes.
6. **Collision Detection**: Records files touched by active sessions with a 15-minute sliding TTL.
7. **Task Metering**: Attributes token and dollar spend against the current active repository task.

### Model Context Protocol (MCP) Server (`internal/mcp`)

The MCP server provides standard Model Context Protocol (protocol version `2024-11-05`) JSON-RPC 2.0 communication over stdio:

- **Transport**: Standard input/output line-delimited JSON.
- **Parsing**: Built with standard library `encoding/json`. Verified against adversarial inputs via Go fuzz testing (`FuzzHandleMessage`).
- **Exposed Tools**:
  - `staypoint_checkpoint`: Captures an isolated git micro-checkpoint in under 15ms.
  - `staypoint_undo`: Restores the working tree with dry-run and ignored file options.
  - `staypoint_wire_post`: Broadcasts notes to other agents on the repository wire.
  - `staypoint_wire_list`: Lists unread broadcast messages on channels.
  - `staypoint_task_list`: Inspects active tasks, dollar budgets, and turn limits.
  - `staypoint_condense`: Compresses large compiler error streams before consumption.
  - `staypoint_status`: Returns current quota headroom, pacer states, and routing plans.

---

## 3. Storage & Schema (`internal/db`)

Staypoint maintains a local SQLite database (`staypoint.db`) configured with Write-Ahead Logging (`PRAGMA journal_mode = WAL`), normal synchronization (`PRAGMA synchronous = NORMAL`), and busy timeout of 5,000ms.

```sql
-- Accounts & Organization Roles
CREATE TABLE IF NOT EXISTS accounts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    account_key   TEXT UNIQUE,
    email_domain  TEXT UNIQUE,
    role          TEXT NOT NULL CHECK (role IN ('work', 'personal', 'other')),
    label         TEXT NOT NULL,
    plan_tier     TEXT NOT NULL,
    notes         TEXT,
    registered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Rolling Quota Tracking
CREATE TABLE IF NOT EXISTS quota_windows (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    pool_key      TEXT NOT NULL UNIQUE,
    window_type   TEXT NOT NULL CHECK (window_type IN ('rolling_5h', 'weekly_7d')),
    used_percent  REAL NOT NULL DEFAULT 0.0,
    remaining_pct REAL NOT NULL DEFAULT 100.0,
    is_locked     INTEGER NOT NULL DEFAULT 0,
    resets_at     TEXT,
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Task Governance & Budgets
CREATE TABLE IF NOT EXISTS tasks (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    repo_path      TEXT NOT NULL,
    git_branch     TEXT,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done', 'soft_deleted')),
    account_role   TEXT NOT NULL DEFAULT 'work',
    max_budget_usd REAL NOT NULL DEFAULT 0.0,
    max_turns      INTEGER NOT NULL DEFAULT 0,
    spent_tokens   INTEGER NOT NULL DEFAULT 0,
    spent_usd      REAL NOT NULL DEFAULT 0.0,
    spent_turns    INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at     TEXT
);

-- Inter-Agent Wire Broadcast
CREATE TABLE IF NOT EXISTS wire_messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    channel     TEXT NOT NULL DEFAULT 'global',
    author      TEXT NOT NULL,
    repo_path   TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at  TEXT NOT NULL
);

-- Active File Touch Locks (Collision Detection)
CREATE TABLE IF NOT EXISTS agent_working_files (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    repo_path         TEXT NOT NULL,
    file_path         TEXT NOT NULL,
    access_type       TEXT NOT NULL CHECK (access_type IN ('read', 'write', 'lock')),
    first_touched_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_touched_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at        TEXT NOT NULL
);

-- Agent Loop Circuit Breakers
CREATE TABLE IF NOT EXISTS agent_circuit_breakers (
    session_id        TEXT PRIMARY KEY,
    repo_path         TEXT NOT NULL,
    agent_type        TEXT NOT NULL,
    is_tripped        INTEGER NOT NULL DEFAULT 0,
    trip_count        INTEGER NOT NULL DEFAULT 0,
    failure_signature TEXT,
    failing_tool      TEXT,
    failing_command   TEXT,
    last_error        TEXT,
    tripped_at        TEXT,
    cleared_at        TEXT
);
```

---

## 4. Ephemeral Git Plumbing Architecture (`internal/checkpoint`)

Staypoint implements an isolated snapshot engine that avoids polluting user branch histories:

1. **Isolated Index**: Checkpoint operations set `GIT_INDEX_FILE=.git/staypoint_index`. The user working index (`.git/index`) is never touched during checkpoint creation.
2. **Plumbing Staging**: Executes `git add -A` against the isolated index.
3. **Tree Object Generation**: Runs `git write-tree` to generate a SHA-1/SHA-256 tree object directly in the Git object database.
4. **Commit Object Creation**: Runs `git commit-tree <tree-sha> -p HEAD -m <msg>` to create an orphan commit pointing to the current HEAD parent.
5. **Custom Ref Namespace**: Commits are updated under `refs/staypoint/checkpoints/<session>/<id>` and `refs/staypoint/checkpoints/latest` using `git update-ref`.
6. **Safety Snapshot on Undo**: Before any rollback is applied to the working directory, Staypoint automatically creates a pre-undo safety snapshot under `refs/staypoint/checkpoints/pre-undo`. This makes `staypoint redo` fully deterministic.

---

## 5. Token Diet Architecture (`internal/condenser`)

Raw compiler error output (such as 500-line TypeScript cascades or multi-goroutine panic traces) can consume 2,000 to 5,000 tokens per agent turn. Staypoint implements deterministic syntactic filtering:

- **TypeScript**: Groups identical error codes (e.g. `[TS2339]`), displays the first 2 occurrences in detail, and summarizes the remainder (`... and 14 other instances`).
- **Go Panics**: Extracts the panic header, runtime signal, and the active crashing goroutine frame, discarding irrelevant parked or background runtime goroutines.
- **Python**: Strips framework middleware frames (e.g. `site-packages/werkzeug`, `site-packages/flask`) and highlights application source code lines and the final exception message.
