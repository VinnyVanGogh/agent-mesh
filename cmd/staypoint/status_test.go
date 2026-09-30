package main

import (
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

func TestFormat3PLine(t *testing.T) {
	now := time.Date(2026, 9, 29, 13, 47, 0, 0, time.Local)
	reset := time.Date(2026, 9, 29, 15, 59, 0, 0, time.Local)

	t.Run("5h exhausted, weekly healthy names the window", func(t *testing.T) {
		p := &router.QuotaPool{
			IsLocked: true,
			FiveHour: router.QuotaWindow{RemainingPct: 0, ResetsAt: reset, IsLocked: true},
			Weekly:   router.QuotaWindow{RemainingPct: 65.9},
		}
		got := format3PLine(p, now)
		for _, want := range []string{"Locked", "5h window exhausted", "resets today 3:59 PM", "Week Left: 65.9%", "5h Left: 0.0%"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in %q", want, got)
			}
		}
	})
	t.Run("weekly exhausted", func(t *testing.T) {
		p := &router.QuotaPool{
			IsLocked: true,
			FiveHour: router.QuotaWindow{RemainingPct: 50},
			Weekly:   router.QuotaWindow{RemainingPct: 0, ResetsAt: reset, IsLocked: true},
		}
		if got := format3PLine(p, now); !strings.Contains(got, "week window exhausted") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("available", func(t *testing.T) {
		p := &router.QuotaPool{
			FiveHour: router.QuotaWindow{RemainingPct: 80},
			Weekly:   router.QuotaWindow{RemainingPct: 65.9},
		}
		got := format3PLine(p, now)
		if !strings.Contains(got, "Available") || strings.Contains(got, "Locked") {
			t.Errorf("got %q", got)
		}
	})
}
