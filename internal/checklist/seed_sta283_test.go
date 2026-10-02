package checklist_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/VinnyVanGogh/staypoint/internal/checklist"
)

const sta283Section = "09. Quota Seat Stability & Project Card Click-Through (STA-283)"

// Re-seeding an already-seeded sprint must add newly defined items without
// resetting the Board's verdicts on existing ones.
func TestSeed_AddsNewItemsAndKeepsBoardStatus(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`ALTER TABLE checklist_items ADD COLUMN commit_hash TEXT`); err != nil {
		t.Fatal(err)
	}
	items := DefaultChecklist("STA-236")
	kept := items[0]
	if _, err := db.Exec(`INSERT INTO checklist_items (id, sprint, section, title, status, notes) VALUES (?,?,?,?,?,?)`,
		kept.ID, "STA-236", kept.Section, kept.Title, "fail", "board note"); err != nil {
		t.Fatal(err)
	}

	added, _, err := Seed(ctx, db, "STA-236", false)
	if err != nil {
		t.Fatal(err)
	}
	if added != len(items)-1 {
		t.Errorf("added %d items, want %d", added, len(items)-1)
	}
	var status, notes string
	if err := db.QueryRow(`SELECT status, notes FROM checklist_items WHERE id=?`, kept.ID).Scan(&status, &notes); err != nil {
		t.Fatal(err)
	}
	if status != "fail" || notes != "board note" {
		t.Errorf("existing item reset to %q/%q", status, notes)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM checklist_items WHERE section=? AND commit_hash='ec80ab5'`, sta283Section).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("STA-283 items with delivery commit = %d, want 3", n)
	}

	if again, _, _ := Seed(ctx, db, "STA-236", false); again != 0 {
		t.Errorf("second seed added %d duplicate items", again)
	}
}

// The STA-283 contracts must pass against this tree.
func TestSTA283ContractsPass(t *testing.T) {
	root, _ := filepath.Abs("../..")
	found := 0
	for _, it := range DefaultChecklist("STA-236") {
		if it.Section != sta283Section {
			continue
		}
		found++
		var c Contract
		if err := json.Unmarshal([]byte(it.Contract), &c); err != nil {
			t.Fatalf("%s: bad contract: %v", it.Title, err)
		}
		if c.Type == ContractTypeCommand && strings.Contains(c.Command, "go ") {
			t.Errorf("%s: daemon-evaluated contracts must not shell out to go (TCC, STA-279)", it.Title)
		}
		if res := EvaluateContract(context.Background(), c, root, nil, ""); !res.Passed {
			t.Errorf("%s: contract failed: %s %s", it.Title, res.Reason, res.Details)
		}
	}
	if found != 3 {
		t.Errorf("found %d STA-283 items, want 3", found)
	}
}
