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
		t.Fatalf("expected 200, got %d", wJS.Code)
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
		t.Fatalf("expected 200, got %d", wCSS.Code)
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
