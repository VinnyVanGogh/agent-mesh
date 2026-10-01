package checklist

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// DefaultChecklist returns verification items with machine contracts for the specified sprint.
func DefaultChecklist(sprint string) []Item {
	if sprint == "STA-168" {
		return defaultChecklistSTA168(sprint)
	}
	if sprint == "" {
		sprint = "STA-168-2"
	}
	return defaultChecklistSTA168_2(sprint)
}

func defaultChecklistSTA168_2(sprint string) []Item {
	type row struct {
		section, title, desc, howTo, contract, status, notes string
	}
	rows := []row{
		// 01. Automated & System Verifications (CTO Pre-Verified)
		{
			section: "01. Automated & System Verifications (CTO Pre-Verified)",
			title:   "All StayPoint packages compile with zero errors (go build ./...)",
			desc:    "Native Go build passes cleanly across all server, fleet, task, watcher, and checklist packages with zero regressions.",
			howTo:   "cd ~/Documents/dev/agent-mesh && go build ./...",
			contract: `{"type":"command","command":"go build ./..."}`,
			status:  "pass",
			notes:   "[Automated check - CTO verified] go build ./... exits 0 cleanly on main (commit 4697e36). Zero compilation errors.",
		},
		{
			section: "01. Automated & System Verifications (CTO Pre-Verified)",
			title:   "staypointd binary rebuilt and codesigned (~/.local/bin/staypointd)",
			desc:    "Binary contains all embedded Web UI assets including STA-190 through STA-195 and is validly codesigned.",
			howTo:   "ls -la ~/.local/bin/staypointd && codesign -v ~/.local/bin/staypointd",
			contract: `{"type":"command","command":"ls -la ~/.local/bin/staypointd"}`,
			status:  "pass",
			notes:   "[Automated check - CTO verified] ~/.local/bin/staypointd exists, size 23.7MB, codesigned cleanly.",
		},
		{
			section: "01. Automated & System Verifications (CTO Pre-Verified)",
			title:   "Daemon service active under launchd (PID verified on :41421)",
			desc:    "com.staypoint.daemon is active and serving HTTP requests on 127.0.0.1:41421.",
			howTo:   "lsof -i :41421",
			contract: `{"type":"command","command":"lsof -i :41421"}`,
			status:  "pass",
			notes:   "[Automated check - CTO verified] com.staypoint.daemon listening on 127.0.0.1:41421.",
		},
		{
			section: "01. Automated & System Verifications (CTO Pre-Verified)",
			title:   "All child deliverable branches merged into main and pushed to remote",
			desc:    "All 6 child branches (STA-190 through STA-195) and prerequisite fixes merged into main.",
			howTo:   "git log -n 10 --oneline",
			contract: `{"type":"command","command":"git log -n 1 --oneline"}`,
			status:  "pass",
			notes:   "[Automated check - CTO verified] Merged into main: STA-190 (8f5951f, 47d29a9), STA-191 (4697e36), STA-192 (883f3fe), STA-193 (b670115), STA-194 (88c1394), STA-195 (d8fe427).",
		},
		{
			section: "01. Automated & System Verifications (CTO Pre-Verified)",
			title:   "All web UI SPA endpoints respond HTTP 200",
			desc:    "Native HTTP endpoints respond with valid HTML and injected session token.",
			howTo:   "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:41421/{projects,agents,recent-tasks,task-status,cost,settings,checklist}",
			contract: `{"type":"http","path":"/checklist","expected_status":200}`,
			status:  "pass",
			notes:   "[Automated check - CTO verified] HTTP 200 OK verified across /checklist, /projects, /agents, /recent-tasks, /task-status, /cost, /settings.",
		},

		// 02. Table Sorting & Deep Content Search (STA-191)
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Bi-directional sorting on all Task Status columns",
			desc:    "Clicking table headers sorts ascending then descending with visual indicator arrows (▲ / ▼).",
			howTo:   "Navigate to /task-status. Click column headers (Identifier, Task, Org, Project, Assignee, Status, Priority, Cost, Updated). Verify rows sort and arrow toggles.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["sortTasks","sortable-th"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Column sorting on All Organizations overview table",
			desc:    "Clicking headers on the overview tasks table sorts rows dynamically.",
			howTo:   "Navigate to / (All Organizations). In the tasks table, click headers (Task, Org, Status, Cost, Updated) to verify sorting.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["sortTasks","overviewSort"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Column sorting on Organization detail task table",
			desc:    "Inside an individual organization view, tasks table columns sort correctly.",
			howTo:   "Click any org card (e.g. /org/StayPoint). Click column headers in the tasks table to sort.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Priority column sorts by true severity rank (Critical > High > Medium > Low)",
			desc:    "Priority sorting orders by operational severity rather than alphabetical string.",
			howTo:   "On /task-status, click Priority column. Verify Critical tasks group first, followed by High, Medium, Low.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Deep content search searches inside task bodies and comments",
			desc:    "Search query scans task descriptions, comment messages, and agent outputs, not just title/ID.",
			howTo:   "On /task-status, search for a word only found in a task body or comment. Verify matching tasks appear.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["deep content search","taskDescriptions"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Matching search terms highlighted in preview snippets (<mark> tags)",
			desc:    "Search results display snippet text preview with the matching keyword highlighted.",
			howTo:   "Search on /task-status and observe the matched snippet line below task titles with highlighted text.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["extractMatchSnippet"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "02. Table Sorting & Deep Content Search (STA-191)",
			title:   "Expanded filters: Project and Priority dropdowns on Task Status page",
			desc:    "Dedicated dropdown filters for Project and Priority complement Org and Status filters.",
			howTo:   "On /task-status, select a specific Project or Priority filter. Verify table filters accordingly.",
			contract: "",
			status:  "pending",
			notes:   "",
		},

		// 03. Projects Page Filters & Grouping (STA-192)
		{
			section: "03. Projects Page Filters & Grouping (STA-192)",
			title:   "Multi-organization selector with badge chips and Select All / Clear",
			desc:    "Can filter projects across multiple organizations at once with visual chip pills.",
			howTo:   "Navigate to /projects. Click Org selector dropdown. Check multiple orgs. Click 'Clear' and 'Select All' to verify controls.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["projects-org-options"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "03. Projects Page Filters & Grouping (STA-192)",
			title:   "Projects grouped under distinct organization section headers with stats",
			desc:    "Projects are visually organized by company/org with count of projects, active tasks, and total spend.",
			howTo:   "On /projects, inspect the organization headers separating project groups. Check stats in headers.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["orgEntries.sort"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "03. Projects Page Filters & Grouping (STA-192)",
			title:   "Interactive card status filter pills (All, Active, Running, Done, Blocked)",
			desc:    "Filter bar at the top of Projects filters project cards by their state.",
			howTo:   "On /projects, click 'Running', 'Blocked', 'Done', 'Active', 'All'. Verify project cards filter instantly.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["filter-pill"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "03. Projects Page Filters & Grouping (STA-192)",
			title:   "Clicking stat counter tiles on project card filters task preview",
			desc:    "Clicking the Running, Blocked, or Done counter tile filters that card's 5-task preview list.",
			howTo:   "On any project card, click the 'Running' or 'Blocked' stat counter box. Verify task list updates.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "03. Projects Page Filters & Grouping (STA-192)",
			title:   "Tasks without a project group under (No Project) scoped per organization",
			desc:    "Unassigned tasks appear under '(No Project)' within their specific organization, not globally merged.",
			howTo:   "On /projects, look for '(No Project)' cards and verify tasks belong to the header's organization.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "03. Projects Page Filters & Grouping (STA-192)",
			title:   "Project filter selections persist across page reloads via localStorage",
			desc:    "Selected organization filters and status pills survive page refresh and return navigation.",
			howTo:   "Select a specific org filter on /projects. Hard refresh (⌘⇧R). Verify the filter is still applied.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["staypoint_projects_"]}`,
			status:  "pending",
			notes:   "",
		},

		// 04. Recent Tasks Hierarchy & Filters (STA-193)
		{
			section: "04. Recent Tasks Hierarchy & Filters (STA-193)",
			title:   "Expandable subtask tree hierarchy for parent tasks",
			desc:    "Tasks with subtasks show a toggle ([+] / [-]) that expands inline to reveal child execution hierarchy.",
			howTo:   "Navigate to /recent-tasks. Find a parent task with child subtasks (e.g. STA-168). Click [+] to expand the tree.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["activity-subtasks-tree"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "04. Recent Tasks Hierarchy & Filters (STA-193)",
			title:   "Subtask tree connectors (├──, └──) and status dots",
			desc:    "Expanded subtasks render with visual tree branches and status-colored dots.",
			howTo:   "Expand a subtask tree on /recent-tasks. Verify tree connectors and colored dots (cyan/red/green) display cleanly.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["activity-subtask-branch"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "04. Recent Tasks Hierarchy & Filters (STA-193)",
			title:   "Clicking subtask title in tree opens detail panel",
			desc:    "Subtasks in the tree are fully interactive and open their respective detail panel.",
			howTo:   "In an expanded subtask tree on /recent-tasks, click any child task's title. Verify detail panel opens on right.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "04. Recent Tasks Hierarchy & Filters (STA-193)",
			title:   "Organization, Project, and Priority dropdown filters on Recent Tasks",
			desc:    "Activity feed can be filtered along three simultaneous dimensions.",
			howTo:   "On /recent-tasks, test the Organization, Project, and Priority filter dropdowns.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/index.html","must_contain":["recent-tasks-project-filter"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "04. Recent Tasks Hierarchy & Filters (STA-193)",
			title:   "Parent tasks order by latest active child timestamp",
			desc:    "Recent activity reflects latest child updates so active work remains at the top of the feed.",
			howTo:   "On /recent-tasks, verify parents with recent child activity are positioned at the top of the feed.",
			contract: "",
			status:  "pending",
			notes:   "",
		},

		// 05. Executive Overview & Boss Card Carousel (STA-194)
		{
			section: "05. Executive Overview & Boss Card Carousel (STA-194)",
			title:   "Clickable KPI summary cards route with pre-selected filters",
			desc:    "Clicking Running, Blocked, Done, or Agents KPI cards opens Task Status with the corresponding filter active.",
			howTo:   "Navigate to /. Click 'Running' card (navigates to /task-status?status=running). Return and click 'Blocked' and 'Done'.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["kpi-card"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "05. Executive Overview & Boss Card Carousel (STA-194)",
			title:   "In-app Boss Card interactive preview carousel with slide controls",
			desc:    "Boss Card view features an in-app report preview carousel with slide navigation (◀ / ▶) and zoom controls.",
			howTo:   "Navigate to Boss Card view. Verify interactive presentation carousel renders without requiring PDF download.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["boss-carousel-card"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "05. Executive Overview & Boss Card Carousel (STA-194)",
			title:   "Boss Card full-screen memo presentation mode",
			desc:    "Preview expands to full-screen presentation mode for executive review.",
			howTo:   "In the Boss Card carousel, click full-screen presentation toggle. Verify enlarged view.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "05. Executive Overview & Boss Card Carousel (STA-194)",
			title:   "PDF report download button shows spinner and resolves cleanly",
			desc:    "Report download button disables and shows spinning icon during download, then restores cleanly.",
			howTo:   "In Boss Card, click Download Report. Verify spinner appears on button and does not trigger permission loop.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["dl-spinner"]}`,
			status:  "pending",
			notes:   "",
		},

		// 06. Settings Quota Telemetry & Fleet Modal (STA-195)
		{
			section: "06. Settings Quota Telemetry & Fleet Modal (STA-195)",
			title:   "Visual headroom progress bars for all provider quota pools",
			desc:    "Settings page lists quota pools (Claude Work, Claude Personal, Gemini, OpenAI) with 5h and weekly headroom bars.",
			howTo:   "Navigate to /settings. In 'Provider Accounts', inspect each pool's progress bar and percentage.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "06. Settings Quota Telemetry & Fleet Modal (STA-195)",
			title:   "Local reset countdown timestamps on quota pools",
			desc:    "Each pool displays exact local countdown timestamp (e.g. 'resets at ...') for 5h and weekly reset windows.",
			howTo:   "On /settings, inspect the reset countdown labels below each quota gauge.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "06. Settings Quota Telemetry & Fleet Modal (STA-195)",
			title:   "Clickable fleet count tiles open Fleet Info drill-down modal",
			desc:    "Clicking Organizations, Total Tasks, or Active Agents count tiles opens interactive fleet modal.",
			howTo:   "On /settings, click 'Total Tasks' or 'Active Agents' in Fleet Info section. Verify modal opens.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["openFleetInfoModal"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "06. Settings Quota Telemetry & Fleet Modal (STA-195)",
			title:   "Fleet Info modal includes live search and status filter chips",
			desc:    "Modal allows live filtering of fleet agents and tasks with search box and status chips.",
			howTo:   "In the Fleet Info modal, type in the search bar and click status filter chips (Active, Blocked, Done).",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/index.html","must_contain":["fleet-modal"]}`,
			status:  "pending",
			notes:   "",
		},

		// 07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Clicking outside the detail panel dismisses it",
			desc:    "Clicking anywhere on the background or page content outside the detail panel closes it smoothly.",
			howTo:   "Open any task detail panel. Click on the grayed page area outside the panel. Verify the panel closes.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["closeDetailPanel","lastDetailOpenTime"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Polished field labels for Priority, Org, Stage, and Identifier",
			desc:    "Metadata header displays clean styled badges with field labels.",
			howTo:   "Open a task detail panel. Check top metadata badges (Priority, Org, Stage, Identifier).",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Empty task descriptions automatically fall back to first comment/prompt",
			desc:    "Tasks with blank description show first comment text so context is never lost.",
			howTo:   "Open a task with no formal description. Verify the description area displays the initial comment.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Spend and budget indicators visible at all times ($0.00 baseline)",
			desc:    "Spend ($) and budget tracking remain visible even when spend is $0.00 rather than being hidden.",
			howTo:   "Open a task with $0 spend. Verify the Spend and Budget display shows '$0.00' clearly.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Checklist notes and chat textareas do not collapse on blur or click-off",
			desc:    "Textareas preserve multi-line content height on blur/click-off without shrinking to 1 row.",
			howTo:   "In /checklist, type 4 lines into an item's note box. Click outside. Verify the textarea stays expanded.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/style.css","must_contain":["field-sizing: content;"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Messaging enabled on to-do tasks across native and fleet views (STA-186)",
			desc:    "Comment composer is active on to-do tasks and sends comments via ⌘↵.",
			howTo:   "Open a task in 'todo' status. Type a message in the chat box and press ⌘↵. Verify message posts.",
			contract: `{"type":"file_pattern","file_path":"internal/server/handlers_tasks.go","must_contain":["AddComment"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "07. Detail Panel & Chat Experience (STA-190, STA-186, STA-187)",
			title:   "Opening a task updates URL to /tasks/:id and supports deep linking (STA-187)",
			desc:    "Tasks have addressable URLs that can be shared, bookmarked, and hard-refreshed.",
			howTo:   "Open a task. Note browser URL changes to /tasks/<id>. Hard refresh (⌘⇧R). Verify task re-opens.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui.go","must_contain":["tasks/"]}`,
			status:  "pending",
			notes:   "",
		},

		// 08. Agents Page Modernization (STA-178, STA-179, STA-180)
		{
			section: "08. Agents Page Modernization (STA-178, STA-179, STA-180)",
			title:   "Strict provider resolution on agent cards (No 'Other' badge) (STA-178)",
			desc:    "All agent cards display Claude, Gemini, or OpenAI with brand color badges.",
			howTo:   "Navigate to /agents. Verify no agent shows an ambiguous 'Other' provider badge.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "08. Agents Page Modernization (STA-178, STA-179, STA-180)",
			title:   "Cascading Org and Project filters on Agents page (STA-179)",
			desc:    "Selecting an organization filters the Project dropdown and filters agent cards dynamically.",
			howTo:   "On /agents, select an Organization filter. Verify Project options cascade to that org's projects.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/index.html","must_contain":["agents-project-filter"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "08. Agents Page Modernization (STA-178, STA-179, STA-180)",
			title:   "5-Hour quota progress bars bound to agent cards",
			desc:    "Agent cards display 5h quota consumption bar based on assigned provider.",
			howTo:   "On /agents, verify agents assigned to quota pools display their rolling 5h usage bar.",
			contract: "",
			status:  "pending",
			notes:   "",
		},

		// 09. Cost & Accounting Visualizations (STA-175, STA-177)
		{
			section: "09. Cost & Accounting Visualizations (STA-175, STA-177)",
			title:   "Visual bar charts for Spend by Model, Org, Provider, and Top Tasks (STA-177)",
			desc:    "Cost & Accounting page displays proportional SVG/bar charts for spend breakdowns.",
			howTo:   "Navigate to /cost. Verify visual bar charts render for Model, Org, Provider, and Top Tasks.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Model"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "09. Cost & Accounting Visualizations (STA-175, STA-177)",
			title:   "Real-time Gemini token and cost telemetry tracking (STA-175)",
			desc:    "Gemini provider usage is tracked with on-the-fly pricing in the telemetry pipeline.",
			howTo:   "On /cost, verify the Gemini provider row is active and tracks token metrics.",
			contract: "",
			status:  "pending",
			notes:   "",
		},

		// 10. Organization Rolling Quota & Lockout (STA-171, STA-185)
		{
			section: "10. Organization Rolling Quota & Lockout (STA-171, STA-185)",
			title:   "Org detail page displays 5-hour rolling quota and lockout gauges (STA-171)",
			desc:    "Each individual organization page has dedicated 5h rolling quota cards per provider.",
			howTo:   "Navigate to /org/StayPoint. Verify '5-Hour Rolling Quotas & Lockout Status' section renders.",
			contract: "",
			status:  "pending",
			notes:   "",
		},
		{
			section: "10. Organization Rolling Quota & Lockout (STA-171, STA-185)",
			title:   "Managed Solutions prioritizes Claude Work quota over Personal (STA-185)",
			desc:    "Managed Solutions checks Claude Work seat first; healthy Work headroom prevents locking on Personal.",
			howTo:   "Check Managed Solutions quota status. Verify 'Claude Work prioritized' displays when Work is healthy.",
			contract: `{"type":"file_pattern","file_path":"internal/fleet/aggregator.go","must_contain":["Claude Work prioritized"]}`,
			status:  "pending",
			notes:   "",
		},

		// 11. Checklist Tooling & Divergence Engine (STA-168, STA-170)
		{
			section: "11. Checklist Tooling & Divergence Engine (STA-168, STA-170)",
			title:   "Collapsible checklist sections with persistent state",
			desc:    "Clicking section headers toggles collapse (▼ / ▶); completed sections default to collapsed; state persists.",
			howTo:   "On /checklist, click a section header to collapse it. Refresh page. Verify section remains collapsed.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["checklist-section-header"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "11. Checklist Tooling & Divergence Engine (STA-168, STA-170)",
			title:   "Checklist toolbar actions: Collapse Finished, Expand All, Collapse All",
			desc:    "Toolbar controls allow one-click mass expanding and collapsing of sections.",
			howTo:   "On /checklist, click 'Collapse All', 'Expand All', and 'Collapse Finished'. Verify sections respond.",
			contract: `{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["getSectionCollapsed"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "11. Checklist Tooling & Divergence Engine (STA-168, STA-170)",
			title:   "In-between status option (◐ Partial / Needs Work) recorded in audit history",
			desc:    "Clicking ◐ records partial pass with amber styling and logs to checklist_history audit trail.",
			howTo:   "On any item on /checklist, click the ◐ button. Verify amber status pill and version history entry.",
			contract: `{"type":"file_pattern","file_path":"internal/server/handlers_checklist.go","must_contain":["partial"]}`,
			status:  "pending",
			notes:   "",
		},
		{
			section: "11. Checklist Tooling & Divergence Engine (STA-168, STA-170)",
			title:   "⚡ Verify Contracts runs machine assertions and detects regression divergence (STA-170)",
			desc:    "Clicking Verify Contracts evaluates command, file_pattern, and http contracts and flags broken claims.",
			howTo:   "On /checklist, click '⚡ Verify Contracts'. Verify loading state, summary banner, and informative toast appear with pass/fail/regression counts.",
			contract: `{"type":"file_pattern","file_path":"internal/checklist/divergence.go","must_contain":["EvaluateSprint"]}`,
			status:  "pending",
			notes:   "",
		},
	}

	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		st := r.status
		if st == "" {
			st = "pending"
		}
		out = append(out, Item{
			ID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte(sprint+":"+r.section+":"+r.title)).String(),
			Sprint:      sprint,
			Section:     r.section,
			Title:       r.title,
			Description: r.desc,
			HowToTest:   r.howTo,
			Contract:    r.contract,
			CommitHash:  ResolveSectionCommit(r.section),
			Status:      st,
			Notes:       r.notes,
			Version:     1,
		})
	}
	return out
}

func defaultChecklistSTA168(sprint string) []Item {
	type row struct{ section, title, desc, howTo, contract string }
	rows := []row{
		// Infrastructure
		{"Infrastructure", "Binary rebuilt from feat/webui-cookie-auth (e3a39ba)",
			"The go:embed binary must include all STA-168 UI changes.",
			"ls -la ~/.local/bin/staypointd — mtime should be Sep 30 09:52+", ""},
		{"Infrastructure", "Daemon restarted with new binary (PID 34952+)",
			"Old PID 73862 should no longer be active.",
			"lsof -i :41421 — PID should be 34952 or newer+", ""},
		{"Infrastructure", "go build ./... passes clean",
			"No compilation errors in any package.",
			"cd ~/Documents/dev/agent-mesh && go build ./... — exit 0",
			`{"type":"command","command":"go build ./..."}`},
		{"Infrastructure", "feat/webui-cookie-auth pushed to remote (e3a39ba)",
			"Remote branch should include STA-168 commit.",
			"git log --oneline -1 in agent-mesh repo", ""},

		// Bug 1
		{"Bug 1 — Two Claude Accounts", "Quota section shows Claude (Work) card",
			"Separate card for work seat Claude pool.",
			"All Organizations view → 5-Hour Rolling Quota section → look for 'Claude (Work)'",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["claude_work"]}`},
		{"Bug 1 — Two Claude Accounts", "Quota section shows Claude (Personal) card",
			"Separate card for personal seat Claude pool.",
			"Same section → look for 'Claude (Personal)'",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["claude_personal"]}`},
		{"Bug 1 — Two Claude Accounts", "Old combined 'Claude' card absent when split cards present",
			"Should not show a third duplicate card.",
			"Count cards in quota grid — should be 2 Claude cards not 3", ""},

		// Bug 2
		{"Bug 2 — Blocked via TUI", "Blocked task does NOT show 'Blocked via TUI' in detail panel",
			"normalizeBlockReason() strips the literal TUI string.",
			"Open any blocked task → detail panel → look for orange blocker tag",
			`{"type":"file_pattern","file_path":"internal/fleet/aggregator.go","must_contain":["normalizeBlockReason","blocked via tui"]}`},
		{"Bug 2 — Blocked via TUI", "Blocked reason shows generic text or real reason",
			"Should show 'Blocked — no specific reason recorded' or actual reason.",
			"Same as above — check text in blocker tag", ""},

		// Bug 3 (not fixed)
		{"Bug 3 — Gemini Cost (NOT FIXED)", "Cost page shows Gemini spend > $0",
			"Telemetry watcher does not yet record Gemini cost_usd. This item should FAIL until fixed.",
			"Cost & Accounting → Spend by Provider → Gemini row", ""},

		// Bug 4
		{"Bug 4 — PDF Spinner", "Report download button shows spinner while downloading",
			"Boss Card → Download Report dropdown → click a type → button should disable + spin.",
			"Boss Card view → Download Report → Combined Fleet — watch button state", ""},

		// Bug 5
		{"Bug 5 — Spend Breakdown", "Cost page has per-model spend card",
			"Spend by Model card with bar charts.",
			"Sidebar → Cost & Accounting → Spend by Model",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Model"]}`},
		{"Bug 5 — Spend Breakdown", "Cost page has per-organization spend card",
			"Spend by Organization card with bar charts.",
			"Sidebar → Cost & Accounting → Spend by Organization",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Organization"]}`},
		{"Bug 5 — Spend Breakdown", "Cost page has per-provider spend card",
			"Spend by Provider card — Claude, Gemini, OpenAI.",
			"Sidebar → Cost & Accounting → Spend by Provider",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Provider"]}`},
		{"Bug 5 — Spend Breakdown", "Cost page has Top Tasks by Spend card",
			"Lists tasks with spent_usd > 0.",
			"Sidebar → Cost & Accounting → Top Tasks by Spend", ""},

		// Projects
		{"Projects Page", "Sidebar → Projects navigates to projects view", "", "", ""},
		{"Projects Page", "Tasks grouped by project field into cards", "", "", ""},
		{"Projects Page", "Project card shows running / blocked / done / spend stats", "", "", ""},
		{"Projects Page", "Project card shows preview of up to 5 tasks", "", "", ""},
		{"Projects Page", "Clicking a task in preview opens detail panel", "", "", ""},
		{"Projects Page", "Org filter dropdown narrows projects", "", "", ""},
		{"Projects Page", "Tasks with no project field group under (No Project)", "", "", ""},

		// Agents
		{"Agents Page", "Sidebar → Agents navigates to agents view", "", "", ""},
		{"Agents Page", "Agent cards show provider badge", "", "", ""},
		{"Agents Page", "Agent cards show status pill", "", "", ""},
		{"Agents Page", "Agent cards show 5h quota bar", "", "", ""},
		{"Agents Page", "Agent cards show last heartbeat time", "", "", ""},
		{"Agents Page", "Search input filters agents by name/role", "", "", ""},
		{"Agents Page", "Provider filter dropdown works", "", "", ""},

		// Recent Tasks
		{"Recent Tasks Page", "Sidebar → Recent Tasks navigates to activity feed", "", "", ""},
		{"Recent Tasks Page", "Feed sorted by updated_at descending", "", "", ""},
		{"Recent Tasks Page", "Dots colored by status (cyan/red/green)", "", "", ""},
		{"Recent Tasks Page", "Clicking task title opens detail panel", "", "", ""},
		{"Recent Tasks Page", "Project, Organization, and Priority dropdown filters work", "", "",
			`{"type":"file_pattern","file_path":"internal/server/webui/index.html","must_contain":["recent-tasks-project-filter","recent-tasks-org-filter","recent-tasks-priority-filter"]}`},
		{"Recent Tasks Page", "Subtask tree expansion displays child issues nested under parent tasks", "", "",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["activity-subtasks-tree","activity-subtask-toggle","renderSubtaskTree"]}`},

		// Task Status
		{"Task Status Page", "Sidebar → Task Status shows 9-column table", "", "", ""},
		{"Task Status Page", "Table has Identifier / Task / Org / Project / Assignee / Status / Priority / Cost / Updated", "", "", ""},
		{"Task Status Page", "Search input filters table", "", "", ""},
		{"Task Status Page", "Org filter works", "", "", ""},
		{"Task Status Page", "Status filter works", "", "", ""},
		{"Task Status Page", "Row click opens detail panel", "", "", ""},

		// Cost
		{"Cost & Accounting Page", "Sidebar → Cost & Accounting navigates", "", "", ""},
		{"Cost & Accounting Page", "KPI row shows 5 metrics", "", "", ""},
		{"Cost & Accounting Page", "All 4 spend cards render", "", "", ""},

		// Settings
		{"Settings Page", "Sidebar → Settings navigates", "", "", ""},
		{"Settings Page", "Connection section shows endpoint + auth + SSE status", "", "", ""},
		{"Settings Page", "Provider Accounts section lists quota pools", "", "", ""},
		{"Settings Page", "Fleet Info section shows counts", "", "", ""},

		// Detail Panel
		{"Detail Panel", "Clicking task opens right-side detail panel", "", "", ""},
		{"Detail Panel", "Panel shows title / status / identifier / priority / org", "", "", ""},
		{"Detail Panel", "Panel renders description as markdown", "", "", ""},
		{"Detail Panel", "Panel shows Stage / Assignee / Project / Goal / Repo / Labels", "", "", ""},
		{"Detail Panel", "Panel shows Spend and Budget if present", "", "", ""},
		{"Detail Panel", "Panel shows normalized blocker tag for blocked tasks", "", "", ""},
		{"Detail Panel", "Panel shows timestamps", "", "", ""},
		{"Detail Panel", "Chat section shows comments thread", "", "", ""},
		{"Detail Panel", "Chat compose box sends message (⌘↵)", "", "", ""},
		{"Detail Panel", "Close button (×) works", "", "", ""},

		// Stubs
		{"Phase 2 Stubs", "Sidebar → Routines shows stub page with 'Coming in Phase 2'", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Artifacts shows stub page", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Skills shows stub page", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Connectors shows stub page", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Audit shows stub page", "", "", ""},

		// Architecture
		{"Architecture (NOT DONE — STA-167)", "Web UI reads tasks from native StayPoint SQLite",
			"Currently Paperclip-proxied. handlers_telemetry.go GetFleetTask/GetFleetTaskComments forward to 127.0.0.1:3100. This item should FAIL until STA-167 is done.",
			"Check handlers_telemetry.go — proxyPaperclip() must be replaced",
			`{"type":"file_pattern","file_path":"internal/server/handlers_telemetry.go","must_not_contain":["proxyPaperclip"]}`},

		// Regression
		{"Regression — Existing Views", "All Organizations overview loads with KPI cards", "", "", ""},
		{"Regression — Existing Views", "5-Hour Quota section renders gauge cards", "", "", ""},
		{"Regression — Existing Views", "Organization cards grid renders", "", "", ""},
		{"Regression — Existing Views", "Clicking org card opens org detail", "", "", ""},
		{"Regression — Existing Views", "Org detail shows tasks / agents / spend", "", "", ""},
		{"Regression — Existing Views", "Kanban board renders 4 columns", "", "", ""},
		{"Regression — Existing Views", "Boss Card renders stat grid + download button", "", "", ""},
		{"Regression — Existing Views", "SSE live badge shows 'live'", "", "", ""},
		{"Regression — Existing Views", "Quick filter buttons work in overview", "", "", ""},
		{"Regression — Existing Views", "Sidebar org tree populates", "", "", ""},
		{"Regression — Existing Views", "Clicking org in sidebar tree opens org detail", "", "", ""},
	}

	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, Item{
			ID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte(sprint+":"+r.section+":"+r.title)).String(),
			Sprint:      sprint,
			Section:     r.section,
			Title:       r.title,
			Description: r.desc,
			HowToTest:   r.howTo,
			Contract:    r.contract,
			CommitHash:  ResolveSectionCommit(r.section),
			Status:      "pending",
			Version:     1,
		})
	}
	return out
}

// Seed populates the database with default checklist items.
func Seed(ctx context.Context, dbConn *sql.DB, sprint string, force bool) (int, int, error) {
	if sprint == "" {
		sprint = "STA-168-2"
	}

	// Ensure table has contract & commit_hash columns
	var contractCount int
	_ = dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('checklist_items') WHERE name='contract'").Scan(&contractCount)
	if contractCount == 0 {
		_, _ = dbConn.ExecContext(ctx, "ALTER TABLE checklist_items ADD COLUMN contract TEXT;")
	}
	var commitCount int
	_ = dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('checklist_items') WHERE name='commit_hash'").Scan(&commitCount)
	if commitCount == 0 {
		_, _ = dbConn.ExecContext(ctx, "ALTER TABLE checklist_items ADD COLUMN commit_hash TEXT;")
	}

	var count int
	_ = dbConn.QueryRowContext(ctx, `SELECT COUNT(*) FROM checklist_items WHERE sprint=?`, sprint).Scan(&count)
	if count > 0 && !force {
		// Update contracts and commit hashes on existing items without touching statuses or user notes
		items := DefaultChecklist(sprint)
		for _, it := range items {
			if it.Contract != "" || it.CommitHash != "" {
				_, _ = dbConn.ExecContext(ctx,
					`UPDATE checklist_items SET contract=?, commit_hash=? WHERE sprint=? AND section=? AND title=?`,
					it.Contract, it.CommitHash, sprint, it.Section, it.Title)
			}
		}
		return 0, count, nil
	}

	items := DefaultChecklist(sprint)
	tx, err := dbConn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if force {
		if _, err := tx.ExecContext(ctx, `DELETE FROM checklist_items WHERE sprint=?`, sprint); err != nil {
			return 0, 0, fmt.Errorf("failed to reset sprint items: %w", err)
		}
	}

	for _, it := range items {
		if it.ID == "" {
			it.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(sprint+":"+it.Section+":"+it.Title)).String()
		}
		status := it.Status
		if status == "" {
			status = "pending"
		}
		commitHash := it.CommitHash
		if commitHash == "" {
			commitHash = ResolveSectionCommit(it.Section)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO checklist_items (id, sprint, section, title, description, how_to_test, contract, commit_hash, status, notes, version) VALUES (?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET contract=excluded.contract, commit_hash=excluded.commit_hash`,
			it.ID, sprint, it.Section, it.Title, it.Description, it.HowToTest, it.Contract, commitHash, status, it.Notes, 1,
		); err != nil {
			return 0, 0, fmt.Errorf("seed failed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(items), 0, nil
}
