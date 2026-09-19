# Agent-Mesh (`mesh`)

Autonomous AI Agent Ops, Quota Pacing & Cross-AI Context Platform for **Claude Code** and **Google Antigravity / Gemini**.

---

## Features

- **⚡ Sub-5ms Statusline**: Tokyo Night recessed meter tracks for context, 5h rolling, and 7-day rate-limit windows.
- **🧭 Dynamic Routing & Pacing**: Automatically chooses between Claude and Gemini based on rate-limit headroom and repository context.
- **🌉 Resilient Work Bridge**: Detects enterprise repos and routes sessions to remote hardware (`mansol-mbp`) with zero-close local fallback and automatic `tmux` session persistence.
- **🔄 Zero-Clarification Handoff**: Auto-synthesizes git branch, diff stat, and immediate next steps for seamless model switching via `pbcopy`.
- **📄 Executive ROI Briefings ("Boss Card")**: Generates print-ready high-DPI PDFs in pure Go via Chrome DevTools Protocol (`chromedp`).
- **👀 Background Watcher (`meshd`)**: Lightweight daemon (19MB RAM) watching transcripts via `fsnotify` and monitoring rate limits.

---

## Quick Start

### Installation

```bash
# Build from source
git clone https://github.com/VinnyVanGogh/agent-mesh.git
cd agent-mesh
go build -o ~/.local/bin/mesh ./cmd/mesh
go build -o ~/.local/bin/meshd ./cmd/meshd
```

### Shell Integration

Add to your `~/.zshrc` or `~/.bashrc`:

```bash
eval "$(mesh init --shell)"
```

### Commands

```bash
mesh status                                 # Live fleet meters & routing
mesh statusline                             # Fast statusline hook (<5ms)
mesh route [dir]                            # Quota waterfall recommendation
mesh report --pdf --type all                # Generate all 4 executive PDF reports
mesh bridge check [dir]                     # Test remote SSH & repo path mapping
mesh bridge launch [dir]                    # Launch remote session in tmux with fallback
mesh handoff                                # Cross-model switch prompt to clipboard
mesh handoff --push [remote]                # Push active context directly to remote machine
mesh handoff --pull [remote]                # Pull remote context to local clipboard
mesh sync pull [remote]                     # Pull remote transcripts over SSH/Tailscale into local DB
mesh sync export -o bundle.tar.gz           # Export telemetry bundle for air-gapped / MDM transfer
mesh sync import bundle.tar.gz              # Ingest exported telemetry bundle into local DB
mesh task list                              # SQLite task management
```

---

## Configuration

Configuration is located at `~/.agent-mesh/config.toml`:

```toml
company_name = "Managed Solution"
engineer_name = "Vince Vasile"

# Optional: Set your billable hourly rate.
# When 0.0, client billable hours are completely omitted from reports.
hourly_rate = 0.0

work_email = "vvasile@managedsolution.com"
personal_email = "stylesbyvinny@gmail.com"
work_repo_root = "~/Documents/dev/mansol"
remote_host = "mansol-mbp"

# Machine Role: "work", "personal", or "hybrid" (default)
# - "work": Forces all ingested activity on this machine to corporate work attribution.
# - "personal": Forces all ingested activity to personal attribution.
# - "hybrid": Dynamically inspects repository paths and git authors.
machine_role = "hybrid"
```

---

## Multi-Machine & Air-Gapped Setups

For developers using dedicated hardware (e.g., corporate laptop + personal workstation):

1. **Connected via SSH / Tailscale**:
   - Run `mesh sync pull work-laptop` from your personal machine to pull transcripts into your central telemetry DB.
   - Run `mesh handoff --push work-laptop` before stepping away to prime the remote machine's clipboard.
   - Run `mesh handoff --pull personal-desktop` when picking up work on the other machine.

2. **Air-Gapped / Strict Corporate MDM (Inbound SSH Blocked)**:
   - On the work machine: `mesh sync export -o ~/Desktop/work-telemetry.tar.gz`
   - Move archive via Slack, AirDrop, or secure thumbdrive.
   - On your personal machine: `mesh sync import ~/Desktop/work-telemetry.tar.gz`
   - Run `mesh report --pdf --type combined` to produce your unified multi-AI executive memo.

---

## License

Apache 2.0. 100% local-first, zero telemetry phone-home.
