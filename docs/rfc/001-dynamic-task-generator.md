# RFC-001: Architecture & Implementation of Dual CLI & TUI Dynamic Task Generator

## 1. Objective
Add a new `staypoint task create` (or `staypoint task add` alias) command with dual-mode (CLI & TUI) task generation. It should ingest a raw user comment, use Gemini 3.8 Flash (falling back to Claude Sonnet) to structure the task, and dispatch it to the Paperclip API. Finally, it should render the generated task as a markdown summary via `glow` (or glamour) and output a clickable link alongside telemetry.

## 2. Interface Options

### 2.1 TUI Mode
Triggered by `staypoint task create --tui` or by running `staypoint task create` with no trailing arguments.
- **Library**: Use `github.com/charmbracelet/bubbletea` and `github.com/charmbracelet/bubbles/textarea`.
- **Requirements**:
  - Full-screen or inline modal text entry.
  - Speech-to-Text Safe: Must not unexpectedly trigger execution or break formatting upon encountering unescaped quotes, backticks, bracket collisions, or typeless STT dictation artifacts.
  - Exits with `Ctrl+S` or `Ctrl+D` (Submit) / `Ctrl+C` (Cancel).

### 2.2 CLI Mode
Triggered by `staypoint task create "<comment>"` or via `stdin` piping (e.g., `echo "fix login bug" | staypoint task create`).
- **Requirements**:
  - Accepts raw comments as positional arguments or piped input.
  - Robust quote parsing and sanitization.

## 3. Engine & Model Hierarchy

### 3.1 Primary Model: Gemini 3.8 Flash
- Uses HTTP calls to the Gemini API (`gemini-3.8-flash`).
- Designed for high speed, low latency, and zero cost under the subscription model.

### 3.2 Fallback Model: Claude Sonnet
- If the Gemini API is exhausted, rate-limited, or returns a 429/500 series error, automatically downshift to Claude Sonnet (`claude-sonnet-4-6` via `claude_local` router or Anthropic API).

### 3.3 Prompt & Inference Logic
The generation step will construct a prompt wrapping the raw user comment. The LLM must return structured JSON inference containing:
- `organization`: Target Organization/Company (StayPoint, Managed Solution, RuneLite, Maintenance, Research).
- `project`: Target Project.
- `title`: Crisp issue title.
- `description`: Structured Markdown description (Objectives, Core Specs, Next Steps).
- `priority`: Enum (`low`, `medium`, `high`, `urgent`).
- `labels`: Array of relevant tag strings.
- `assigneeRole`: Recommended assignee role.

## 4. Issue Dispatch
Use standard Go `net/http` to send a POST request to the Paperclip API.
- **Endpoint**: `/api/companies/:companyId/issues`
- **Headers**:
  - `Authorization: Bearer <PAPERCLIP_API_KEY>`
  - `Content-Type: application/json`
- Map inferred JSON data into the issue creation payload.

## 5. Post-Run Output & Terminal Presentation
After successful dispatch, format and output the result:
1. **Fleet Status & Pacing**: Execute and print the equivalent of `staypoint status` to display pacing and week boundaries.
2. **Token Telemetry**: Display token usage metrics (input, output, cached tokens, estimated USD cost) returned by the inference model.
3. **Markdown Summary**: Use `github.com/charmbracelet/glamour` directly in Go (avoiding a separate `glow` binary dependency) to render a concise summary card of the generated task.
4. **Clickable Task Link**: Output a hyperlink to the created issue: `http://127.0.0.1:3100/<PREFIX>/issues/<ID>`.

## 6. Implementation Plan & Package Layout
We delegate implementation to the CLI & Statusline Presentation Specialist.
- `cmd/staypoint/task_create.go`: Define the Cobra command, manage flags, detect TUI vs CLI mode, and render post-run outputs.
- `internal/ui/textarea.go`: Implement Bubble Tea model and logic for the interactive text area.
- `internal/ai/generator.go`: Orchestrate inference using Gemini with a fallback to Claude, requesting structured JSON from the model.
- `internal/paperclip/client.go`: Handle the POST request payload mapping and HTTP dispatch to `/api/companies/:companyId/issues`.

## 7. QA / Testing Validation
- Add unit tests for prompt generation and JSON extraction.
- Add mock client tests for Gemini API fallback behavior.
- Validate Bubble Tea UX gracefully handles pasting multi-line quotes without artifacts.
