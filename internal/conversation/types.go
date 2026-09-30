package conversation

import (
	"time"
)

// Role represents the sender role in a conversation turn.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall represents a function or tool invocation requested by the assistant.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string representation of args
}

// ToolResult represents the output of a completed tool call.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// Message represents a single turn in the canonical conversation.
type Message struct {
	ID          string                 `json:"id"`
	SessionID   string                 `json:"session_id,omitempty"`
	SequenceNum int                    `json:"sequence_num"`
	Role        Role                   `json:"role"`
	Content     string                 `json:"content"`
	ToolCalls   []ToolCall             `json:"tool_calls,omitempty"`
	ToolResults []ToolResult           `json:"tool_results,omitempty"`
	TokenCount  int                    `json:"token_count,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ProviderHandle stores provider-specific session tracking data (e.g. Anthropic conversation ID, OpenAI thread ID).
type ProviderHandle struct {
	SessionID string                 `json:"session_id,omitempty"`
	Provider  string                 `json:"provider"`
	Handle    string                 `json:"handle"`
	Model     string                 `json:"model,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Conversation represents a full multi-turn conversation across providers.
type Conversation struct {
	ID              string                     `json:"id"`
	Title           string                     `json:"title"`
	RepoPath        string                     `json:"repo_path,omitempty"`
	SystemPrompt    string                     `json:"system_prompt,omitempty"`
	Messages        []Message                  `json:"messages"`
	ProviderHandles map[string]*ProviderHandle `json:"provider_handles,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
	Metadata        map[string]interface{}     `json:"metadata,omitempty"`
}

// SessionSummary represents a lightweight metadata view of a session.
type SessionSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	RepoPath     string    `json:"repo_path,omitempty"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
