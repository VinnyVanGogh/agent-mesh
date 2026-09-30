package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// LocalOpenAIAdapter connects to an OpenAI-compatible SSE endpoint (llama.cpp, vLLM, LocalAI)
// over loopback via net/http.
type LocalOpenAIAdapter struct {
	BaseURL string
	Client  *http.Client
}

func (LocalOpenAIAdapter) Provider() string          { return "local" }
func (LocalOpenAIAdapter) BinaryName() string        { return "" }
func (LocalOpenAIAdapter) KnownMajorVersions() []int { return []int{1} }

func (LocalOpenAIAdapter) BuildArgs(opts ParsedOptions) []string {
	return nil
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIChatRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIChatMessage  `json:"messages"`
	Stream        bool                 `json:"stream"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

func (a LocalOpenAIAdapter) endpointURL() string {
	base := a.BaseURL
	if base == "" {
		if u := os.Getenv("LOCAL_LLM_URL"); u != "" {
			base = u
		} else if u := os.Getenv("OPENAI_BASE_URL"); u != "" {
			base = u
		} else if u := os.Getenv("STAYPOINT_LOCAL_URL"); u != "" {
			base = u
		} else {
			base = "http://127.0.0.1:8000"
		}
	}
	base = strings.TrimSuffix(base, "/")
	if strings.HasSuffix(base, "/v1/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

func (a LocalOpenAIAdapter) Execute(ctx context.Context, req ExecRequest) error {
	model := req.Opts.Model
	if model == "" {
		model = "default"
	}

	var messages []openAIChatMessage
	if len(req.Opts.History) > 0 {
		for _, h := range req.Opts.History {
			messages = append(messages, openAIChatMessage{
				Role:    h.Role,
				Content: h.Content,
			})
		}
		if req.Opts.Prompt != "" {
			messages = append(messages, openAIChatMessage{
				Role:    "user",
				Content: req.Opts.Prompt,
			})
		}
	} else {
		messages = []openAIChatMessage{
			{Role: "user", Content: req.Opts.Prompt},
		}
	}

	chatReq := openAIChatRequest{
		Model:         model,
		Messages:      messages,
		Stream:        true,
		StreamOptions: &openAIStreamOptions{IncludeUsage: true},
	}

	bodyBytes, err := json.Marshal(chatReq)
	if err != nil {
		return fmt.Errorf("local openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.endpointURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("local openai: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	client := a.Client
	if client == nil {
		client = &http.Client{}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("local openai: http error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("local openai: server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if _, err := req.Stdout.Write(append(line, '\n')); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(line), []byte("data: [DONE]")) {
			return nil
		}
	}

	return scanner.Err()
}

type openAIChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIChunkChoice struct {
	Index        int              `json:"index"`
	Delta        openAIChunkDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason,omitempty"`
}

type openAIChunkUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type openAIChunk struct {
	ID      string              `json:"id,omitempty"`
	Model   string              `json:"model,omitempty"`
	Choices []openAIChunkChoice `json:"choices,omitempty"`
	Usage   *openAIChunkUsage   `json:"usage,omitempty"`
}

func (LocalOpenAIAdapter) ParseStreamDelta(line []byte) ([]StreamDelta, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	}
	if len(line) == 0 {
		return nil, nil
	}
	if bytes.Equal(line, []byte("[DONE]")) {
		return []StreamDelta{{Kind: DeltaResult, Status: "completed"}}, nil
	}

	var chunk openAIChunk
	if err := json.Unmarshal(line, &chunk); err != nil {
		return nil, fmt.Errorf("local openai stream: %w", err)
	}

	var out []StreamDelta
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			out = append(out, StreamDelta{
				Kind:      DeltaText,
				SessionID: chunk.ID,
				Model:     chunk.Model,
				Text:      choice.Delta.Content,
			})
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			out = append(out, StreamDelta{
				Kind:      DeltaResult,
				SessionID: chunk.ID,
				Model:     chunk.Model,
				Status:    *choice.FinishReason,
			})
		}
	}
	if chunk.Usage != nil {
		out = append(out, StreamDelta{
			Kind:      DeltaUsage,
			SessionID: chunk.ID,
			Model:     chunk.Model,
			Usage: &Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			},
		})
	}

	return out, nil
}

// OllamaAdapter connects to Ollama's /api/chat NDJSON endpoint over loopback via net/http.
type OllamaAdapter struct {
	BaseURL string
	Client  *http.Client
}

func (OllamaAdapter) Provider() string          { return "ollama" }
func (OllamaAdapter) BinaryName() string        { return "" }
func (OllamaAdapter) KnownMajorVersions() []int { return []int{0} }

func (OllamaAdapter) BuildArgs(opts ParsedOptions) []string {
	return nil
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

func (a OllamaAdapter) endpointURL() string {
	base := a.BaseURL
	if base == "" {
		if u := os.Getenv("OLLAMA_HOST"); u != "" {
			base = u
		} else if u := os.Getenv("STAYPOINT_OLLAMA_URL"); u != "" {
			base = u
		} else {
			base = "http://127.0.0.1:11434"
		}
	}
	base = strings.TrimSuffix(base, "/")
	if strings.HasSuffix(base, "/api/chat") {
		return base
	}
	return base + "/api/chat"
}

func (a OllamaAdapter) Execute(ctx context.Context, req ExecRequest) error {
	model := req.Opts.Model
	if model == "" {
		model = "llama3"
	}

	chatReq := ollamaChatRequest{
		Model: model,
		Messages: []ollamaChatMessage{
			{Role: "user", Content: req.Opts.Prompt},
		},
		Stream: true,
	}

	bodyBytes, err := json.Marshal(chatReq)
	if err != nil {
		return fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.endpointURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("ollama: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := a.Client
	if client == nil {
		client = &http.Client{}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama: http error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama: server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if _, err := req.Stdout.Write(append(line, '\n')); err != nil {
			return err
		}
	}

	return scanner.Err()
}

type ollamaChunkMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChunk struct {
	Model           string             `json:"model"`
	CreatedAt       string             `json:"created_at"`
	Message         ollamaChunkMessage `json:"message"`
	Done            bool               `json:"done"`
	PromptEvalCount int64              `json:"prompt_eval_count"`
	EvalCount       int64              `json:"eval_count"`
}

func (OllamaAdapter) ParseStreamDelta(line []byte) ([]StreamDelta, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}

	var chunk ollamaChunk
	if err := json.Unmarshal(line, &chunk); err != nil {
		return nil, fmt.Errorf("ollama stream: %w", err)
	}

	var out []StreamDelta
	if chunk.Message.Content != "" {
		out = append(out, StreamDelta{
			Kind:  DeltaText,
			Model: chunk.Model,
			Text:  chunk.Message.Content,
		})
	}
	if chunk.Done {
		var u *Usage
		if chunk.PromptEvalCount > 0 || chunk.EvalCount > 0 {
			u = &Usage{
				InputTokens:  chunk.PromptEvalCount,
				OutputTokens: chunk.EvalCount,
			}
		}
		out = append(out, StreamDelta{
			Kind:   DeltaResult,
			Model:  chunk.Model,
			Status: "completed",
			Usage:  u,
		})
	}

	return out, nil
}
