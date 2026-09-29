package router

import (
	"testing"
	"time"
)

func TestOnWeeklyPace(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		used     float64
		daysLeft float64
		want     bool
	}{
		{"low remaining but early pace is fine", 80, 0.5, true},
		{"plenty remaining but burning too fast", 30, 6, false},
		{"unused week", 0, 3, true},
		{"exhausted", 100, 2, false},
		{"steady mid-week burn", 50, 3.5, true},
	}
	for _, c := range cases {
		p := &QuotaPool{Weekly: QuotaWindow{
			UsedPct:  c.used,
			ResetsAt: now.Add(time.Duration(c.daysLeft * 24 * float64(time.Hour))),
		}}
		if got := p.OnWeeklyPace(now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
