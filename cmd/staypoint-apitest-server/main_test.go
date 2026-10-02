package main

import (
	"path/filepath"
	"testing"
)

func TestRefuseRealDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, p := range []string{
		filepath.Join(home, ".staypoint", "staypoint.db"),
		filepath.Join(home, ".agent-mesh", "agent-mesh.db"),
	} {
		if err := refuseRealDB(p); err == nil {
			t.Errorf("refuseRealDB(%q) = nil, want refusal", p)
		}
	}
	if err := refuseRealDB(filepath.Join(t.TempDir(), "staypoint.db")); err != nil {
		t.Errorf("throwaway path refused: %v", err)
	}
}
