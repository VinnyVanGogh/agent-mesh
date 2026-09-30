package conversation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizer_Anthropic_RoundTrip(t *testing.T) {
	rawJSON := `{
  "system": "You are a coding assistant.",
  "messages": [
    {
      "role": "user",
      "content": "What is the weather in Seattle and Portland?"
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "I will check the weather for both cities."
        },
        {
          "type": "tool_use",
          "id": "toolu_01A",
          "name": "get_weather",
          "input": {
            "city": "Seattle"
          }
        },
        {
          "type": "tool_use",
          "id": "toolu_02B",
          "name": "get_weather",
          "input": {
            "city": "Portland"
          }
        }
      ]
    },
    {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "tool_use_id": "toolu_01A",
          "content": "65F and partly cloudy",
          "is_error": false
        },
        {
          "type": "tool_result",
          "tool_use_id": "toolu_02B",
          "content": "68F and sunny",
          "is_error": false
        }
      ]
    },
    {
      "role": "assistant",
      "content": "It is 65F in Seattle and 68F in Portland."
    }
  ]
}`

	var orig AnthropicPayload
	if err := json.Unmarshal([]byte(rawJSON), &orig); err != nil {
		t.Fatalf("failed to unmarshal original Anthropic JSON: %v", err)
	}

	// Step 1: Anthropic -> Canonical
	conv := FromAnthropic(&orig)
	if conv.SystemPrompt != "You are a coding assistant." {
		t.Errorf("expected system prompt to match, got %q", conv.SystemPrompt)
	}
	if len(conv.Messages) != 4 {
		t.Fatalf("expected 4 canonical messages, got %d", len(conv.Messages))
	}

	// Verify assistant message
	if len(conv.Messages[1].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls on assistant message, got %d", len(conv.Messages[1].ToolCalls))
	}
	if conv.Messages[1].ToolCalls[0].ID != "toolu_01A" || conv.Messages[1].ToolCalls[0].Name != "get_weather" {
		t.Errorf("unexpected tool call 0: %+v", conv.Messages[1].ToolCalls[0])
	}

	// Verify user tool results
	if len(conv.Messages[2].ToolResults) != 2 {
		t.Fatalf("expected 2 tool results on user message, got %d", len(conv.Messages[2].ToolResults))
	}
	if conv.Messages[2].ToolResults[0].ToolCallID != "toolu_01A" || conv.Messages[2].ToolResults[0].Content != "65F and partly cloudy" {
		t.Errorf("unexpected tool result 0: %+v", conv.Messages[2].ToolResults[0])
	}

	// Step 2: Canonical -> Anthropic
	rt := ToAnthropic(conv)

	if string(rt.System) != string(orig.System) {
		t.Errorf("system mismatch: expected %q, got %q", orig.System, rt.System)
	}
	if len(rt.Messages) != len(orig.Messages) {
		t.Fatalf("messages length mismatch: expected %d, got %d", len(orig.Messages), len(rt.Messages))
	}

	for i := range orig.Messages {
		if rt.Messages[i].Role != orig.Messages[i].Role {
			t.Errorf("message %d role mismatch: expected %s, got %s", i, orig.Messages[i].Role, rt.Messages[i].Role)
		}
		if len(rt.Messages[i].Content) != len(orig.Messages[i].Content) {
			t.Fatalf("message %d content block count mismatch: expected %d, got %d", i, len(orig.Messages[i].Content), len(rt.Messages[i].Content))
		}
		for j := range orig.Messages[i].Content {
			expB := orig.Messages[i].Content[j]
			gotB := rt.Messages[i].Content[j]
			if expB.Type != gotB.Type {
				t.Errorf("msg %d block %d type mismatch: expected %s, got %s", i, j, expB.Type, gotB.Type)
			}
			if expB.Text != gotB.Text {
				t.Errorf("msg %d block %d text mismatch: expected %s, got %s", i, j, expB.Text, gotB.Text)
			}
			if expB.ID != gotB.ID {
				t.Errorf("msg %d block %d ID mismatch: expected %s, got %s", i, j, expB.ID, gotB.ID)
			}
			if expB.Name != gotB.Name {
				t.Errorf("msg %d block %d Name mismatch: expected %s, got %s", i, j, expB.Name, gotB.Name)
			}
			if expB.ToolUseID != gotB.ToolUseID {
				t.Errorf("msg %d block %d ToolUseID mismatch: expected %s, got %s", i, j, expB.ToolUseID, gotB.ToolUseID)
			}
			if expB.Content != gotB.Content {
				t.Errorf("msg %d block %d Content mismatch: expected %v, got %v", i, j, expB.Content, gotB.Content)
			}
			if expB.IsError != gotB.IsError {
				t.Errorf("msg %d block %d IsError mismatch: expected %v, got %v", i, j, expB.IsError, gotB.IsError)
			}
		}
	}
}

func TestNormalizer_Gemini_RoundTrip(t *testing.T) {
	rawJSON := `{
  "systemInstruction": {
    "parts": [
      {
        "text": "You are a helpful assistant."
      }
    ]
  },
  "contents": [
    {
      "role": "user",
      "parts": [
        {
          "text": "What is the weather?"
        }
      ]
    },
    {
      "role": "model",
      "parts": [
        {
          "text": "Checking weather."
        },
        {
          "functionCall": {
            "name": "lookup_weather",
            "args": {
              "location": "Boston"
            }
          }
        }
      ]
    },
    {
      "role": "user",
      "parts": [
        {
          "functionResponse": {
            "name": "lookup_weather",
            "response": {
              "result": "55F and raining"
            }
          }
        }
      ]
    },
    {
      "role": "model",
      "parts": [
        {
          "text": "The weather in Boston is 55F and raining."
        }
      ]
    }
  ]
}`

	var orig GeminiPayload
	if err := json.Unmarshal([]byte(rawJSON), &orig); err != nil {
		t.Fatalf("failed to unmarshal original Gemini JSON: %v", err)
	}

	// Gemini -> Canonical
	conv := FromGemini(&orig)
	if conv.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("expected system prompt to match, got %q", conv.SystemPrompt)
	}
	if len(conv.Messages) != 4 {
		t.Fatalf("expected 4 canonical messages, got %d", len(conv.Messages))
	}

	// Canonical -> Gemini
	rt := ToGemini(conv)

	if rt.SystemInstruction == nil || len(rt.SystemInstruction.Parts) == 0 || rt.SystemInstruction.Parts[0].Text != orig.SystemInstruction.Parts[0].Text {
		t.Errorf("system instruction mismatch")
	}

	if len(rt.Contents) != len(orig.Contents) {
		t.Fatalf("contents length mismatch: expected %d, got %d", len(orig.Contents), len(rt.Contents))
	}

	for i := range orig.Contents {
		if rt.Contents[i].Role != orig.Contents[i].Role {
			t.Errorf("turn %d role mismatch: expected %s, got %s", i, orig.Contents[i].Role, rt.Contents[i].Role)
		}
		if len(rt.Contents[i].Parts) != len(orig.Contents[i].Parts) {
			t.Fatalf("turn %d parts count mismatch: expected %d, got %d", i, len(orig.Contents[i].Parts), len(rt.Contents[i].Parts))
		}
		for j := range orig.Contents[i].Parts {
			expP := orig.Contents[i].Parts[j]
			gotP := rt.Contents[i].Parts[j]
			if expP.Text != gotP.Text {
				t.Errorf("turn %d part %d text mismatch: expected %s, got %s", i, j, expP.Text, gotP.Text)
			}
			if (expP.FunctionCall == nil) != (gotP.FunctionCall == nil) {
				t.Fatalf("turn %d part %d functionCall presence mismatch", i, j)
			}
			if expP.FunctionCall != nil {
				if expP.FunctionCall.Name != gotP.FunctionCall.Name {
					t.Errorf("functionCall name mismatch: %s vs %s", expP.FunctionCall.Name, gotP.FunctionCall.Name)
				}
				if !reflect.DeepEqual(expP.FunctionCall.Args, gotP.FunctionCall.Args) {
					t.Errorf("functionCall args mismatch: %+v vs %+v", expP.FunctionCall.Args, gotP.FunctionCall.Args)
				}
			}
			if (expP.FunctionResponse == nil) != (gotP.FunctionResponse == nil) {
				t.Fatalf("turn %d part %d functionResponse presence mismatch", i, j)
			}
			if expP.FunctionResponse != nil {
				if expP.FunctionResponse.Name != gotP.FunctionResponse.Name {
					t.Errorf("functionResponse name mismatch: %s vs %s", expP.FunctionResponse.Name, gotP.FunctionResponse.Name)
				}
				if !reflect.DeepEqual(expP.FunctionResponse.Response, gotP.FunctionResponse.Response) {
					t.Errorf("functionResponse response mismatch: %+v vs %+v", expP.FunctionResponse.Response, gotP.FunctionResponse.Response)
				}
			}
		}
	}
}

func TestNormalizer_OpenAI_RoundTrip(t *testing.T) {
	rawJSON := `{
  "messages": [
    {
      "role": "system",
      "content": "You are a helpful coding assistant."
    },
    {
      "role": "user",
      "content": "Calculate 42 * 10"
    },
    {
      "role": "assistant",
      "content": "Let me calculate that.",
      "tool_calls": [
        {
          "id": "call_calc_1",
          "type": "function",
          "function": {
            "name": "calc",
            "arguments": "{\"expr\":\"42 * 10\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_calc_1",
      "content": "420"
    },
    {
      "role": "assistant",
      "content": "42 * 10 is 420."
    }
  ]
}`

	var orig OpenAIPayload
	if err := json.Unmarshal([]byte(rawJSON), &orig); err != nil {
		t.Fatalf("failed to unmarshal original OpenAI JSON: %v", err)
	}

	// OpenAI -> Canonical
	conv := FromOpenAI(&orig)
	if conv.SystemPrompt != "You are a helpful coding assistant." {
		t.Errorf("expected system prompt to match, got %q", conv.SystemPrompt)
	}
	if len(conv.Messages) != 4 {
		t.Fatalf("expected 4 canonical messages (system prompt set separately), got %d", len(conv.Messages))
	}

	// Canonical -> OpenAI
	rt := ToOpenAI(conv)

	if len(rt.Messages) != len(orig.Messages) {
		t.Fatalf("messages count mismatch: expected %d, got %d", len(orig.Messages), len(rt.Messages))
	}

	for i := range orig.Messages {
		expM := orig.Messages[i]
		gotM := rt.Messages[i]

		if expM.Role != gotM.Role {
			t.Errorf("msg %d role mismatch: expected %s, got %s", i, expM.Role, gotM.Role)
		}
		if expM.Content != gotM.Content {
			t.Errorf("msg %d content mismatch: expected %q, got %q", i, expM.Content, gotM.Content)
		}
		if expM.ToolCallID != gotM.ToolCallID {
			t.Errorf("msg %d ToolCallID mismatch: expected %q, got %q", i, expM.ToolCallID, gotM.ToolCallID)
		}
		if len(expM.ToolCalls) != len(gotM.ToolCalls) {
			t.Fatalf("msg %d tool_calls length mismatch: expected %d, got %d", i, len(expM.ToolCalls), len(gotM.ToolCalls))
		}
		for j := range expM.ToolCalls {
			if expM.ToolCalls[j].ID != gotM.ToolCalls[j].ID {
				t.Errorf("tool_call %d ID mismatch", j)
			}
			if expM.ToolCalls[j].Function.Name != gotM.ToolCalls[j].Function.Name {
				t.Errorf("tool_call %d function name mismatch", j)
			}
			if expM.ToolCalls[j].Function.Arguments != gotM.ToolCalls[j].Function.Arguments {
				t.Errorf("tool_call %d function args mismatch", j)
			}
		}
	}
}

func TestNormalizer_CrossProvider_Transformations(t *testing.T) {
	// Anthropic -> Canonical -> OpenAI -> Canonical -> Gemini -> Canonical -> Anthropic
	anthropicInput := &AnthropicPayload{
		System: "System prompt",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContentBlock{{Type: "text", Text: "Hello"}}},
			{Role: "assistant", Content: []AnthropicContentBlock{
				{Type: "text", Text: "Calling tool"},
				{Type: "tool_use", ID: "call_abc", Name: "search", Input: map[string]interface{}{"query": "go"}},
			}},
			{Role: "user", Content: []AnthropicContentBlock{
				{Type: "tool_result", ToolUseID: "call_abc", Content: "golang.org"},
			}},
			{Role: "assistant", Content: []AnthropicContentBlock{{Type: "text", Text: "Done"}}},
		},
	}

	conv1 := FromAnthropic(anthropicInput)
	openAI := ToOpenAI(conv1)

	// Verify OpenAI shape has tool role
	hasToolMsg := false
	for _, m := range openAI.Messages {
		if m.Role == "tool" && m.ToolCallID == "call_abc" && m.Content == "golang.org" {
			hasToolMsg = true
		}
	}
	if !hasToolMsg {
		t.Errorf("expected OpenAI payload to have role=tool message with call_abc")
	}

	conv2 := FromOpenAI(openAI)
	gemini := ToGemini(conv2)

	// Verify Gemini shape has functionResponse
	hasFuncResp := false
	for _, c := range gemini.Contents {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "search" {
				hasFuncResp = true
			}
		}
	}
	if !hasFuncResp {
		t.Errorf("expected Gemini payload to have functionResponse for search")
	}

	conv3 := FromGemini(gemini)
	anthropicOutput := ToAnthropic(conv3)

	if len(anthropicOutput.Messages) != len(anthropicInput.Messages) {
		t.Fatalf("cross-provider round trip message count mismatch: %d vs %d", len(anthropicOutput.Messages), len(anthropicInput.Messages))
	}
}

func TestNormalizer_FlattenForNonToolModels(t *testing.T) {
	conv := &Conversation{
		SystemPrompt: "You are an assistant.",
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Run test suite",
			},
			{
				Role:    RoleAssistant,
				Content: "Running tests now.",
				ToolCalls: []ToolCall{
					{
						ID:        "t1",
						Name:      "bash",
						Arguments: `{"cmd":"go test ./..."}`,
					},
				},
			},
			{
				Role: RoleTool,
				ToolResults: []ToolResult{
					{
						ToolCallID: "t1",
						Name:       "bash",
						Content:    "PASS\nok",
					},
				},
			},
			{
				Role:    RoleAssistant,
				Content: "All tests passed successfully!",
			},
		},
	}

	flat := FlattenForNonToolModels(conv)

	for _, m := range flat.Messages {
		if m.Role != RoleSystem && m.Role != RoleUser && m.Role != RoleAssistant {
			t.Errorf("expected flattened roles to be system, user, or assistant, got %s", m.Role)
		}
		if len(m.ToolCalls) != 0 {
			t.Errorf("expected 0 tool calls in flattened message, got %d", len(m.ToolCalls))
		}
		if len(m.ToolResults) != 0 {
			t.Errorf("expected 0 tool results in flattened message, got %d", len(m.ToolResults))
		}
	}

	// Verify tool call text is embedded in assistant message
	if flat.Messages[1].Role != RoleAssistant || len(flat.Messages[1].ToolCalls) != 0 {
		t.Errorf("unexpected flattened assistant message")
	}
	if !strings.HasPrefix(flat.Messages[1].Content, "Running tests now.") {
		t.Errorf("unexpected content: %s", flat.Messages[1].Content)
	}

	// Verify tool result is embedded in user message
	if flat.Messages[2].Role != RoleUser {
		t.Errorf("expected tool result to flatten to RoleUser, got %s", flat.Messages[2].Role)
	}
	if !strings.HasPrefix(flat.Messages[2].Content, "[Tool Result: bash") {
		t.Errorf("unexpected flattened tool result content: %s", flat.Messages[2].Content)
	}
}
