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

