package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BridgeSession holds metadata describing the active reverse bridge between client and remote node.
type BridgeSession struct {
	ClientUser string `json:"client_user"`
	ClientHome string `json:"client_home"`
	ClientHost string `json:"client_host"`
	BridgePort int    `json:"bridge_port"`
	Token      string `json:"token"`
	Active     bool   `json:"active"`
	UpdatedAt  string `json:"updated_at"`
}

var bridgeSessionPathOverride string

// DefaultSessionPath returns the path to ~/.agent-mesh/bridge-session.json.
func DefaultSessionPath() string {
	if bridgeSessionPathOverride != "" {
		return bridgeSessionPathOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agent-mesh", "bridge-session.json")
}

// SaveBridgeSession saves session metadata locally.
func SaveBridgeSession(session BridgeSession) error {
	p := DefaultSessionPath()
	if p == "" {
		return fmt.Errorf("unable to determine home directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// LoadBridgeSession reads the active session metadata if present.
func LoadBridgeSession() (*BridgeSession, error) {
	p := DefaultSessionPath()
	if p == "" {
		return nil, fmt.Errorf("unable to determine home directory")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var session BridgeSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// ClearBridgeSession marks the session inactive.
func ClearBridgeSession() error {
	p := DefaultSessionPath()
	if p == "" {
		return nil
	}
	_ = os.Remove(p)
	return nil
}
