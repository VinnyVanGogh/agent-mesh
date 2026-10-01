package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterUIRoutes_Root(t *testing.T) {
	mux := http.NewServeMux()
	const token = "test-tok-abc"
	RegisterUIRoutes(mux, token)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "staypoint-token") {
		t.Error("response missing staypoint-token meta tag")
	}
	if !strings.Contains(body, token) {
		t.Errorf("response missing injected token %q", token)
	}
	if !strings.Contains(body, "StayPoint") {
		t.Error("response missing StayPoint title")
	}
}

func TestRegisterUIRoutes_StaticAssets(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "tok")

	for _, path := range []string{"/ui/style.css", "/ui/app.js"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d", path, w.Code)
		}
	}
}

func TestRegisterUIRoutes_NotFoundForUnknownPath(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "tok")

	req := httptest.NewRequest("GET", "/unknown/path", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown path, got %d", w.Code)
	}
}

func TestRegisterUIRoutes_TokenXSSEscape(t *testing.T) {
	mux := http.NewServeMux()
	// Token with characters that must be HTML-escaped in attribute context
	const maliciousToken = `"><script>alert(1)</script>`
	RegisterUIRoutes(mux, maliciousToken)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("token was not HTML-escaped — XSS risk in meta content attribute")
	}
}

func TestRegisterUIRoutes_SPARoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-tok")

	routes := []string{
		"/checklist",
		"/projects",
		"/agents",
		"/cost",
		"/task-status",
		"/recent-tasks",
		"/settings",
		"/org/StayPoint",
	}

	for _, route := range routes {
		req := httptest.NewRequest("GET", route, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 for SPA route %q, got %d", route, w.Code)
		}
		if !strings.Contains(w.Body.String(), "staypoint-token") {
			t.Errorf("SPA route %q response missing staypoint-token meta tag", route)
		}
	}
}

func TestWebUI_DetailPanelPolishAndDismiss(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-tok")

	// Verify app.js serves required detail panel behavior
	reqJS := httptest.NewRequest("GET", "/ui/app.js", nil)
	wJS := httptest.NewRecorder()
	mux.ServeHTTP(wJS, reqJS)
	if wJS.Code != http.StatusOK {
		t.Fatalf("expected 200 for app.js, got %d", wJS.Code)
	}
	appJS := wJS.Body.String()

	// 1. Click-outside-to-dismiss behavior
	if !strings.Contains(appJS, "closeDetailPanel") {
		t.Error("app.js missing closeDetailPanel function")
	}
	if !strings.Contains(appJS, "lastDetailOpenTime") {
		t.Error("app.js missing lastDetailOpenTime guard against bubbling click dismissal")
	}

	// 2. Field label polish: Priority, Org, Stage, Identifier
	for _, label := range []string{"Priority", "Org", "Stage", "Identifier"} {
		if !strings.Contains(appJS, label) {
			t.Errorf("app.js missing explicit field label %q", label)
		}
	}

	// 3. Fallback to rendering first comment when description is blank
	if !strings.Contains(appJS, "task.comments") {
		t.Error("app.js missing comment inspection for description fallback")
	}

	// Verify style.css serves required typography classes
	reqCSS := httptest.NewRequest("GET", "/ui/style.css", nil)
	wCSS := httptest.NewRecorder()
	mux.ServeHTTP(wCSS, reqCSS)
	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 for style.css, got %d", wCSS.Code)
	}
	styleCSS := wCSS.Body.String()

	if !strings.Contains(styleCSS, ".panel-meta-tag-label") {
		t.Error("style.css missing .panel-meta-tag-label typography rule")
	}
	if !strings.Contains(styleCSS, ".panel-meta-item") {
		t.Error("style.css missing .panel-meta-item rule")
	}
}

func TestWebUI_SettingsQuotaAndFleetModal(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-token")

	// 1. Verify index.html contains the modal markup
	reqRoot := httptest.NewRequest("GET", "/", nil)
	wRoot := httptest.NewRecorder()
	mux.ServeHTTP(wRoot, reqRoot)
	if wRoot.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wRoot.Code)
	}
	html := wRoot.Body.String()
	for _, expected := range []string{
		`id="fleet-modal"`,
		`id="fleet-modal-title"`,
		`id="modal-tab-orgs"`,
		`id="modal-tab-tasks"`,
		`id="modal-tab-agents"`,
		`id="fleet-modal-body"`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("index.html missing expected element %q", expected)
		}
	}

	// 2. Verify app.js contains quota telemetry breakdown & fleet modal functions
	reqJS := httptest.NewRequest("GET", "/ui/app.js", nil)
	wJS := httptest.NewRecorder()
	mux.ServeHTTP(wJS, reqJS)
	if wJS.Code != http.StatusOK {
		t.Fatalf("expected 200 for app.js, got %d", wJS.Code)
	}
	js := wJS.Body.String()
	for _, expected := range []string{
		"openFleetInfoModal",
		"closeFleetInfoModal",
		"switchFleetModalTab",
		"renderFleetModalOrganizations",
		"renderFleetModalTasks",
		"renderFleetModalAgents",
		"settings-provider-card",
		"settings-pool-box",
		"headroom-badge",
		"settings-row-clickable",
	} {
		if !strings.Contains(js, expected) {
			t.Errorf("app.js missing expected symbol/class %q", expected)
		}
	}

	// 3. Verify style.css contains modal and settings quota classes
	reqCSS := httptest.NewRequest("GET", "/ui/style.css", nil)
	wCSS := httptest.NewRecorder()
	mux.ServeHTTP(wCSS, reqCSS)
	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 for style.css, got %d", wCSS.Code)
	}
	css := wCSS.Body.String()
	for _, expected := range []string{
		".modal-overlay",
		".modal-container",
		".modal-tabs",
		".modal-tab-btn",
		".settings-provider-card",
		".settings-pool-box",
		".headroom-badge",
		".settings-row-clickable",
		".settings-val-interactive",
	} {
		if !strings.Contains(css, expected) {
			t.Errorf("style.css missing expected CSS class %q", expected)
		}
	}
}

func TestRegisterUIRoutes_ProjectsViewElements(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-tok")

	// 1. Verify index.html contains projects multi-org controls and status filters
	req := httptest.NewRequest("GET", "/projects", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for /projects, got %d", w.Code)
	}
	body := w.Body.String()
	requiredElements := []string{
		`id="projects-org-multiselect"`,
		`id="projects-org-multiselect-btn"`,
		`id="projects-org-menu"`,
		`id="projects-org-options"`,
		`id="projects-status-filter"`,
		`id="projects-grid"`,
		`class="projects-container"`,
	}
	for _, el := range requiredElements {
		if !strings.Contains(body, el) {
			t.Errorf("/projects response missing expected element: %s", el)
		}
	}

	// 2. Verify app.js contains multi-org and card status filtering logic
	reqJS := httptest.NewRequest("GET", "/ui/app.js", nil)
	wJS := httptest.NewRecorder()
	mux.ServeHTTP(wJS, reqJS)
	if wJS.Code != http.StatusOK {
		t.Fatalf("expected 200 for /ui/app.js, got %d", wJS.Code)
	}
	jsBody := wJS.Body.String()
	requiredJS := []string{
		"populateProjectsOrgFilter",
		"STORAGE_PROJECTS_ORGS_KEY",
		"STORAGE_PROJECTS_STATUS_KEY",
		"STORAGE_PROJECTS_CARD_STATUS_KEY",
		"project-org-group",
		"project-card-filter-pill",
		"project-stat-clickable",
	}
	for _, symbol := range requiredJS {
		if !strings.Contains(jsBody, symbol) {
			t.Errorf("/ui/app.js missing expected symbol: %s", symbol)
		}
	}

	// 3. Verify style.css contains styles for projects grouped headers and multi-select
	reqCSS := httptest.NewRequest("GET", "/ui/style.css", nil)
	wCSS := httptest.NewRecorder()
	mux.ServeHTTP(wCSS, reqCSS)
	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 for /ui/style.css, got %d", wCSS.Code)
	}
	cssBody := wCSS.Body.String()
	requiredCSS := []string{
		".project-org-group",
		".project-org-header",
		".project-cards-subgrid",
		".project-card-filter-pill",
		".multiselect-dropdown",
		".multiselect-menu",
	}
	for _, selector := range requiredCSS {
		if !strings.Contains(cssBody, selector) {
			t.Errorf("/ui/style.css missing expected CSS selector: %s", selector)
		}
	}
}

func TestRegisterUIRoutes_STA194_ClickableKPIsAndBossCarousel(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-tok")

	// 1. Verify index.html contains clickable KPI cards for Running, Active, Blocked, Done, Total, Agents, Spend
	req := httptest.NewRequest("GET", "/overview", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for /overview, got %d", w.Code)
	}
	body := w.Body.String()
	requiredKPIs := []string{
		`id="kpi-card-running"`,
		`id="kpi-card-active"`,
		`id="kpi-card-blocked"`,
		`id="kpi-card-done"`,
		`id="kpi-card-total"`,
		`id="kpi-card-agents"`,
		`id="kpi-card-cost"`,
	}
	for _, kpi := range requiredKPIs {
		if !strings.Contains(body, kpi) {
			t.Errorf("expected overview to contain KPI card %s", kpi)
		}
	}

	// 2. Verify app.js contains drill-down navigation and Boss Card carousel logic
	reqJS := httptest.NewRequest("GET", "/ui/app.js", nil)
	wJS := httptest.NewRecorder()
	mux.ServeHTTP(wJS, reqJS)
	if wJS.Code != http.StatusOK {
		t.Fatalf("expected 200 for /ui/app.js, got %d", wJS.Code)
	}
	jsBody := wJS.Body.String()
	requiredJS := []string{
		"drillDownToTasks",
		"drillDownToAgents",
		"drillDownToCost",
		"buildBossReportCarousel",
		"openBossReportModal",
		"BOSS_REPORTS",
		"boss-carousel-card",
		"boss-preview-iframe",
	}
	for _, symbol := range requiredJS {
		if !strings.Contains(jsBody, symbol) {
			t.Errorf("expected app.js to contain %s", symbol)
		}
	}

	// 3. Verify style.css contains styles for clickable cards and boss report preview carousel
	reqCSS := httptest.NewRequest("GET", "/ui/style.css", nil)
	wCSS := httptest.NewRecorder()
	mux.ServeHTTP(wCSS, reqCSS)
	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 for /ui/style.css, got %d", wCSS.Code)
	}
	cssBody := wCSS.Body.String()
	requiredCSS := []string{
		".kpi-card.clickable",
		".boss-carousel-card",
		".boss-carousel-nav",
		".boss-carousel-tabs",
		".boss-preview-sheet",
		".boss-preview-iframe",
		".boss-modal-backdrop",
	}
	for _, selector := range requiredCSS {
		if !strings.Contains(cssBody, selector) {
			t.Errorf("expected style.css to contain %s", selector)
		}
	}
}

func TestRegisterUIRoutes_TableSortingAndSearchElements(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-tok")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()

	// Check table IDs
	if !strings.Contains(body, `id="overview-task-table"`) {
		t.Error("missing id=\"overview-task-table\" in HTML")
	}
	if !strings.Contains(body, `id="ts-task-table"`) {
		t.Error("missing id=\"ts-task-table\" in HTML")
	}

	// Check sortable columns
	expectedCols := []string{
		`data-col="identifier"`,
		`data-col="task"`,
		`data-col="organization"`,
		`data-col="status"`,
		`data-col="priority"`,
		`data-col="cost"`,
		`data-col="updated"`,
		`data-col="project"`,
		`data-col="assignee"`,
	}
	for _, col := range expectedCols {
		if !strings.Contains(body, col) {
			t.Errorf("missing sort column %s in HTML", col)
		}
	}

	// Check deep search placeholders
	if !strings.Contains(body, `placeholder="Search tasks, descriptions, comments, orgs…"`) {
		t.Error("missing deep search placeholder in search inputs")
	}

	// Check CSS includes sortable and snippet classes
	reqCSS := httptest.NewRequest("GET", "/ui/style.css", nil)
	wCSS := httptest.NewRecorder()
	mux.ServeHTTP(wCSS, reqCSS)
	css := wCSS.Body.String()
	if !strings.Contains(css, ".sortable-th") {
		t.Error("style.css missing .sortable-th")
	}
	if !strings.Contains(css, ".task-search-snippet") {
		t.Error("style.css missing .task-search-snippet")
	}

	// Check app.js includes sorting and deep search functions
	reqJS := httptest.NewRequest("GET", "/ui/app.js", nil)
	wJS := httptest.NewRecorder()
	mux.ServeHTTP(wJS, reqJS)
	js := wJS.Body.String()
	for _, fn := range []string{"getPrioritySeverity", "sortTasks", "renderTableSortHeaders", "getTaskSearchMatch"} {
		if !strings.Contains(js, fn) {
			t.Errorf("app.js missing %s function", fn)
		}
	}
}

func TestRegisterUIRoutes_AgentsCascadingProjectFilter(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "test-tok")

	// 1. Verify index.html contains the necessary filter elements
	reqHTML := httptest.NewRequest("GET", "/", nil)
	wHTML := httptest.NewRecorder()
	mux.ServeHTTP(wHTML, reqHTML)
	if wHTML.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wHTML.Code)
	}
	html := wHTML.Body.String()
	if !strings.Contains(html, `id="agents-org-filter"`) {
		t.Error("index.html missing agents-org-filter")
	}
	if !strings.Contains(html, `id="agents-project-filter"`) {
		t.Error("index.html missing agents-project-filter")
	}

	// 2. Verify app.js contains cascading filter functions and state logic
	reqJS := httptest.NewRequest("GET", "/ui/app.js", nil)
	wJS := httptest.NewRecorder()
	mux.ServeHTTP(wJS, reqJS)
	if wJS.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wJS.Code)
	}
	js := wJS.Body.String()

	requiredFuncs := []string{
		"populateAgentsFilters",
		"populateAgentsOrgFilter",
		"populateAgentsProjectFilter",
		"getProjectsForOrg",
		"getAgentProjects",
		"resetAgentFilters",
		"orgMatches",
	}
	for _, fn := range requiredFuncs {
		if !strings.Contains(js, fn) {
			t.Errorf("app.js missing expected function: %s", fn)
		}
	}

	// Verify project filter reset logic exists on org change and filter reset
	if !strings.Contains(js, "state.agentsFilter.project = 'all'") {
		t.Error("app.js missing state.agentsFilter.project = 'all' reset assignment")
	}
	if !strings.Contains(js, "populateAgentsProjectFilter()") {
		t.Error("app.js missing populateAgentsProjectFilter() calls")
	}
}
