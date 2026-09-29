package telemetry

import (
	"strings"
	"testing"
)

func TestRateCardEmbeddedAndAttributed(t *testing.T) {
	c, err := loadRateCard()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) == 0 {
		t.Fatal("embedded rate card has no models")
	}
	if !strings.Contains(c.License, "MIT") || !strings.Contains(c.Source, "models.dev") {
		t.Errorf("attribution missing: source=%q license=%q", c.Source, c.License)
	}
}

func TestComputeCostMicros_BaseRates(t *testing.T) {
	// claude-sonnet-4-5: $3 in / $15 out / $0.30 read / $3.75 5m write / $6 1h write per Mtok.
	tests := []struct {
		name string
		u    Usage
		want int64
	}{
		{"input only", Usage{Input: 1_000_000}, 3_000_000},
		{"output only", Usage{Output: 1_000_000}, 15_000_000},
		{"cache read", Usage{CacheRead: 1_000_000}, 300_000},
		{"cache write 5m", Usage{CacheCreation5m: 1_000_000}, 3_750_000},
		{"cache write 1h", Usage{CacheCreation1h: 1_000_000}, 6_000_000},
		{"all five", Usage{Input: 1000, Output: 2000, CacheRead: 10_000, CacheCreation5m: 4000, CacheCreation1h: 500}, 3000 + 30000 + 3000 + 15000 + 3000},
		{"zero", Usage{}, 0},
	}
	for _, tt := range tests {
		got, ok := ComputeCostMicros("claude-sonnet-4-5", tt.u)
		if !ok {
			t.Fatalf("%s: model not found", tt.name)
		}
		if got != tt.want {
			t.Errorf("%s: got %d micros, want %d", tt.name, got, tt.want)
		}
	}
}

func TestComputeCostMicros_RoundsOncePerRequest(t *testing.T) {
	// $0.30/Mtok read: 1 token = 0.3 micro-dollars. Ten tokens = 3, not 10*round(0.3)=0.
	if got, _ := ComputeCostMicros("claude-sonnet-4-5", Usage{CacheRead: 10}); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
	// Half-micro rounds up: 5 tokens at $0.30 = 1.5 -> 2.
	if got, _ := ComputeCostMicros("claude-sonnet-4-5", Usage{CacheRead: 5}); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestComputeCostMicros_ContextTier(t *testing.T) {
	// gemini-3.1-pro-preview: $2/$12/$0.20 up to 200k context, $4/$18/$0.40 above.
	model := "gemini-3.1-pro-preview"

	// Exactly at the threshold stays on the base tier ("over" is strict).
	got, _ := ComputeCostMicros(model, Usage{Input: 200_000, Output: 1_000_000})
	if want := int64(200_000*2 + 12_000_000); got != want {
		t.Errorf("at threshold: got %d, want %d", got, want)
	}

	// One token past the threshold flips the WHOLE request to the higher tier.
	got, _ = ComputeCostMicros(model, Usage{Input: 200_001, Output: 1_000_000})
	if want := int64(200_001*4 + 18_000_000); got != want {
		t.Errorf("over threshold: got %d, want %d", got, want)
	}

	// Cached tokens count toward context: 150k input + 60k cache read = 210k.
	got, _ = ComputeCostMicros(model, Usage{Input: 150_000, CacheRead: 60_000})
	if want := int64(150_000*4 + 60_000*400_000/1_000_000); got != want {
		t.Errorf("cache counts toward context: got %d, want %d", got, want)
	}

	// Tier omits cache_write in the source: falls back to the base entry, no panic.
	if _, ok := ComputeCostMicros(model, Usage{CacheCreation5m: 300_000}); !ok {
		t.Fatal("model not found")
	}
}

func TestPriceMicros_HighestMatchingTierWins(t *testing.T) {
	m := modelRate{
		rate: rate{Input: 1_000_000},
		Tiers: []tierRate{
			{rate: rate{Input: 2_000_000}, ContextOver: 100_000},
			{rate: rate{Input: 5_000_000}, ContextOver: 500_000},
		},
	}
	for _, tt := range []struct {
		ctx  int64
		want int64
	}{
		{100_000, 1_000_000}, // not over
		{100_001, 2_000_000},
		{500_000, 2_000_000},
		{500_001, 5_000_000},
	} {
		if got := m.effective(tt.ctx).Input; got != tt.want {
			t.Errorf("ctx %d: input rate %d, want %d", tt.ctx, got, tt.want)
		}
	}
}

func TestPriceMicros_Missing1hRateFallsBackTo5m(t *testing.T) {
	m := modelRate{rate: rate{CacheWrite: 1_250_000}}
	if got := priceMicros(m, Usage{CacheCreation1h: 1_000_000}); got != 1_250_000 {
		t.Errorf("got %d, want 1250000", got)
	}
}

func TestLookupModel_Normalization(t *testing.T) {
	c, err := loadRateCard()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"claude-sonnet-4-5",
		"Claude-Sonnet-4-5",
		"anthropic/claude-sonnet-4-5",
		"claude-sonnet-4-5[1m]",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-5@default",
	} {
		if _, ok := lookupModel(c, id); !ok {
			t.Errorf("%q not resolved", id)
		}
	}
	if _, ok := lookupModel(c, "definitely-not-a-model"); ok {
		t.Error("unknown model resolved")
	}
}

func TestComputeCostMicros_UnknownModel(t *testing.T) {
	got, ok := ComputeCostMicros("definitely-not-a-model", Usage{Input: 1_000_000})
	if ok || got != 0 {
		t.Errorf("got (%d, %v), want (0, false)", got, ok)
	}
}
