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
```

---

## License

Apache 2.0. 100% local-first, zero telemetry phone-home.
