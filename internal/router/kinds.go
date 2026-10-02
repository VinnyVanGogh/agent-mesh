package router

// WorkKind is the category of work driving model-routing decisions.
type WorkKind string

const (
	WorkKindCoding       WorkKind = "coding"
	WorkKindArchitecture WorkKind = "architecture"
	WorkKindPlanning     WorkKind = "planning"
	WorkKindQA           WorkKind = "qa"
)

// ValidWorkKinds is the complete set of accepted WorkKind values.
var ValidWorkKinds = []WorkKind{
	WorkKindCoding,
	WorkKindArchitecture,
	WorkKindPlanning,
	WorkKindQA,
}

// KindSlot is one ordered entry in a work-kind routing chain.
type KindSlot struct {
	// Provider identifies the pool: "claude-opus", "claude-sonnet",
	// "gemini-3.1-pro", "gemini-3.8-flash", "claude-cloud".
	Provider string
	// Model is the exact model flag to pass to the CLI
	// (e.g. "opus", "sonnet", "gemini-3.1-pro-high", "gemini-3.8-flash-high").
	Model string
	// PoolID is used to check quota lockout via PacerState.
	PoolID PoolID
	// Enabled controls whether the slot participates at all.
	// Cloud slot is off until STA-410 lands.
	Enabled bool
	// CloudCreditExpires is RFC3339; non-empty slots skip when expired.
	// Empty means no credit expiry.
	CloudCreditExpires string
}

// DefaultKindChains returns the built-in routing table when no [routing] section
// is present in config.toml.
//
// Routing table (source of truth):
//
//	coding       : Claude Opus → Gemini 3.1 Pro
//	architecture : Gemini 3.1 Pro → Claude Cloud (Opus) [off] → Claude Opus
//	planning     : Gemini 3.8 Flash → Claude Cloud [off] → Claude Sonnet
//	qa           : Gemini 3.8 Flash → Claude Sonnet
//
// TODO(STA-316): implement – currently returns nil so tests fail.
func DefaultKindChains() map[WorkKind][]KindSlot {
	return nil
}

// ResolveKindChain walks the chain for kind, skipping locked, expired, or
// disabled slots, and returns the first viable slot. Returns nil when every
// slot is unavailable.
//
// TODO(STA-316): implement – currently returns nil so tests fail.
func ResolveKindChain(kind WorkKind, chains map[WorkKind][]KindSlot, pacer *PacerState) *KindSlot {
	return nil
}

// PairModelBidirectional maps a model to its cross-provider equivalent:
//
//	Claude Opus  <->  Gemini 3.1 Pro
//	Claude Sonnet <-> Gemini 3.8 Flash
//
// Returns "" for unknown models.
//
// TODO(STA-316): implement – currently returns "" so tests fail.
func PairModelBidirectional(model string) string {
	return ""
}
