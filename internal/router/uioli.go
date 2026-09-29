package router

import (
	"fmt"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
)

// Use-it-or-lose-it (UIOLI) routing: a weekly quota window that is about to
// reset with unspent quota is wasted capacity, so near the end of the window the
// router prefers the pool that would otherwise expire unused.
const (
	DefaultUIOLIWindowHours     = 24.0 // only the last day of the weekly window
	DefaultUIOLIMinRemainingPct = 15.0 // ignore crumbs not worth steering for
	DefaultUIOLIMinPctPerHour   = 1.0  // remaining% / hours-to-reset burn-down needed

	// UIOLIHighPriorityModel is used for high-priority tasks when UIOLI steers
	// to personal Claude Code and no model was explicitly requested.
	UIOLIHighPriorityModel = "claude-fable-5-1"
)

// UIOLIConfig tunes end-of-window burn-down routing. Zero values select the
// defaults above. It is inactive outside the end-of-window case by design:
// hours-to-reset must be <= WindowHours before it can trigger.
type UIOLIConfig struct {
	Disabled        bool
	WindowHours     float64
	MinRemainingPct float64
	MinPctPerHour   float64
}

func (c UIOLIConfig) withDefaults() UIOLIConfig {
	if c.WindowHours <= 0 {
		c.WindowHours = DefaultUIOLIWindowHours
	}
	if c.MinRemainingPct <= 0 {
		c.MinRemainingPct = DefaultUIOLIMinRemainingPct
	}
	if c.MinPctPerHour <= 0 {
		c.MinPctPerHour = DefaultUIOLIMinPctPerHour
	}
	return c
}

// UIOLIPressure is the reset-time-aware burn-down state of a pool.
type UIOLIPressure struct {
	Active         bool
	RemainingPct   float64
	HoursToReset   float64
	PctPerHourNeed float64 // remaining% / hours-to-reset
}

// UIOLIPressure evaluates whether the pool's weekly window is in the
// end-of-window burn-down zone: known data, a future reset within WindowHours,
// enough unspent quota, and a required burn rate of at least MinPctPerHour.
func (p *QuotaPool) UIOLIPressure(now time.Time, cfg UIOLIConfig) UIOLIPressure {
	cfg = cfg.withDefaults()
	var out UIOLIPressure
	if cfg.Disabled || p == nil || p.IsLocked || !p.Weekly.Known || !p.Weekly.ResetsAt.After(now) {
		return out
	}
	out.RemainingPct = p.Weekly.RemainingPct
	out.HoursToReset = p.Weekly.ResetsAt.Sub(now).Hours()
	if out.HoursToReset > cfg.WindowHours || out.RemainingPct < cfg.MinRemainingPct {
		return out
	}
	// Floor the divisor so a reset seconds away does not produce Inf.
	out.PctPerHourNeed = out.RemainingPct / max(out.HoursToReset, 0.25)
	out.Active = out.PctPerHourNeed >= cfg.MinPctPerHour
	return out
}

// UIOLIFromConfig builds the UIOLI tuning from user config; nil yields defaults.
func UIOLIFromConfig(c *config.Config) UIOLIConfig {
	if c == nil {
		return UIOLIConfig{}
	}
	return UIOLIConfig{
		Disabled:        c.UIOLIDisabled,
		WindowHours:     c.UIOLIWindowHours,
		MinRemainingPct: c.UIOLIMinRemainingPct,
		MinPctPerHour:   c.UIOLIMinPctPerHour,
	}
}

func (u UIOLIPressure) describe() string {
	return fmt.Sprintf("use-it-or-lose-it: %.0f%% weekly left, resets in %s (%.1f%%/h to burn down)",
		u.RemainingPct, FormatDuration(time.Duration(u.HoursToReset*float64(time.Hour))), u.PctPerHourNeed)
}
