# StayPoint Native Orchestrator: Decisions and Baselines

Status: Phase 0 (T0, STA-92). Epic: STA-91. This document records decisions the research
reports left open and replaces unverified performance claims with measurements.

## Decisions

| Topic | Decision | Notes |
|---|---|---|
| Web UI listen address | `127.0.0.1:4173` | Reports disagreed (42125 / 4173 / 4040). Configurable; loopback only, never `0.0.0.0` (see `SECURITY.md`). If the port is taken the server must fail with a clear error, not silently pick another. |
| Daemon HTTP API prefix | `/api/v1` | Versioned, served on the same listener as the web UI. Existing `internal/bridge` endpoints (`/ping`, `/fetch`) are a separate token-authenticated listener and are unchanged. The existing `staypointd.sock` unix-socket IPC (`internal/ipc`) is unchanged. |
| Provider access policy | BYOC only | Only the user's own locally installed CLIs and own accounts. No pooling, proxying, or resale, in any tier. Documented in `README.md` and `SECURITY.md`. |
| `NOTICE` for Paperclip (MIT) | Not added | Added only if Paperclip schemas or prompt text are ported verbatim. Nothing has been ported so far. Revisit in any task that copies such material. |
| Scheduling model | Event-driven | Nothing wakes an agent on a timer. |
| Runtime constraints | Pure Go, zero CGO, `modernc.org/sqlite`, no new external daemons | |
| Monetization gate | NO-GO on paid SaaS / tiers | StayPoint is 100% free, local-first, and open source (Apache-2.0). See `docs/MONETIZATION-DECISION-GATE.md` (T19). |

## Baselines (measured)

Method: `go test ./internal/checkpoint -run TestBenchmarkCheckpointDistribution -bench BenchmarkCreateCheckpoint -benchtime 30x -v`
(existing benchmarks in `internal/checkpoint/checkpoint_benchmark_test.go`; repo of 50 modified
tracked files and 10 untracked files). Startup: `staypoint version`, 10 runs, wall clock via
`/usr/bin/time -p`, binary built with plain `go build`.

Environment: Apple M2 Max, macOS (Darwin 25.4.0), Go 1.26.2, 2026-09-29.
**The host was heavily loaded during measurement (load average 140 to 157 on a 12-core machine,
shared with many concurrent agent processes).** All numbers below are therefore pessimistic upper
bounds, not best-case figures. Re-run on an idle machine before publishing any number externally.

| Metric | Measured | Previously claimed |
|---|---|---|
| Checkpoint latency (`CreateCheckpoint`, 20 samples) | min 407 ms, median 940 ms, p95 5.013 s (hit the default 5 s timeout), avg 1.182 s | "<5ms" (README), "under 15ms" (ARCHITECTURE) |
| Checkpoint latency (Go benchmark, 30 iterations) | 1279.6 ms/op | same |
| Plain `git status --porcelain` on a 50-file repo, single run, same load | 70 ms | n/a |
| Plain `git write-tree` on a 50-file repo, single run, same load | 161 ms | n/a |
| Startup, `staypoint version` (10 runs) | min 40 ms, range 40 to 410 ms (typical 100 to 400 ms) | "sub-2ms" statusline (different command, not measured here) |
| Binary size | `staypoint` 33.0 MB, `staypointd` 16.3 MB | n/a |

Findings:

- `CreateCheckpoint` shells out to roughly 8 to 10 `git` subprocesses per call (`rev-parse`, `add`,
  `write-tree`, `status --ignored`, `commit-tree`, three `update-ref`, `diff-tree`). Under load
  each subprocess costs tens to hundreds of milliseconds, so the cost is dominated by process
  spawning, not by StayPoint logic. The sub-5ms claim is not credible for this design under
  load; the true idle figure is unmeasured.
- The p95 sample equals the 5 s default timeout, meaning a checkpoint can fail outright when the
  host is saturated. Callers in the orchestrator must treat checkpoint failure as non-fatal.
- The statusline "sub-2ms" and "1.8ms" figures were not re-measured in this task and remain
  unverified.

## Follow-ups (not done in T0)

- Correct or remove the unverified latency claims in `README.md` (badge, lines describing
  `<5ms`, `<2ms`, `<10ms`) and `ARCHITECTURE.md` (`<15ms`, `sub-2ms`) once an idle-machine
  benchmark exists. Left untouched here to keep T0 to the specified scope; tracked by risk K-18.
- Codex CLI flags come from unverified research reports. The Codex adapter task must probe the
  installed `codex --help` and record golden fixtures before hardcoding any flag.
- Re-read current Anthropic/OpenAI/Google terms before any public release (BYOC working rule).
