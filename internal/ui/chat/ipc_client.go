package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/ipc"
)

var ErrDaemonUnreachable = errors.New("staypointd is not running or unreachable")

// DaemonClientInterface abstracts IPC communication with staypointd.
type DaemonClientInterface interface {
	IsConnected() bool
	Ping(ctx context.Context) error
	Checkpoint(ctx context.Context, message string) (string, error)
	Undo(ctx context.Context, checkpointID string) (string, error)
	Close() error
}

// IPCClient manages a live IPC socket connection to staypointd.
type IPCClient struct {
	mu         sync.Mutex
	socketPath string
	conn       net.Conn
	reader     *bufio.Reader
	connected  bool
	reqID      atomic.Uint64
}

// ConnectDaemon attempts to dial staypointd over the local IPC socket.
// If staypointd is not running, returns ErrDaemonUnreachable.
func ConnectDaemon(socketPath string) (*IPCClient, error) {
	if socketPath == "" {
		socketPath = ipc.SocketPath()
	}

	conn, err := ipc.Dial(socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}

	client := &IPCClient{
		socketPath: socketPath,
		conn:       conn,
		reader:     bufio.NewReader(conn),
		connected:  true,
	}

	// Verify connection with a fast ping (500ms timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%w: ping failed: %v", ErrDaemonUnreachable, err)
	}

	return client, nil
}

func (c *IPCClient) IsConnected() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *IPCClient) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *IPCClient) Ping(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return ErrDaemonUnreachable
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	}

	id := c.reqID.Add(1)
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": "ping"}
	if err := json.NewEncoder(c.conn).Encode(req); err != nil {
		c.connected = false
		return err
	}

	line, err := c.reader.ReadString('\n')
	if err != nil {
		c.connected = false
		return err
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return fmt.Errorf("invalid ping response: %w", err)
	}
	if resp["error"] != nil {
		return fmt.Errorf("ping error: %v", resp["error"])
	}
	return nil
}

func (c *IPCClient) callTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return "", ErrDaemonUnreachable
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	}

	id := c.reqID.Add(1)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}

	if err := json.NewEncoder(c.conn).Encode(req); err != nil {
		c.connected = false
		return "", err
	}

	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			c.connected = false
			return "", err
		}
		if line == "" || line == "\n" {
			continue
		}

		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		respID, ok := resp["id"].(float64)
		if !ok || uint64(respID) != id {
			// Skip notifications or responses to other IDs
			continue
		}

		if resp["error"] != nil {
			return "", fmt.Errorf("daemon error: %v", resp["error"])
		}

		result, _ := resp["result"].(map[string]any)
		if isErr, _ := result["isError"].(bool); isErr {
			content, _ := result["content"].([]any)
			if len(content) > 0 {
				item, _ := content[0].(map[string]any)
				return "", errors.New(item["text"].(string))
			}
			return "", errors.New("tool execution failed")
		}

		content, _ := result["content"].([]any)
		if len(content) > 0 {
			item, _ := content[0].(map[string]any)
			if text, ok := item["text"].(string); ok {
				return text, nil
			}
		}
		return "", nil
	}
}

func (c *IPCClient) Checkpoint(ctx context.Context, message string) (string, error) {
	return c.callTool(ctx, "staypoint_checkpoint", map[string]any{
		"message": message,
	})
}

func (c *IPCClient) Undo(ctx context.Context, checkpointID string) (string, error) {
	args := map[string]any{}
	if checkpointID != "" {
		args["checkpoint_id"] = checkpointID
	}
	return c.callTool(ctx, "staypoint_undo", args)
}
