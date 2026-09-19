package router

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func BenchmarkStatusline(b *testing.B) {
	payload := `{"model":{"display_name":"Claude 3.7 Sonnet"},"workspace":{"current_dir":"/Users/vincevasile/Documents/dev/agent-mesh"},"cost":{"total_cost_usd":0.43},"context_window":{"used_percentage":18,"remaining_tokens":164000}}`
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader([]byte(payload))
		if err := RenderStatusline(io.Discard, r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacerLoad(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadPacerState(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRoute(b *testing.B) {
	ctx := context.Background()
	pacerState, err := LoadPacerState()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Route(ctx, "/Users/vincevasile/Documents/dev/agent-mesh", pacerState, RouteOptions{
			CheckSSH: false,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
