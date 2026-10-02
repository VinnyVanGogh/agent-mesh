# Roadmap: Replace Paperclip with StayPoint (Dogfood)

> **Status:** In progress — started 2026-10-01. Board approved. T1–T7 assigned.  
> **Scope:** STA company only. Other companies (MAN, PER, RUN) stay in Paperclip until post-cutover parity is proven.  
> **Source plan:** STA-288 revision 3.

---

## North Star

The HTML task-page mockup on STA-288:
[`sta-289-run-timeline.html`](/api/attachments/8b7cce76-7bf5-4dcc-870c-656639611be1/content)

It has a live run timeline, pause/message controls, token/cost stats, a diff sidebar, and two-click Stop. Source is on branch `docs/sta-289-run-timeline-diagram` at `89bcde7` (not merged). Every T1 UI decision anchors to that mockup.

---

## MVP = The Dogfood Loop

You create a task in StayPoint and assign it. It wakes on its own. You watch its steps live. It asks you a question via an interaction card. You answer in the StayPoint web UI. It wakes again, finishes, and marks done. No Paperclip, no terminal, no spinner.

---

## Already Done (from STA-236)

- **STA-282** — task detail as a full URL-routed page (`/tasks/:org/:project/:id`) + interactive session panel ✅
- **STA-283** — claude_work / claude_personal quota flip fix + project card click-through ✅

---

## T1–T7 Scope Table

| Tier | Issue | Work | Depends on | Status vs Code |
|------|-------|------|------------|----------------|
| T1 | **STA-289** | Run timeline — live micro-checkpoint UI built from the north-star mockup. Critical path. | none | **PARTIAL** |
| T2 | **STA-291** | Interaction cards in the web UI — render ask/confirm/suggest cards; Board responds without a terminal | none (runs alongside T1) | **PARTIAL** |
| T3 | **STA-292** | Wake-on-assign — assigning a task from the UI wakes the assigned adapter automatically | T2 | **MISSING** |
| T4 | **STA-293** | Run Now button — Board manually kicks a heartbeat from the task detail page | T3 | **PARTIAL** |
| T5 | **STA-316** | Dogfood acceptance test — Auditor runs the full loop end-to-end, then Board runs it | T1–T4 | **MISSING** |
| T6 | **STA-317** | Refresh `docs/ROADMAP-REPLACE-PAPERCLIP.md` to the approved plan | none | **IN PROGRESS** |
| T7 | **STA-318** | Cutover — new STA work goes into StayPoint; Paperclip becomes read-only fallback | T5 | **MISSING** |

### Code Evidence (checked against repo, not issue status)

**T1 — PARTIAL**  
Backend foundations exist: `internal/checkpoint/` (checkpoint.go, types.go, undo.go), checkpoint creation + diff in `internal/orchestrator/harness.go`, SSE `EventHub` in `internal/server/events.go`.  
Missing: harness does not publish step events to SSE hub; web UI (`internal/server/webui/app.js`) has zero timeline/checkpoint/RunStep rendering — only a "working" spinner.

**T2 — PARTIAL**  
Schema exists: `task_interactions` table in `internal/db/db.go`; context CRUD in `internal/context/` (interactions_test.go confirms create/list/resolve). No HTTP endpoints for interactions in `internal/server/handlers_tasks.go`. Web UI has no card rendering (1 grep hit = a comment in a chat section, not a card component).

**T3 — MISSING**  
`GlobalDispatcher.Wake()` exists in `internal/orchestrator/dispatcher.go` and is called from `internal/mcp/tools.go`. Task update handler does not call `dispatcher.Wake` on assignment change — the assignment→wake wire does not exist in `internal/server/`.

**T4 — PARTIAL**  
`SetStage` handler exists (`POST /api/tasks/{id}/stage` in `internal/server/handlers_tasks.go`). Web UI has no "Run Now" button — zero hits for `run-now`/`RunNow` in `app.js`.

**T5, T7 — MISSING**  
No acceptance test scaffold. No cutover tooling.

**T6 — IN PROGRESS**  
This document.

---

## Backlog (outside T1–T7, not blocking cutover)

From STA-236:
- **STA-284** subtask tree UX
- **STA-285** "commit unknown" banner
- **STA-286** Gemini cost audit
- **STA-287** checklist UX
- **STA-310** TUI timeline (child of STA-289)
- **STA-311** one-topic-per-task intake ← recommended first task after MVP

From the earlier roadmap pass:
- **STA-294** adapter status dashboard
- **STA-296** priority field + DB column
- **STA-297** full-text task search
- **STA-298** PR review handoff automation

Post-cutover (not blocking):
- Labels / tags on tasks
- `preview_url` and `runtime_service` work product types
- File attachments
- Routines / cron triggers
- Approval cards (first-class entity beyond governance tables)
- Multi-company / multi-tenant (separate epic)

---

## Rough Size

| Work | Estimate |
|------|----------|
| T1 (run timeline) | ~1.5–2 weeks |
| T2–T4 (cards, wake, Run Now) — run alongside T1 | ~1 week |
| T5 (acceptance test) | 1–2 days |
| MVP lands | ~2–2.5 weeks from now |

---

_Last updated: 2026-10-01 · Matches STA-288 plan revision 3 · Code audit at commit `98d3994`_
