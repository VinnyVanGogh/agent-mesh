# Roadmap: Replace Paperclip with StayPoint (Dogfood)

> **Status:** Draft for Board approval — STA-288  
> **Author:** Lead Systems & Daemon Architect  
> **Date:** 2026-10-01  
> **Scope:** STA company only. Other companies (MAN, PER, RUN) stay in Paperclip until post-cutover parity is proven.

---

## 1. Current-State Gap Table

What exists and works in StayPoint today vs. what Paperclip provides day-to-day.

| Paperclip Feature | StayPoint Today | Notes |
|---|:---:|---|
| Issues / tasks (CRUD, execution stages) | ✅ EXISTS | Full schema + REST API live |
| Comments | ✅ EXISTS | Read + write, author field |
| Task blockers / dependencies | ✅ EXISTS | `task_relations`, block/unblock API |
| Task documents (versioned plan) | ✅ EXISTS | `task_documents` table + API |
| Work products (PR, commit, branch, workspace_file) | ✅ EXISTS | Schema + write API; `preview_url`/`runtime_service` types missing |
| Budget + spend tracking per task | ✅ EXISTS | `max_budget_usd`, `spent_usd` on tasks |
| Quota gauges (5h / weekly, claude / gemini) | ✅ EXISTS | Live in web UI |
| Governance / approvers / reviewers | ✅ EXISTS | `task_governance` tables + routes |
| SSE real-time events | ✅ EXISTS | `/api/events`; board subscribes |
| Board Kanban web UI | ✅ EXISTS | 4 columns, filter pills, project cards |
| Task detail panel (full-page) | ✅ EXISTS | Panel + full-page toggle; URL-routed page in STA-282 |
| Interaction cards in web UI (ask / confirm / suggest) | ⚠️ PARTIAL | Schema + API exist; **web UI does not render them** |
| **Run visibility ("working" spinner → live timeline)** | ❌ MISSING | Only spinner shown; Board cannot see what agent is doing (STA-289) |
| Agent assignment / checkout | ⚠️ PARTIAL | `checkout_agent_id` field + claim API; no wake-on-assign from UI |
| Agent run trigger from UI | ❌ MISSING | No "Run Now" button |
| Project-level grouping | ⚠️ PARTIAL | `project` text field on tasks; cards in UI; no first-class entity |
| Priority field (DB) | ❌ MISSING | Filter UI exists; no `priority` column in schema |
| Agent directory / inbox | ❌ MISSING | No per-agent inbox or catalog |
| Wake-on-assign automation | ❌ MISSING | Dispatcher exists; assignment→wake not wired |
| PR review handoff (auto-create review task) | ❌ MISSING | Manual only |
| Issue / task search | ❌ MISSING | No full-text search endpoint |
| Multi-company / multi-org isolation | ❌ MISSING | Single-tenant by design; out of scope for this roadmap |
| Labels / tags | ❌ MISSING | No labels column in schema |
| Routines / cron triggers | ❌ MISSING | Not built |
| File attachments | ❌ MISSING | Not in schema |
| Approval cards (first-class approval entity) | ❌ MISSING | Governance tables exist; no Paperclip-style approval board card |
| Migration tool (Paperclip → StayPoint) | ❌ MISSING | Not built; not required (clean-start approach) |

---

## 2. Minimum Cutover Scope

The Board must be able to complete the dogfood loop before cutting over. Minimum set:

1. **Run visibility** — live step-by-step timeline on task detail page so Board can see what an agent is doing without relying on `--dangerously-skip-permissions` terminal (STA-289)
2. **Interaction cards in web UI** — Board can respond to agent `ask_user_questions` / `request_confirmation` cards without switching to a terminal
3. **URL-routed task detail page** — stable links to tasks, with comment thread and work products (STA-282)
4. **Wake-on-assign** — assigning a task from the UI wakes the assigned agent automatically
5. **Agent run trigger from UI** — "Run Now" button so Board can manually kick a heartbeat
6. **Quota gauges stay live** — existing 5h/weekly gauges continue working (STA-283 regression must be resolved first)

Everything else (labels, priority field, routines, file attachments, migration tooling, multi-company) is post-cutover.

---

## 3. Ordered Milestones

> Sizes: S < 1 day · M 1-3 days · L 4-7 days · XL > 1 week

### M0 · In-flight (STA-282 + STA-283) — L — target: this sprint

- STA-282: task detail as a full URL-routed page (`/tasks/:org/:project/:id`) + interactive session panel
- STA-283: fix claude_work / claude_personal quota flip + project card click-through regression

_These are already `in_progress`. M1 and M2 depend on M0._

---

### M1 · Run visibility: live micro-checkpoints (STA-289) — L — starts after M0

**Board signal:** This is the most important feature for the replacement.

Replace the spinning "working" indicator with a live, named step timeline on the task detail page. Each harness step emits a checkpoint event over SSE; the task page renders it in real time.

Steps to show:
1. Wake reason (assigned / commented / blocker cleared)
2. Context read (files, issue, comments)
3. Plan, one line
4. Each action (file edit, test, command) with pass/fail
5. Git checkpoint (sha, diff link, undo button)
6. Finish (status + 1–2 sentence plain-English summary)

Also: elapsed time, tokens/cost so far, "stuck" warning if no step in N minutes.

_Existing foundations:_ `internal/adapter/*.go` `ParseStreamDelta`, `internal/checkpoint/`, `internal/orchestrator/harness.go` Run loop, `/api/events` SSE.

---

### M2 · Interaction cards in web UI — M — starts after M0, parallel with M1

Render the three existing interaction kinds in the task thread:

- `ask_user_questions` → checkbox/option form, Board submits answer
- `request_confirmation` → accept / reject buttons, plan revision target
- `suggest_tasks` → Board selects tasks to create

API already exists (`GET/POST /api/tasks/{id}/interactions`). This is a pure web UI sprint.

_Gates the dogfood loop: without this the Board cannot respond to agent questions._

---

### M3 · Wake-on-assign + "Run Now" button — S — starts after M0

Two small wires:

1. When `checkout_agent_id` is set on a task (via UI or API), fire `GlobalDispatcher.Wake(taskID, "assigned", …)` — connects the existing dispatcher to the assignment action
2. Add a "Run Now" button to the task detail page that POSTs to `/api/tasks/{id}/stage` with `assigned` reason, triggering the same wake

_Both lean on code that already exists; this is wiring, not invention._

---

### 🏁 First Dogfood Milestone (after M0 + M1 + M2 + M3)

> **The earliest point where StayPoint work is tracked in StayPoint.**

Definition: Board can create a task in StayPoint, assign it to the Chief of Staff, the CoS wakes automatically, asks a question via an interaction card, Board responds via the StayPoint web UI, CoS wakes again and marks done — **without ever opening Paperclip or a terminal.**

Estimated time from now: **~3 weeks** (M0 ≈ 1 week remaining + M1 ≈ 1 week + M2 ≈ 3 days + M3 ≈ 1 day).

---

### M4 · Agent directory + per-agent inbox — M — post-dogfood

- `/api/agents` list endpoint (minimal: id, name, role, current task)
- Per-agent inbox: `GET /api/tasks?assignedAgent={id}` filter
- Agent page in web UI showing assigned tasks and last run
- Re-hire the Chief of Staff and 2–3 engineers in StayPoint

_Full fleet re-hiring (22 agents) is after cutover._

---

### M5 · Priority field + task search — S — post-dogfood

- Add `priority` column to `tasks` schema (migration)
- Full-text search: `GET /api/tasks?q=...` using SQLite FTS5
- Wire priority filter in web UI to the real DB column

---

### M6 · PR review handoff automation — M — post-dogfood

- When a task transitions to `done` with a `pull_request` work product, auto-create a review subtask assigned to the designated reviewer agent
- Configurable per-project reviewer in config

---

### M7 · Hard cutover: STA work moves to StayPoint — milestone only

When M0–M6 pass a Board acceptance test:
- Stop creating new STA issues in Paperclip
- Existing STA-1..287 stay in Paperclip as read-only archive (clean start; no migration)
- New issues start at STA-288 in StayPoint
- Parallel operation window: up to 8 weeks; hard cutover when Board is confident

---

### Post-cutover backlog (not blocking cutover)

- Labels / tags on tasks
- `preview_url` and `runtime_service` work product types
- File attachments
- Routines / cron triggers
- Approval cards (first-class entity beyond governance tables)
- Multi-company / multi-tenant (separate epic)
- Paperclip migration importer (if archive-only becomes insufficient)

---

## 4. Risks

| ID | Risk | Sev | Mitigation |
|---|---|:-:|---|
| R-01 | Run visibility SSE throughput: high-frequency checkpoint events flood the board | H | Debounce/batch at 200ms; add `level` field so UI can collapse fine-grained steps |
| R-02 | Interaction card web UI: test coverage gap — card rendered but response not delivered to agent | H | E2E test: create interaction, submit response, verify agent wakes with correct payload |
| R-03 | Wake-on-assign races with manual checkout: two agents claim the same task | M | Existing concurrency cap (max 1 active claim) prevents double-run; still add idempotency key on wake |
| R-04 | Quota flip regression (STA-283) not fully resolved before dogfood | M | STA-283 must be `done` before M3 merges; add checklist gate |
| R-05 | Clean-start cuts off Board from historical STA context | M | Keep Paperclip URL in bookmark bar; add a pinned comment on STA-288 with the archive link |
| R-06 | CoS and engineers need re-hiring in StayPoint with correct instructions | M | M4 covers this; CoS instructions are in `~/.paperclip/…/AGENTS.md` — port verbatim |
| R-07 | STA-289 scope creep: micro-checkpoints become a large streaming infrastructure rewrite | M | Scope strictly to emit + display; no persistence of step history beyond in-memory SSE buffer initially |

---

## 5. First Dogfood Milestone (summary)

**Earliest point where StayPoint work is tracked in StayPoint:**

After M0 (STA-282 + STA-283) + M1 (STA-289 run visibility) + M2 (interaction cards in web UI) + M3 (wake-on-assign + Run Now):

> Board creates a task, assigns it to Chief of Staff. CoS wakes, sees a live timeline of its steps, asks a clarifying question via interaction card. Board reads the question and responds in the StayPoint web UI. CoS wakes, completes the task, and marks done. Board sees the final status on the Kanban board.

No Paperclip. No terminal. No spinner.

Estimated target: **~3 weeks from M0 merge**.

---

_See STA-288 for child issues proposed for Board approval._
