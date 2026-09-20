package router

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tokyo Night Palette
const (
	Cyan    = "\033[38;2;125;207;255m"
	Blue    = "\033[38;2;122;162;247m"
	Purple  = "\033[38;2;187;154;247m"
	Green   = "\033[38;2;158;206;106m"
	Yellow  = "\033[38;2;224;175;104m"
	Red     = "\033[38;2;247;118;142m"
	Orange  = "\033[38;2;255;158;100m"
	Magenta = "\033[38;2;255;121;198m"
	Gray    = "\033[38;2;86;95;137m"
	Teal    = "\033[38;2;115;218;202m"
	Dim     = "\033[2m"
	Bold    = "\033[1m"
	Reset   = "\033[0m"

	BarBG    = "\033[48;2;0;0;0m"
	BarEmpty = "\033[38;2;54;60;88m"
	Sep      = " \033[38;2;86;95;137m│\033[0m "
)

type RGB struct {
	R, G, B int
}

var (
	RampCtxCool  = RGB{125, 207, 255}
	RampCtxHot   = RGB{198, 88, 196}
	RampFiveCool = RGB{255, 158, 100}
	RampFiveHot  = RGB{232, 93, 62}
	RampWeekCool = RGB{255, 121, 198}
	RampWeekHot  = RGB{168, 46, 88}
)

type StatuslinePayload struct {
	Model struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cwd        string `json:"cwd"`
	SessionID  string `json:"session_id"`
	VimMode    string `json:"vim_mode"`
	RateLimits struct {
		FiveHour struct {
			UsedPercentage *float64 `json:"used_percentage"`
			ResetsAt       *float64 `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			UsedPercentage *float64 `json:"used_percentage"`
			ResetsAt       *float64 `json:"resets_at"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
	ContextWindow struct {
		UsedPercentage    *float64 `json:"used_percentage"`
		RemainingTokens   *int64   `json:"remaining_tokens"`
		ContextWindowSize *int64   `json:"context_window_size"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
}

type gitCacheInfo struct {
	Branch    string `json:"branch"`
	Dirty     string `json:"dirty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	RemoteURL string `json:"remote_url"`
}

// BuildBar constructs a Tokyo Night progress bar with continuous non-linear color interpolation.
func BuildBar(pct int, cool, hot RGB, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	t := math.Pow(float64(pct)/100.0, 1.4)
	r := int(math.Round(float64(cool.R) + float64(hot.R-cool.R)*t))
	g := int(math.Round(float64(cool.G) + float64(hot.G-cool.G)*t))
	b := int(math.Round(float64(cool.B) + float64(hot.B-cool.B)*t))
	fill := fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)

	filled := pct * width / 100
	if pct > 0 && filled == 0 {
		filled = 1
	}
	empty := width - filled

	return fmt.Sprintf("%s%s▏%s%s%s%s%s▕%s",
		BarBG, Gray, fill, strings.Repeat("█", filled),
		BarEmpty, strings.Repeat("░", empty), Gray, Reset)
}

// FormatTokens formats token counts cleanly (e.g. 58k, 1.2M, 8.1B).
func FormatTokens(tokens int64) string {
	if tokens >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB", float64(tokens)/1_000_000_000.0)
	} else if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000.0)
	} else if tokens >= 1_000 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func formatResetTime(t time.Time, includeDay bool) string {
	if t.IsZero() {
		return ""
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err == nil {
		t = t.In(loc)
	}
	if includeDay {
		return strings.ToLower(t.Format("Mon 3:04pm"))
	}
	return strings.ToLower(t.Format("3:04pm"))
}

// fastGitInfo reads git branch and cache without launching slow subprocesses.
func fastGitInfo(dir string) (branch, dirty, sync string) {
	// 1. Check git cache in /tmp first (<0.1ms)
	h := md5.Sum([]byte(dir))
	cacheKey := hex.EncodeToString(h[:8])
	cacheFile := filepath.Join(os.TempDir(), fmt.Sprintf("statusline-git-%s.cache", cacheKey))
	if stat, err := os.Stat(cacheFile); err == nil && time.Since(stat.ModTime()) < 20*time.Second {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var gc gitCacheInfo
			if json.Unmarshal(data, &gc) == nil && gc.Branch != "" {
				s := ""
				if gc.Ahead > 0 {
					s += fmt.Sprintf("⇡%d", gc.Ahead)
				}
				if gc.Behind > 0 {
					s += fmt.Sprintf("⇣%d", gc.Behind)
				}
				return gc.Branch, gc.Dirty, s
			}
		}
	}

	// 2. Direct read of .git/HEAD (<0.05ms)
	gitDir := filepath.Join(dir, ".git")
	headPath := filepath.Join(gitDir, "HEAD")

	// Handle worktrees/submodules where .git is a file
	if fi, err := os.Stat(gitDir); err == nil && !fi.IsDir() {
		if content, err := os.ReadFile(gitDir); err == nil {
			line := strings.TrimSpace(string(content))
			if strings.HasPrefix(line, "gitdir:") {
				rel := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(rel) {
					rel = filepath.Join(dir, rel)
				}
				headPath = filepath.Join(rel, "HEAD")
			}
		}
	}

	if headContent, err := os.ReadFile(headPath); err == nil {
		headStr := strings.TrimSpace(string(headContent))
		if strings.HasPrefix(headStr, "ref: refs/heads/") {
			branch = strings.TrimPrefix(headStr, "ref: refs/heads/")
		} else if len(headStr) >= 7 {
			branch = headStr[:7]
		}
	}

	return branch, "", ""
}

// RenderStatusline produces the Tokyo Night multi-line statusline in <5ms.
func RenderStatusline(w io.Writer, r io.Reader) error {
	// 1. Read input payload if piped (e.g. from Claude Code)
	var payload StatuslinePayload
	hasPipedInput := false

	if r != nil {
		// Check if reader is os.Stdin and whether it's a pipe
		if f, ok := r.(*os.File); ok {
			fi, err := f.Stat()
			if err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
				data, _ := io.ReadAll(r)
				if len(data) > 0 {
					_ = json.Unmarshal(data, &payload)
					hasPipedInput = true
				}
			}
		} else {
			data, _ := io.ReadAll(r)
			if len(data) > 0 {
				_ = json.Unmarshal(data, &payload)
				hasPipedInput = true
			}
		}
	}

	// 2. Determine directory
	dir := payload.Workspace.CurrentDir
	if dir == "" {
		dir = payload.Cwd
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dirName := filepath.Base(dir)

	// 3. Load live pacer state (<1ms)
	pacerState, _ := LoadPacerState()

	// 4. Determine work vs personal repo
	isWork, _, _ := IsWorkRepo(dir)

	// 5. Build Model badge
	modelName := payload.Model.DisplayName
	if modelName == "" {
		modelName = payload.Model.ID
	}
	if modelName == "" {
		if hasPipedInput {
			modelName = "Claude"
		} else if isWork {
			modelName = "Claude (Work)"
		} else {
			modelName = "Gemini (Native)"
		}
	}

	modelIcon := "󰚩"
	if strings.Contains(strings.ToLower(modelName), "gemini") {
		modelIcon = "󰛡"
	}
	modelPart := fmt.Sprintf("%s %s%s%s", modelIcon, Cyan, modelName, Reset)

	// 6. Account badge
	var accountBadge string
	if isWork {
		accountBadge = fmt.Sprintf("🪪 %swork%s", Teal, Reset)
	} else {
		accountBadge = fmt.Sprintf("🪪 %spersonal%s", Gray, Reset)
	}

	// 7. Cost badge
	var costBadge string
	if payload.Cost.TotalCostUSD != nil && *payload.Cost.TotalCostUSD > 0.001 {
		costBadge = fmt.Sprintf("💰 %s$%.2f%s", Yellow, *payload.Cost.TotalCostUSD, Reset)
	}

	// 8. Line 1
	line1Parts := []string{modelPart, accountBadge}
	if costBadge != "" {
		line1Parts = append(line1Parts, costBadge)
	}
	line1Parts = append(line1Parts, fmt.Sprintf("%s⚡ mesh:active%s", Teal, Reset))

	// 9. Git info & badges for Line 2
	branch, dirty, sync := fastGitInfo(dir)
	var gitPart string
	if branch != "" {
		gitPart = fmt.Sprintf("%s🐙 %s%s", Purple, branch, Reset)
		if dirty != "" {
			gitPart += fmt.Sprintf(" %s", dirty)
		}
		if sync != "" {
			gitPart += fmt.Sprintf(" %s%s%s", Yellow, sync, Reset)
		}
	}

	line2Parts := []string{fmt.Sprintf("%s📁 %s%s", Blue, dirName, Reset)}
	if gitPart != "" {
		line2Parts = append(line2Parts, gitPart)
	}

	if payload.VimMode != "" {
		vimColor := Green
		if payload.VimMode != "INSERT" {
			vimColor = Blue
		}
		line2Parts = append(line2Parts, fmt.Sprintf("%s%s%s%s", vimColor, Bold, payload.VimMode, Reset))
	}

	// Check Caveman mode
	home, _ := os.UserHomeDir()
	cavemanFile := filepath.Join(home, ".claude", ".caveman-mode")
	if _, err := os.Stat(cavemanFile); err == nil {
		line2Parts = append(line2Parts, fmt.Sprintf("%s🦴 CAVEMAN%s", Yellow, Reset))
	}

	// 10. Quotas & Meters
	var fivePct, weekPct float64
	var fiveResetsAt, weekResetsAt time.Time
	var isLocked bool
	var lockoutLabel string

	// Select matching pool
	activePoolID := PoolPersonalClaude
	if isWork {
		activePoolID = PoolWorkClaude
	} else if strings.Contains(strings.ToLower(modelName), "gemini") {
		activePoolID = PoolGeminiNative
	}

	if pacerState != nil && pacerState.Pools[activePoolID] != nil {
		p := pacerState.Pools[activePoolID]
		fivePct = p.FiveHour.UsedPct
		fiveResetsAt = p.FiveHour.ResetsAt
		weekPct = p.Weekly.UsedPct
		weekResetsAt = p.Weekly.ResetsAt
		isLocked = p.IsLocked
		if isLocked {
			lockoutLabel = formatResetTime(p.LockoutUntil, false)
			if lockoutLabel == "" {
				lockoutLabel = "soon"
			}
		}
	}

	// Override from Claude payload if provided
	if payload.RateLimits.FiveHour.UsedPercentage != nil {
		fivePct = *payload.RateLimits.FiveHour.UsedPercentage
		if payload.RateLimits.FiveHour.ResetsAt != nil && *payload.RateLimits.FiveHour.ResetsAt > 0 {
			fiveResetsAt = time.Unix(int64(*payload.RateLimits.FiveHour.ResetsAt), 0)
		}
	}
	if payload.RateLimits.SevenDay.UsedPercentage != nil {
		weekPct = *payload.RateLimits.SevenDay.UsedPercentage
		if payload.RateLimits.SevenDay.ResetsAt != nil && *payload.RateLimits.SevenDay.ResetsAt > 0 {
			weekResetsAt = time.Unix(int64(*payload.RateLimits.SevenDay.ResetsAt), 0)
		}
	}

	if fivePct >= 100.0 {
		isLocked = true
		lockoutLabel = formatResetTime(fiveResetsAt, false)
	}

	if isLocked {
		line2Parts = append(line2Parts, fmt.Sprintf("%s%s🔒 5H LOCKED (@%s)%s", Red, Bold, lockoutLabel, Reset))
		if !isWork {
			line2Parts = append(line2Parts, fmt.Sprintf("%s%s[⚡ Switch -> agy / Gemini]%s", Yellow, Bold, Reset))
		}
	}

	// Print Line 1 & Line 2
	fmt.Fprintln(w, strings.Join(line1Parts, Sep))
	fmt.Fprintln(w, strings.Join(line2Parts, Sep))

	// Line 3: Context meter (if available in payload)
	if payload.ContextWindow.UsedPercentage != nil {
		ctxPct := int(math.Round(*payload.ContextWindow.UsedPercentage))
		remTokensStr := ""
		if payload.ContextWindow.RemainingTokens != nil {
			remTokensStr = fmt.Sprintf(" %s~%s left%s", Dim, FormatTokens(*payload.ContextWindow.RemainingTokens), Reset)
		}
		bar := BuildBar(ctxPct, RampCtxCool, RampCtxHot, 20)
		fmt.Fprintf(w, "%s %sctx:%d%%%s%s\n", bar, Cyan, ctxPct, Reset, remTokensStr)
	}

	// Line 4: 5-Hour Session meter
	fiveInt := int(math.Round(fivePct))
	rem5h := math.Max(0, 100.0-fivePct)
	reset5hStr := ""
	if rStr := formatResetTime(fiveResetsAt, false); rStr != "" {
		reset5hStr = fmt.Sprintf(" %s@%s%s", Dim, rStr, Reset)
	}
	bar5h := BuildBar(fiveInt, RampFiveCool, RampFiveHot, 20)
	fmt.Fprintf(w, "%s %ssession:%d%%%s %s~%.0f%% left%s%s\n",
		bar5h, Orange, fiveInt, Reset, Dim, rem5h, Reset, reset5hStr)

	// Line 5: Weekly meter
	weekInt := int(math.Round(weekPct))
	remW := math.Max(0, 100.0-weekPct)
	resetWStr := ""
	if rStr := formatResetTime(weekResetsAt, true); rStr != "" {
		resetWStr = fmt.Sprintf(" %s@%s%s", Dim, rStr, Reset)
	}
	barW := BuildBar(weekInt, RampWeekCool, RampWeekHot, 20)
	fmt.Fprintf(w, "%s %sweekly:%d%%%s %s~%.0f%% left%s%s\n",
		barW, Magenta, weekInt, Reset, Dim, remW, Reset, resetWStr)

	// Line 6: Plan / Dynamic Route line
	if pacerState != nil {
		var planParts []string
		if isWork {
			planParts = append(planParts, fmt.Sprintf("route ▸ %sremote-claude%s", Green, Reset))
			planParts = append(planParts, fmt.Sprintf("fallback: %slocal-work%s", Yellow, Reset))
			if pacerState.Pools[PoolWorkClaude] != nil {
				planParts = append(planParts, fmt.Sprintf("runway: %d turns", pacerState.Pools[PoolWorkClaude].TurnsRunway))
			}
		} else {
			geminiPool := pacerState.Pools[PoolGeminiNative]
			if geminiPool != nil && !geminiPool.IsLocked && geminiPool.Weekly.RemainingPct > 0 {
				planParts = append(planParts, fmt.Sprintf("route ▸ %sgemini-3.8-flash-high (agy)%s", Green, Reset))
				planParts = append(planParts, fmt.Sprintf("fallback: %sclaude-sonnet-4-6%s", Yellow, Reset))
				planParts = append(planParts, fmt.Sprintf("runway: %d turns", geminiPool.TurnsRunway))
			} else {
				planParts = append(planParts, fmt.Sprintf("route ▸ %sclaude-sonnet-4-6 (agy 3P)%s", Orange, Reset))
				planParts = append(planParts, fmt.Sprintf("gemini locked (%s)", formatResetTime(geminiPool.LockoutUntil, false)))
			}
		}

		if len(planParts) > 0 {
			bullet := fmt.Sprintf(" %s·%s ", Gray, Reset)
			fmt.Fprintf(w, "%splan:%s %s\n", Green, Reset, strings.Join(planParts, bullet))
		}
	}

	return nil
}
