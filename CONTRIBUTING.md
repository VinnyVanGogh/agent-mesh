# Contributing to Staypoint

Thank you for your interest in contributing to Staypoint!

## 1. Prerequisites

- Go 1.23 or newer.
- Git.
- Optional: `golangci-lint` (v1.64 or newer).

## 2. Local Development Workflow

### Clone and Build

```bash
git clone https://github.com/VinnyVanGogh/staypoint.git
cd staypoint

# Build both binaries
go build -o bin/staypoint ./cmd/staypoint
go build -o bin/staypointd ./cmd/staypointd
```

### Running Tests

Staypoint requires all tests to pass cleanly without cached results and with the race detector enabled:

```bash
# Run full test suite with race detector
go test -v -race ./...

# Run fuzz testing on MCP frame parser
go test -v -fuzz=FuzzHandleMessage -fuzztime=5s ./internal/mcp
```

### Static Analysis & Verification

Ensure your changes pass standard Go vetting and linter rules:

```bash
# Standard Go vetting
go vet ./...

# golangci-lint (if installed)
golangci-lint run
```

## 3. Engineering Guidelines

1. **Pure Go (Zero CGO)**: Staypoint must always compile with `CGO_ENABLED=0`. All database operations use `modernc.org/sqlite`. Do not introduce dependencies that require a C compiler or dynamic library links.
2. **Sub-Millisecond Execution**: Core interactive paths such as `statusline` must execute in under 2 milliseconds. Avoid spawning sub-processes or making network calls in statusline rendering code.
3. **Decoupled Architecture**: CLI commands reside in `cmd/staypoint/` in modular files (`root.go`, `checkpoint_cmd.go`, etc.). Engine logic resides in `internal/`.
4. **Error Handling**: Use explicit error wrapping with `fmt.Errorf("...: %w", err)` and sentinel checks via `errors.Is` and `errors.As`.
5. **No Em Dashes**: In accordance with project documentation standards, never use em dashes in code comments, commit messages, or documentation files. Use periods, commas, colons, or "which".

## 4. Submitting Pull Requests

1. Create a feature branch from `main`: `git checkout -b feature/my-improvement`.
2. Commit your changes with clear, descriptive commit messages.
3. Push to your fork and open a pull request against `main`.
4. Verify that all automated CI checks (multi-platform matrix, race detector, govulncheck) are green.
