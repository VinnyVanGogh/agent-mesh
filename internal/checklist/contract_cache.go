package checklist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ContractCacheEntry holds the pre-evaluated result of one checklist item's contract.
type ContractCacheEntry struct {
	ItemID       string `json:"item_id"`
	ContractHash string `json:"contract_hash"` // first 8 bytes of SHA-256 of contract JSON
	Passed       bool   `json:"passed"`
	Reason       string `json:"reason,omitempty"`
	Details      string `json:"details,omitempty"`
	EvaluatedMs  int64  `json:"evaluated_ms"`
}

// ContractEvalCache holds the pre-evaluated contract results for one build.
//
// reinstall-daemon.sh writes this file (in the user's shell, which has
// ~/Documents TCC access). The daemon reads it instead of touching the repo.
// Under launchd, macOS blocks reads of ~/Documents until staypointd is granted
// Documents access, and os.ReadFile/exec.Command hang rather than failing; each
// blocked read burns ~1.2 s of the curl timeout that verify-checklist.sh uses.
type ContractEvalCache struct {
	Commit   string               `json:"commit"`
	RepoRoot string               `json:"repo_root"`
	BuiltAt  string               `json:"built_at"`
	Entries  []ContractCacheEntry `json:"entries"`
}

// ContractCachePath returns the canonical path for the contract eval cache.
func ContractCachePath() string {
	if p := os.Getenv("STAYPOINT_CONTRACT_CACHE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".staypoint", "contract-eval-cache.json")
}

// contractHash returns a short hash of a contract JSON string for cache keying.
func contractHash(contractJSON string) string {
	h := sha256.Sum256([]byte(contractJSON))
	return fmt.Sprintf("%x", h[:8])
}

// LoadContractCache reads the cache file and returns it, or nil if absent/unreadable.
func LoadContractCache(path string) *ContractEvalCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c ContractEvalCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

// LookupEntry returns the cache entry for a given item and contract JSON, or nil.
// A hit requires both item_id and contract_hash to match (detects contract edits).
func (c *ContractEvalCache) LookupEntry(itemID, contractJSON string) *ContractCacheEntry {
	if c == nil {
		return nil
	}
	h := contractHash(contractJSON)
	for i := range c.Entries {
		if c.Entries[i].ItemID == itemID && c.Entries[i].ContractHash == h {
			return &c.Entries[i]
		}
	}
	return nil
}

// WriteContractCache atomically writes the cache to path.
func WriteContractCache(path string, cache *ContractEvalCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EvalContractsForSprint evaluates all contracts for a sprint directly (no DB
// update, no divergence logic) and returns a ContractEvalCache ready for writing.
//
// Intended for use by the eval-contracts CLI subcommand, which runs in the
// user's shell with ~/Documents TCC access.
func EvalContractsForSprint(ctx context.Context, dbConn *sql.DB, sprint, repoRoot, commit string) (*ContractEvalCache, error) {
	if repoRoot == "" {
		repoRoot = "."
	}
	query := `SELECT id, contract FROM checklist_items
		WHERE sprint = ? AND contract IS NOT NULL AND contract != ''`
	rows, err := dbConn.QueryContext(ctx, query, sprint)
	if err != nil {
		return nil, fmt.Errorf("query contracts: %w", err)
	}
	defer rows.Close()

	httpClient := &http.Client{Timeout: 10 * time.Second}

	cache := &ContractEvalCache{
		Commit:   commit,
		RepoRoot: repoRoot,
		BuiltAt:  time.Now().UTC().Format(time.RFC3339),
	}

	for rows.Next() {
		var itemID, contractJSON string
		if err := rows.Scan(&itemID, &contractJSON); err != nil {
			continue
		}
		contract, err := ParseContract(contractJSON)
		if err != nil || contract == nil {
			continue
		}
		start := time.Now()
		result := EvaluateContract(ctx, *contract, repoRoot, httpClient, "")
		ms := time.Since(start).Milliseconds()

		cache.Entries = append(cache.Entries, ContractCacheEntry{
			ItemID:       itemID,
			ContractHash: contractHash(contractJSON),
			Passed:       result.Passed,
			Reason:       result.Reason,
			Details:      result.Details,
			EvaluatedMs:  ms,
		})
		fmt.Printf("  [%4dms] %s  %s\n", ms, passStr(result.Passed), itemID)
	}
	return cache, nil
}

func passStr(ok bool) string {
	if ok {
		return "pass"
	}
	return "FAIL"
}
