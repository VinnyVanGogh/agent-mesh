package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Event represents a single Server-Sent Event with monotonic ID.
type Event struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

// Subscriber is an active listener on the EventHub with a bounded channel.
type Subscriber struct {
	id           string
	ch           chan Event
	droppedCount atomic.Int64
}

// Channel returns the receive-only channel of events.
func (s *Subscriber) Channel() <-chan Event {
	return s.ch
}

// Dropped returns the count of dropped events due to buffer overflow.
func (s *Subscriber) Dropped() int64 {
	return s.droppedCount.Load()
}

// EventHub manages SSE event broadcasting, replay buffer, and bounded subscriber channels.
type EventHub struct {
	mu           sync.RWMutex
	lastID       atomic.Int64
	subscribers  map[*Subscriber]struct{}
	buffer       []Event
	bufferCap    int
	bufferHead   int
	isFull       bool
	subscriberCap int
}

// NewEventHub creates a new EventHub with given replay buffer capacity and subscriber channel capacity.
func NewEventHub(replayBufferCap, subscriberChannelCap int) *EventHub {
	if replayBufferCap <= 0 {
		replayBufferCap = 1000
	}
	if subscriberChannelCap <= 0 {
		subscriberChannelCap = 128
	}
	return &EventHub{
		subscribers:   make(map[*Subscriber]struct{}),
		buffer:        make([]Event, replayBufferCap),
		bufferCap:     replayBufferCap,
		subscriberCap: subscriberChannelCap,
	}
}

// Publish creates and broadcasts an event with an incrementing monotonic ID.
func (h *EventHub) Publish(eventType string, data any) Event {
	id := h.lastID.Add(1)
	evt := Event{
		ID:        id,
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UTC(),
	}

	h.mu.Lock()
	// 1. Append to circular replay buffer
	h.buffer[h.bufferHead] = evt
	h.bufferHead = (h.bufferHead + 1) % h.bufferCap
	if h.bufferHead == 0 {
		h.isFull = true
	}

	// 2. Broadcast to all subscribers with non-blocking send (bounded buffers)
	for sub := range h.subscribers {
		select {
		case sub.ch <- evt:
		default:
			// Buffer full: slow consumer. Drop event and record count to prevent memory leaks/hangs.
			sub.droppedCount.Add(1)
		}
	}
	h.mu.Unlock()

	return evt
}

// Subscribe registers a new subscriber with a bounded channel.
func (h *EventHub) Subscribe() *Subscriber {
	sub := &Subscriber{
		id: fmt.Sprintf("%d", time.Now().UnixNano()),
		ch: make(chan Event, h.subscriberCap),
	}

	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()

	return sub
}

// Unsubscribe removes an active subscriber and closes its channel.
func (h *EventHub) Unsubscribe(sub *Subscriber) {
	h.mu.Lock()
	if _, ok := h.subscribers[sub]; ok {
		delete(h.subscribers, sub)
		close(sub.ch)
	}
	h.mu.Unlock()
}

// SubscriberCount returns the current number of active subscribers.
func (h *EventHub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// CurrentID returns the latest event ID published.
func (h *EventHub) CurrentID() int64 {
	return h.lastID.Load()
}

// ReplayAfter returns all events in the replay buffer with ID > afterID, ordered by ID ascending.
func (h *EventHub) ReplayAfter(afterID int64) []Event {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []Event
	count := h.bufferHead
	if h.isFull {
		count = h.bufferCap
	}

	startIdx := 0
	if h.isFull {
		startIdx = h.bufferHead
	}

	for i := 0; i < count; i++ {
		idx := (startIdx + i) % h.bufferCap
		evt := h.buffer[idx]
		if evt.ID > afterID {
			result = append(result, evt)
		}
	}

	return result
}

// HandleSSE handles SSE streaming requests with cursor-based replay.
func (h *EventHub) HandleSSE() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming unsupported by server")
			return
		}

		// Cursor check: Last-Event-ID header, or query params ?cursor= or ?last_event_id=
		cursorStr := r.Header.Get("Last-Event-ID")
		if cursorStr == "" {
			cursorStr = r.URL.Query().Get("cursor")
		}
		if cursorStr == "" {
			cursorStr = r.URL.Query().Get("last_event_id")
		}

		var lastID int64
		if cursorStr != "" {
			if id, err := strconv.ParseInt(cursorStr, 10, 64); err == nil {
				lastID = id
			}
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Replay missed events if client reconnected with cursor
		if lastID > 0 {
			missed := h.ReplayAfter(lastID)
			for _, evt := range missed {
				if err := writeSSEEvent(w, evt); err != nil {
					return
				}
				flusher.Flush()
			}
		}

		// Subscribe to live events
		sub := h.Subscribe()
		defer h.Unsubscribe(sub)

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				// SSE keepalive ping
				_, err := fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
				if err != nil {
					return
				}
				flusher.Flush()
			case evt, ok := <-sub.ch:
				if !ok {
					return
				}
				if err := writeSSEEvent(w, evt); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, evt Event) error {
	dataBytes, err := json.Marshal(evt.Data)
	if err != nil {
		dataBytes = []byte(`"{}"`)
	}

	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evt.ID, evt.Type, string(dataBytes))
	return err
}
