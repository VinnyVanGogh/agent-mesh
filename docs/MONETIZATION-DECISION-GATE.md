# StayPoint Native Orchestrator: Monetization Decision Gate Memo

- **Epic:** StayPoint Native Orchestrator (Go, event-driven, BYOC)
- **Task:** T19 / STA-111 (Phase 6 Decision Gate)
- **Repo:** `staypoint` (label `repo:staypoint`)
- **Status:** APPROVED & RECORDED (Board & Executive Decision)
- **Decision:** NO-GO on Paid SaaS / Commercial Tiers. StayPoint remains 100% Free, Local-First, and Open Source.
- **Related Risks Resolved:** K-06 (Provider ToS / pooling liability), K-16 (Scope creep into SaaS diluting personal leverage)
- **Date:** 2026-09-30
- **Author:** Chief of Staff / CEO for the Board

---

## 1. Executive Summary & Board Decision

This memorandum serves as the authoritative Phase 6 decision gate for the StayPoint Native Orchestrator epic (STA-91 / T19), resolving the strategic question of commercialization raised across the research foundation (RES-44 through RES-49).

### Formal Board Decision:
1. **NO-GO on Commercial SaaS, Paid Subscription Tiers, and Cloud Multi-Tenancy.**
   StayPoint will **not** build, package, or offer a paid Pro or Enterprise SaaS tier, hosted inference proxy, cloud synchronization backend, license key DRM, or billing integration (Stripe/Paddle/LemonSqueezy).
2. **Permanent Free and Local-First Commitment.**
   StayPoint is designated permanently as a **local-first personal force multiplier and open-source developer platform (Apache-2.0)**. All capabilities—including the Bubble Tea Chat TUI, Kanban Board TUI, embedded Web UI (`go:embed`), task graph engine, worktree isolation, autonomous orchestrator harness, multi-provider BYOC adapters, and dual-mode Boss Card ROI reporting—remain 100% free and locally executed.
3. **Absolute Prohibition on Billing Code.**
   No billing, licensing, or payment gateway code shall be merged into the repository. Any feature whose sole purpose is enforcing paywalls or managing subscription state is out of scope.

---

## 2. Strategic Evaluation: RES-49 vs. RES-44/46/47

The initial research reports evaluated two diverging trajectories for StayPoint:

```
┌─────────────────────────────────────────┐       ┌─────────────────────────────────────────┐
│     RES-44 / RES-46 / RES-47 Sketches   │       │          RES-49 Moat Analysis           │
├─────────────────────────────────────────┤       ├─────────────────────────────────────────┤
│ • Paid Pro / Enterprise Tiers ($29-$99) │       │ • Paid SaaS Viability Score: 2.5 / 10   │
│ • Central Cloud Sync & Team Relay       │  vs   │ • Recommendation: Personal Tool + OSS   │
│ • Hosted Inference & Multi-Tenant DB    │       │ • Zero token resale or hosted liability │
│ • License Keys & Billing Gateways       │       │ • Focus on maximum internal ROI         │
└─────────────────────────────────────────┘       └─────────────────────────────────────────┘
                                       │
                                       ▼
                       DECISION: ADOPT RES-49 THESIS
                       (REJECT COMMERCIAL SaaS TIERS)
```

### 2.1 The Case Against Commercialization (RES-49 Findings)

RES-49 evaluated the commercial SaaS landscape for AI developer agents and scored a hosted/paid StayPoint product at **2.5 out of 10**, identifying critical structural barriers:

1. **Negative Gross Margins and Platform Risk (Risk K-06):**
   Frontier AI providers (Anthropic, OpenAI, Google) strictly prohibit reselling, pooling, or proxying consumer subscription tokens (Claude Pro/Max, ChatGPT Plus, Gemini Advanced) in commercial SaaS wrappers under their respective Terms of Service. A commercial vendor must either:
   - Resell direct API credits with massive hosting and latency overhead, incurring negative or razor-thin gross margins, or
   - Attempt account pooling/proxying, which triggers immediate account termination, severe legal exposure, and existential platform risk.
   StayPoint's BYOC (Bring Your Own Compute / Credentials) model completely sidesteps this liability by running exclusively on the developer's local machine with their own CLI sessions.

2. **The "Thin Wrapper" Moat Trap:**
   The developer tooling market is saturated with transient agent wrappers. Charging a monthly SaaS subscription for an agent orchestrator creates immense friction, high churn, and an unsustainable Customer Acquisition Cost (CAC) relative to Lifetime Value (LTV).

3. **Core Value Dilution (Risk K-16):**
   StayPoint's highest-leverage attribute is its raw speed, zero-overhead static Go binary, pure SQLite WAL persistence, and local developer ergonomics. Pivoting to a commercial product introduces a massive tax of non-core code: authentication, billing webhooks, account migrations, tenant isolation, SOC 2 compliance, and support tickets.

### 2.2 Re-Evaluating the RES-44/46/47 Tier Sketches

The earlier tier sketches in RES-44 (Paperclip teardown), RES-46 (TUI/adapter architecture), and RES-47 (security/licensing) contemplated monetizing features such as:
- **Cloud Team Relay:** Multi-developer synchronization of agent runs.
- **Hosted Fleet Dashboard:** Centralized web UI for teams.
- **Enterprise Vault:** Remote credential management.

**Reconciliation:**
- *Cloud Team Relay* requires replicating the heavy Node/Postgres stack that crippled Paperclip. By contrast, git is already the distributed team relay. Agents commit atomically to git branches, open PRs, and register deliverables in version control.
- *Hosted Fleet Dashboard* is rendered obsolete by the embedded Web UI (`staypoint web`, T17), which serves a rich Kanban and fleet monitor directly from loopback HTTP using `go:embed` without external servers.
- *Enterprise Vault* is unnecessary under BYOC, where secrets remain in the OS keychain or local file boundaries protected by the security floor (T8).

Building commercial infrastructure would degrade StayPoint into the very tool it was built to replace.

---

## 3. Real Usage Data & Operational Retrospective (Phases 0–5)

Across the implementation and dogfooding of tasks T0 through T18, empirical data firmly validates the local-first, free-tier thesis:

| Capability | Observed Metric & Operational Reality | Impact on Commercialization |
|---|---|---|
| **Binary Footprint** | ~33 MB static binary, zero CGO, <20 MB daemon RAM | Zero cloud infrastructure costs; near-zero maintenance overhead. |
| **Startup & Execution** | Sub-second process dispatch, 30fps debounced TUI | Blazing developer speed without remote network latency. |
| **Event-Driven Dispatch** | 0% idle CPU, zero quota burn while waiting | Solved Paperclip's fatal runaway polling cost (Risk K-03). |
| **Dual-Mode Boss Card** | Thousands of dollars in API-equivalent engineering value generated locally | Proves immense economic value captured directly by the user, not by a SaaS gate. |
| **BYOC Adapters** | 4 providers (Claude, Gemini, Codex, local Ollama) operating simultaneously | Complete data sovereignty; zero provider ToS exposure. |

### Economic Conclusion:
The return on investment (ROI) from StayPoint is realized **through engineering acceleration, automated test verification, and multi-agent coordination on our own products**. Forcing a monetization layer would yield negligible SaaS subscription revenue while crippling internal velocity and alienating the open-source community.

---

## 4. Policy Directives & Scope Boundaries

To ensure complete clarity across all future roadmap cycles, the following policies are codified:

### 4.1 Permitted Roadmap Focus:
- **Autonomous Harness Optimization:** Refining the mechanical completion interceptor, worktree lifecycle, and task retry watchdog.
- **Provider Adapter Expansion:** Maintaining compatibility with updated CLI print modes and local Ollama/vLLM endpoints.
- **Developer Ergonomics:** Enhancing the Bubble Tea TUI, Kanban board, and local telemetry reporting.
- **Local Coordination:** Improving git-native collaboration patterns (PR review tasks, worktree branching).

### 4.2 Prohibited Scope (Hard Constraints):
- **NO Billing Systems:** No integration with payment processors, checkout flows, or metered billing APIs.
- **NO Cloud Sync / Multi-Tenancy:** No hosted multi-tenant databases or remote coordinator servers.
- **NO DRM / License Keys:** No software lockouts, activation keys, or seat limits.
- **NO Token Resale or Pooling:** Strictly BYOC. No centralized credential sharing or inference proxying.

---

## 5. Decision Sign-Off

The monetization decision gate is hereby **PASSED with a permanent NO-GO on commercial paid tiers**. 

StayPoint Native Orchestrator remains an open-source, local-first power tool. Epic STA-91 Phase 6 is complete.

*Approved by the Chief of Staff and ratified for the Board.*
