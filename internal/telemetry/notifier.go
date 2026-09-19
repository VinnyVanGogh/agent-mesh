package telemetry

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/vincevasile/agent-mesh/internal/router"
)

type RateLimitNotifier struct {
	last3PLocked     bool
	lastGeminiLocked bool
	initialized      bool
}

func NewNotifier() *RateLimitNotifier {
	return &RateLimitNotifier{}
}

func (n *RateLimitNotifier) Start(ctx context.Context) {
	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()

	// Initial check
	n.check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.check()
		}
	}
}

func (n *RateLimitNotifier) check() {
	state, err := router.LoadPacerState()
	if err != nil {
		return
	}

	pool3P := state.Pools[router.Pool3PClaude]
	poolGem := state.Pools[router.PoolGeminiNative]

	is3PLocked := pool3P != nil && pool3P.IsLocked
	isGemLocked := poolGem != nil && poolGem.IsLocked

	if !n.initialized {
		n.last3PLocked = is3PLocked
		n.lastGeminiLocked = isGemLocked
		n.initialized = true
		return
	}

	// 3P Transition: Unlocked -> Locked
	if is3PLocked && !n.last3PLocked {
		resetStr := "soon"
		if pool3P != nil && !pool3P.LockoutUntil.IsZero() {
			resetStr = pool3P.LockoutUntil.Format("3:04pm")
		}
		SendNotification(
			"[Switch -> Gemini] 3P Quota Locked",
			fmt.Sprintf("3P 5-hour quota exhausted (resets @%s). Switch to Gemini 3.8 Flash for unblocked progress.", resetStr),
		)
	}

	// 3P Transition: Locked -> Reset
	if !is3PLocked && n.last3PLocked {
		SendNotification(
			"[Switch -> Claude] 3P Quota Ready",
			"3P quota has reset! Ready to switch back to Claude 4.6 for deep architecture.",
		)
	}

	// Gemini Transition: Unlocked -> Locked
	if isGemLocked && !n.lastGeminiLocked {
		SendNotification(
			"[Switch -> Claude] Gemini Quota Locked",
			"Gemini quota exhausted. Switch to Claude Sonnet or 3P for immediate progress.",
		)
	}

	// Gemini Transition: Locked -> Reset
	if !isGemLocked && n.lastGeminiLocked {
		SendNotification(
			"[Switch -> Gemini] Gemini Quota Ready",
			"Gemini quota has reset! Ready to switch back to primary Gemini model.",
		)
	}

	n.last3PLocked = is3PLocked
	n.lastGeminiLocked = isGemLocked
}

func SendNotification(title, message string) {
	log.Printf("[meshd notify] %s: %s", title, message)

	// AppleScript notification
	script := fmt.Sprintf(`display notification "%s" with title "%s" sound name "Glass"`, message, title)
	_ = exec.Command("osascript", "-e", script).Run()
}
