package router

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/VinnyVanGogh/staypoint/internal/wire"
)

type QuotaSyncer struct {
	lastHash string
	mu       sync.Mutex
}

// SyncQuotaWire broadcasts the local quota state to the mesh wire if it changed,
// and consumes remote updates from the wire to merge into state.json.
func (s *QuotaSyncer) SyncQuotaWire(db *sql.DB, nodeRole string) error {
	if db == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	stateFile := filepath.Join(home, ".config", "rate-limits", "state.json")

	// 1. Read local state.json
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		// If it doesn't exist, we can't broadcast, but we can consume.
		raw = []byte("{}")
	}

	hash := md5.Sum(raw)
	currentHash := hex.EncodeToString(hash[:])
	stateData := make(map[string]interface{})
	_ = json.Unmarshal(raw, &stateData)

	if stateData["quotas"] == nil {
		stateData["quotas"] = make(map[string]interface{})
	}
	if stateData["lockouts"] == nil {
		stateData["lockouts"] = make(map[string]interface{})
	}

	// 2. Broadcast local state to wire if it changed
	if currentHash != s.lastHash && currentHash != "99914b932bd37a50b983c5e7c90ae93b" { // don't broadcast {}
		broadcastData := map[string]interface{}{
			"quotas":    stateData["quotas"],
			"lockouts":  stateData["lockouts"],
			"node_role": nodeRole,
		}
		bBytes, _ := json.Marshal(broadcastData)

		_, err = wire.Post(db, "quota_sync", "daemon_pacer", "", string(bBytes), 3600)
		if err != nil {
			return fmt.Errorf("failed to broadcast quota: %w", err)
		}
		s.lastHash = currentHash
	}

	// 3. Consume updates from wire
	consumerKey := "quota_sync_consumer_" + nodeRole
	msgs, err := wire.GetUnread(db, consumerKey, "")
	if err != nil {
		return fmt.Errorf("failed to get unread quota syncs: %w", err)
	}

	if len(msgs) == 0 {
		return nil
	}

	changed := false

	for _, msg := range msgs {
		if msg.Channel == "quota_sync" && msg.Author == "daemon_pacer" {
			var remoteState map[string]interface{}
			if err := json.Unmarshal([]byte(msg.Content), &remoteState); err == nil {
				// Ignore self-broadcasts or broadcasts from same role (e.g. 2 instances on same machine)
				if remoteRole, ok := remoteState["node_role"].(string); ok && remoteRole == nodeRole {
					continue
				}

				// Merge quotas
				if remoteQuotas, ok := remoteState["quotas"].(map[string]interface{}); ok {
					localQuotas := stateData["quotas"].(map[string]interface{})
					for k, v := range remoteQuotas {
						localQuotas[k] = v
						changed = true
					}
				}

				// Merge lockouts
				if remoteLockouts, ok := remoteState["lockouts"].(map[string]interface{}); ok {
					localLockouts := stateData["lockouts"].(map[string]interface{})
					for k, v := range remoteLockouts {
						localLockouts[k] = v
						changed = true
					}
				}
			}
		}
	}

	if changed {
		// Write back to state.json
		if finalJSON, err := json.MarshalIndent(stateData, "", "  "); err == nil {
			tmpFile := stateFile + ".tmp"
			if err := os.WriteFile(tmpFile, finalJSON, 0644); err == nil {
				_ = os.Rename(tmpFile, stateFile)
				newHash := md5.Sum(finalJSON)
				s.lastHash = hex.EncodeToString(newHash[:])
			}
		}
	}

	return nil
}
