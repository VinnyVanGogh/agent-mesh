package checklist_test

import (
	"regexp"
	"testing"

	. "github.com/VinnyVanGogh/staypoint/internal/checklist"
)

// The T5 loop is read by the Board, not by engineers: its visible text must
// not carry commit codes or task IDs. Those live in CommitHash and are shown
// only behind the checklist page's "details" toggle.
func TestSTA316Seed_PlainLanguage(t *testing.T) {
	items := DefaultChecklist("STA-316")
	wantTitles := []string{
		"1. Create a task",
		"2. Watch it wake up",
		"3. Watch the steps",
		"4. Answer the question card",
		"5. Mark it done",
	}
	if len(items) != len(wantTitles) {
		t.Fatalf("got %d items, want %d", len(items), len(wantTitles))
	}
	taskID := regexp.MustCompile(`\b(?:(?:STA|MAN|PER|RUN|RES)-\d+|task-[0-9a-f]{6,})\b`)
	hex := regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	isSHA := func(s string) bool {
		return regexp.MustCompile(`\d`).MatchString(s) && regexp.MustCompile(`[a-f]`).MatchString(s)
	}
	for i, it := range items {
		if it.Title != wantTitles[i] {
			t.Errorf("item %d title = %q, want %q", i, it.Title, wantTitles[i])
		}
		if it.Sprint != "STA-316" || it.CommitHash == "" {
			t.Errorf("item %d: sprint %q commit %q", i, it.Sprint, it.CommitHash)
		}
		for _, text := range []string{it.Section, it.Title, it.Description, it.HowToTest} {
			if m := taskID.FindString(text); m != "" {
				t.Errorf("item %d text %q contains task ID %q", i, text, m)
			}
			for _, m := range hex.FindAllString(text, -1) {
				if isSHA(m) {
					t.Errorf("item %d text %q contains commit %q", i, text, m)
				}
			}
		}
	}
}
