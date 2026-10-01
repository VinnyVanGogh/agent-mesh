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
