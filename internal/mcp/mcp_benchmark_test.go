package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// BenchmarkMCPInitialize benchmarks the MCP JSON-RPC initialize handshake latency.
func BenchmarkMCPInitialize(b *testing.B) {
	s := NewServer()
	defer s.Close()

	reqBytes, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      makeRawID(1),
		Method:  "initialize",
		Params:  json.RawMessage(`{}`),
	})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		respBytes, err := s.HandleMessage(ctx, reqBytes)
		if err != nil || len(respBytes) == 0 {
			b.Fatalf("HandleMessage initialize failed: %v", err)
		}
	}
}

// BenchmarkMCPToolsList benchmarks the MCP tools/list discovery roundtrip latency.
func BenchmarkMCPToolsList(b *testing.B) {
	s := NewServer()
	defer s.Close()

	reqBytes, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      makeRawID(2),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		respBytes, err := s.HandleMessage(ctx, reqBytes)
		if err != nil || len(respBytes) == 0 {
			b.Fatalf("HandleMessage tools/list failed: %v", err)
		}
	}
}

// BenchmarkMCPCondenseToolCall benchmarks an in-process tool call (mesh_condense) via MCP JSON-RPC.
func BenchmarkMCPCondenseToolCall(b *testing.B) {
	s := NewServer()
	defer s.Close()

	reqBytes, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      makeRawID(3),
		Method:  "tools/call",
		Params: json.RawMessage(`{
			"name": "mesh_condense",
			"arguments": {
				"raw_text": "Error: connection refused\nError: connection refused\nError: connection refused\nDone.",
				"format": "generic",
				"max_lines": 50
			}
		}`),
	})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		respBytes, err := s.HandleMessage(ctx, reqBytes)
		if err != nil || len(respBytes) == 0 {
			b.Fatalf("HandleMessage tools/call failed: %v", err)
		}
	}
}
