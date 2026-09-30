package orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/ipc"
)

// NotifyDaemon sends a wake event to the running daemon via its IPC socket using MCP.
func NotifyDaemon(taskID, reason, idempotencyKey string) error {
	socketPath := ipc.SocketPath()
	conn, err := ipc.Dial(socketPath)
	if err != nil {
		return fmt.Errorf("could not connect to daemon: %w", err)
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))

	// Send JSON-RPC call to 'staypoint_wake' tool
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "staypoint_wake",
			"arguments": map[string]any{
				"task_id":         taskID,
				"reason":          reason,
				"idempotency_key": idempotencyKey,
			},
		},
	}

	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("failed to send wake request: %w", err)
	}

	return nil
}
