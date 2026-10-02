package server

import (
	"strings"
	"testing"
)

// jsFunc returns the source of a top-level `function name(` in the embedded
// app.js, up to the next top-level function. Good enough to pin down wiring
// that has no browser test harness.
func jsFunc(t *testing.T, name string) string {
	t.Helper()
	b, err := webuiFiles.ReadFile("webui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "\nfunction "+name+"(")
	if start < 0 {
		t.Fatalf("function %s not found in app.js", name)
	}
	rest := src[start+1:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		rest = rest[:end+2]
	}
	return rest
}

// STA-283: project cards are styled clickable but had no handler. The card
// itself must open the project (Task Status filtered to its org + project).
func TestProjectCardOpensProject(t *testing.T) {
	body := jsFunc(t, "createProjectCard")
	for _, want := range []string{
		"card.addEventListener('click'",
		"drillDownToTasks('all', p.org",
		"card.addEventListener('keydown'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("createProjectCard missing %q", want)
		}
	}
	drill := jsFunc(t, "drillDownToTasks")
	if !strings.Contains(drill, "state.tsFilter.project = project") {
		t.Error("drillDownToTasks must apply the project argument to the Task Status filter")
	}
}

// STA-283: org detail hid a Claude seat whenever it read 0%/0%, so a freshly
// reset seat vanished and the view looked like it flipped between seats.
func TestOrgDetailRendersBothClaudeSeats(t *testing.T) {
	body := jsFunc(t, "renderOrgDetailView")
	if strings.Contains(body, "five_hour_used_pct === 0 && q.weekly_used_pct === 0) continue") {
		t.Error("renderOrgDetailView must not hide a Claude seat based on its usage values")
	}
	// STA-210 dropped this declaration; the view threw before drawing any seat.
	if strings.Contains(body, "quotaGrid.appendChild") && !strings.Contains(body, "const quotaGrid") {
		t.Error("renderOrgDetailView uses quotaGrid without declaring it")
	}
}
