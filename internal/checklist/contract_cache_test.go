package checklist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContractCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contract-eval-cache.json")

	cache := &ContractEvalCache{
		Commit:   "abc1234",
		RepoRoot: "/tmp/repo",
		BuiltAt:  "2026-10-01T00:00:00Z",
		Entries: []ContractCacheEntry{
			{ItemID: "item-1", ContractHash: contractHash(`{"type":"file_pattern"}`), Passed: true, EvaluatedMs: 5},
			{ItemID: "item-2", ContractHash: contractHash(`{"type":"command","command":"true"}`), Passed: false, Reason: "exit 1", EvaluatedMs: 12},
		},
	}

	if err := WriteContractCache(path, cache); err != nil {
		t.Fatalf("WriteContractCache: %v", err)
	}

	loaded := LoadContractCache(path)
	if loaded == nil {
		t.Fatal("LoadContractCache returned nil")
	}
	if loaded.Commit != cache.Commit {
		t.Errorf("commit mismatch: got %q want %q", loaded.Commit, cache.Commit)
	}
	if len(loaded.Entries) != len(cache.Entries) {
		t.Fatalf("entry count mismatch: got %d want %d", len(loaded.Entries), len(cache.Entries))
	}
}

func TestContractCacheLoadMissing(t *testing.T) {
	got := LoadContractCache("/nonexistent/path.json")
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestContractCacheLookupEntry(t *testing.T) {
	contractJSON := `{"type":"file_pattern","file_path":"go.mod"}`
	cache := &ContractEvalCache{
		Entries: []ContractCacheEntry{
			{ItemID: "id-a", ContractHash: contractHash(contractJSON), Passed: true},
			{ItemID: "id-b", ContractHash: "differenthash", Passed: false},
		},
	}

	// Exact hit
	entry := cache.LookupEntry("id-a", contractJSON)
	if entry == nil {
		t.Fatal("expected cache hit, got nil")
	}
	if !entry.Passed {
		t.Error("expected passed=true")
	}

	// Item ID mismatch
	if e := cache.LookupEntry("id-c", contractJSON); e != nil {
		t.Errorf("expected nil for unknown item_id, got %+v", e)
	}

	// Contract JSON changed (hash mismatch)
	if e := cache.LookupEntry("id-a", `{"type":"file_pattern","file_path":"go.sum"}`); e != nil {
		t.Errorf("expected nil when contract JSON changed, got %+v", e)
	}

	// Nil cache
	var nilCache *ContractEvalCache
	if e := nilCache.LookupEntry("id-a", contractJSON); e != nil {
		t.Errorf("expected nil from nil cache, got %+v", e)
	}
}

func TestContractCacheEnvOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "custom-cache.json")
	t.Setenv("STAYPOINT_CONTRACT_CACHE", p)
	got := ContractCachePath()
	if got != p {
		t.Errorf("env override: got %q want %q", got, p)
	}
}

func TestContractCacheWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	cache := &ContractEvalCache{Commit: "x", Entries: nil}
	if err := WriteContractCache(path, cache); err != nil {
		t.Fatal(err)
	}
	// .tmp file should not persist after atomic rename
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after successful write")
	}
}

func TestContractCacheHashChangesOnContentChange(t *testing.T) {
	c1 := `{"type":"file_pattern","file_path":"a.go"}`
	c2 := `{"type":"file_pattern","file_path":"b.go"}`
	if contractHash(c1) == contractHash(c2) {
		t.Error("different contracts should have different hashes")
	}
	if contractHash(c1) != contractHash(c1) {
		t.Error("same contract should have same hash")
	}
}
