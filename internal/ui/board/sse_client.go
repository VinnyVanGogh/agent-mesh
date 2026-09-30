package board

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SSEClientInterface abstracts SSE streaming from the daemon.
type SSEClientInterface interface {
	Start(ctx context.Context, events chan<- DaemonEvent, status chan<- daemonStatusMsg)
	Close() error
	IsConnected() bool
}

// SSEClient connects to staypointd SSE endpoint and streams live events.
type SSEClient struct {
	daemonURL  string
	token      string
	httpClient *http.Client
	lastID     int64
	connected  atomic.Bool
	cancel     context.CancelFunc
	mu         sync.Mutex
}

// NewSSEClient creates a new SSE client for the given daemon URL and auth token.
func NewSSEClient(daemonURL, token string, client *http.Client) *SSEClient {
	if client == nil {
		client = &http.Client{
			Timeout: 0, // Infinite for SSE streaming
		}
	}
	daemonURL = strings.TrimRight(daemonURL, "/")
	if daemonURL == "" {
		daemonURL = "http://127.0.0.1:41421"
	}
	return &SSEClient{
		daemonURL:  daemonURL,
		token:      token,
		httpClient: client,
	}
}

// IsConnected returns whether the SSE stream is currently connected.
func (c *SSEClient) IsConnected() bool {
	return c.connected.Load()
}

// Close disconnects the SSE stream.
func (c *SSEClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.connected.Store(false)
	return nil
}

// Start begins streaming SSE events in a background goroutine.
func (c *SSEClient) Start(ctx context.Context, events chan<- DaemonEvent, status chan<- daemonStatusMsg) {
	c.mu.Lock()
	ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	go c.runLoop(ctx, events, status)
}

func (c *SSEClient) runLoop(ctx context.Context, events chan<- DaemonEvent, status chan<- daemonStatusMsg) {
	backoff := 1 * time.Second

	for {
		select {
		case <-ctx.Done():
			c.connected.Store(false)
			return
		default:
		}

		err := c.connectAndRead(ctx, events, status)
		c.connected.Store(false)

		select {
		case status <- daemonStatusMsg{connected: false, err: err}:
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < 8*time.Second {
				backoff *= 2
			}
		}
	}
}

func (c *SSEClient) connectAndRead(ctx context.Context, events chan<- DaemonEvent, status chan<- daemonStatusMsg) error {
	endpoint := fmt.Sprintf("%s/api/events", c.daemonURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("X-StayPoint-Token", c.token)
	}

	if lastID := atomic.LoadInt64(&c.lastID); lastID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(lastID, 10))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	c.connected.Store(true)
	select {
	case status <- daemonStatusMsg{connected: true, err: nil}:
	default:
	}

	reader := bufio.NewReader(resp.Body)
	var currentEvent DaemonEvent
	var hasData bool

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}

		line = strings.TrimRight(line, "\r\n")

		// SSE comment / ping (e.g. ": keepalive ...")
		if strings.HasPrefix(line, ":") {
			continue
		}

		// Empty line marks end of event block
		if line == "" {
			if hasData || currentEvent.Type != "" {
				if currentEvent.ID > 0 {
					atomic.StoreInt64(&c.lastID, currentEvent.ID)
				}
				select {
				case events <- currentEvent:
				case <-ctx.Done():
					return ctx.Err()
				}
				currentEvent = DaemonEvent{}
				hasData = false
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		field := parts[0]
		value := ""
		if len(parts) > 1 {
			value = strings.TrimPrefix(parts[1], " ")
		}

		switch field {
		case "id":
			if id, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
				currentEvent.ID = id
			}
		case "event":
			currentEvent.Type = value
		case "data":
			hasData = true
			currentEvent.RawData = value
			var dataMap map[string]any
			if unmarshalErr := json.Unmarshal([]byte(value), &dataMap); unmarshalErr == nil {
				currentEvent.Data = dataMap
			} else {
				currentEvent.Data = map[string]any{"raw": value}
			}
		}
	}
}
