package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func FuzzHandleMessage(f *testing.F) {
	// Seed corpus with valid and edge-case JSON-RPC messages
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"staypoint_status","arguments":{}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":null,"method":"unknown"}`))
	f.Add([]byte(`{"invalid-json`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call"}`))

	server := NewServer()

	f.Fuzz(func(t *testing.T, data []byte) {
		ctx := context.Background()
		respBytes, err := server.HandleMessage(ctx, data)
		if err != nil {
			return
		}

		if len(respBytes) > 0 {
			var resp Response
			if unmarshalErr := json.Unmarshal(respBytes, &resp); unmarshalErr != nil {
				t.Fatalf("HandleMessage returned invalid JSON response: %v, raw: %s", unmarshalErr, string(respBytes))
			}
			if resp.JSONRPC != "2.0" {
				t.Fatalf("expected jsonrpc 2.0, got: %s", resp.JSONRPC)
			}
		}
	})
}
