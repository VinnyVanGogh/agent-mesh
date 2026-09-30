package chat

import (
	"strings"
	"sync"
)

// TokenAccumulator accumulates streaming tokens in a thread-safe buffer
// and flushes them in frames (e.g. at ~30fps) to eliminate TUI redraw flicker.
type TokenAccumulator struct {
	mu      sync.Mutex
	pending strings.Builder
	total   strings.Builder
}

// NewTokenAccumulator creates a new empty TokenAccumulator.
func NewTokenAccumulator() *TokenAccumulator {
	return &TokenAccumulator{}
}

// Append appends a token or chunk of text to the pending and total buffers.
func (a *TokenAccumulator) Append(chunk string) {
	if chunk == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending.WriteString(chunk)
	a.total.WriteString(chunk)
}

// Flush drains and returns all tokens accumulated since the last Flush call.
// Returns an empty string if no new tokens have arrived.
func (a *TokenAccumulator) Flush() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending.Len() == 0 {
		return ""
	}
	chunk := a.pending.String()
	a.pending.Reset()
	return chunk
}

// Pending returns the currently un-flushed content without draining it.
func (a *TokenAccumulator) Pending() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending.String()
}

// Total returns all text accumulated since creation or the last Reset.
func (a *TokenAccumulator) Total() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.total.String()
}

// Len returns the byte length of pending un-flushed tokens.
func (a *TokenAccumulator) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending.Len()
}

// TotalLen returns the total byte length of all text accumulated.
func (a *TokenAccumulator) TotalLen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.total.Len()
}

// Reset clears both pending and total accumulated text.
func (a *TokenAccumulator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending.Reset()
	a.total.Reset()
}
