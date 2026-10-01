# StayPoint Checklist Commit-Hash Verification & DoD Gate (STA-236)

## Executive Summary
Per Board of Directors directive, StayPoint enforces automated commit-hash verification for all checklist items, a hard UI and API blocking gate against unmerged and uncompiled code, and a turnkey agent validation script (`scripts/verify-checklist.sh`).

Under this policy:
**Unmerged branches and unrebuilt binaries are STRICTLY PROHIBITED from being marked "done".**

---

## 1. Core Architecture

### A. Checklist Commit-Hash Binding (`internal/checklist/`)
Every checklist item or section in StayPoint is bound to a specific git commit hash:
- **Precedence Order**:
  1. Explicit `commit_hash` column on `checklist_items` table (or item struct).
  2. `commit_hash` embedded in machine contract JSON.
  3. Section-level mapping via `DefaultSectionCommits` (e.g. `STA-191` -> `4697e36`, `STA-192` -> `883f3fe`).
- **Schema Migration**: Idempotently adds `commit_hash TEXT` to `checklist_items`.

### B. Main Branch & Active Binary Check (`internal/checklist/commit_check.go`)
When StayPoint serves (`GET /api/checklist`) or evaluates (`POST /api/checklist/evaluate`) a checklist, it executes bidirectional git ancestor verification:
1. **Main Branch Verification**:
   - `git merge-base --is-ancestor <commit> main` (or `origin/main` / `HEAD`).
   - If false: Commit is on an unmerged feature branch. Gate blocks completion.
2. **Active Binary History Verification**:
   - Compares `<commit>` against the running binary's commit hash (injected at build time via `-ldflags "-X main.GitCommit=..."`).
   - `git merge-base --is-ancestor <commit> <running_binary_commit>`.
   - If false: The running `staypointd` binary does not contain the commit (stale/unrebuilt binary). Gate blocks completion.

### C. Build Manifest (`~/.staypoint/build-manifest.json`, `internal/checklist/build_manifest.go`)
The daemon runs under launchd, and macOS blocks launchd processes from reading `~/Documents` (where the repo lives) until `staypointd` is granted Documents access. A blocked `git` does not fail: it hangs until the timeout kills it. Before the manifest existed, every commit was reported as "does not exist" and the checklist page took ~25s to load.

`scripts/reinstall-daemon.sh` therefore records, at build time:
- `full_sha` and `ancestors`: every commit reachable from the build (`git rev-list HEAD`).
- `in_main`: whether the build commit was in `origin/main` when built.
- `dirty`: whether tracked files had uncommitted changes. A dirty build is labelled `<sha>-dirty` and keeps the gate closed.

The gate uses the manifest when it describes the running binary's commit, with no repo access at all. Otherwise it falls back to the git checks above, after one 1.5s probe; if the repo cannot be read, items report "cannot verify" with the cause, never "does not exist".

---

## 2. Hard UI Notice & Completion Gate

### Web UI Behavior (`internal/server/webui/app.js` & `style.css`)
If any checklist item references a commit that is missing from `main` or missing from the running daemon binary:
1. **Prominent Notice Banner**: Renders at the top of `/checklist` with red/amber styling, indicating the exact count of blocked items and sections.
2. **Warning Log Table**: Includes an expandable log table listing each missing commit, target section/item, `main` status, daemon binary status, and failure reason.
3. **Section Warning Badges**: Displays `⛔ Commit Gate Active` on affected section headers.
4. **Hard Completion Gate**:
   - The "Pass" (`✓`) button is disabled (`cl-btn-blocked`) with a descriptive tooltip explaining the block.
   - Any manual or programmatic attempt to mark the item as `pass` alerts the user and halts execution.
   - Walkthrough Tour HUD disables the `pass` button when viewing blocked items.

### API Layer Gate (`PATCH /api/checklist/{id}`)
Defense-in-depth is enforced at the REST API layer:
- If a client attempts to set `status: "pass"` on an item with an unmerged or uncompiled commit, the server rejects the request with **HTTP 409 Conflict** and an explanatory error payload.

---

## 3. Agent CLI Pre-Flight Tool (`scripts/verify-checklist.sh`)

Every engineering agent and contributor MUST run `scripts/verify-checklist.sh` prior to closing any issue or pull request:

```bash
# Basic pre-flight verification against local daemon
scripts/verify-checklist.sh

# Strict mode: verifies 100% of items are marked pass
scripts/verify-checklist.sh --strict

# Custom sprint or daemon URL
scripts/verify-checklist.sh --sprint STA-168-2 --url http://127.0.0.1:41421
```

### Verification Steps
1. Pings daemon health on `/api/health` and retrieves active binary commit.
2. Compares active binary commit against `main` HEAD.
3. Validates all checklist items and section commit hashes for `main` and binary inclusion.
4. Evaluates automated machine contracts via `/api/checklist/evaluate`.
5. Emits a structured PASS/FAIL terminal report. Exits `0` on PASS, `1` on FAIL.

---

## 4. Enforcement Policy & Non-Negotiable DoD

1. **No PR or issue may be closed with active commit gate warnings.**
2. **Feature branches MUST be merged into `main` and pushed to remote origin.**
3. **The daemon binary must be rebuilt and restarted via `scripts/reinstall-daemon.sh` so `-X main.GitCommit` reflects current `main`.**
4. **Any attempt to mark checklist items done without satisfying the commit gate is a policy violation.**
