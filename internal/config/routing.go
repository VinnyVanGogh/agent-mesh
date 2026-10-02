package config

// RoutingKindEntry is one slot in a work-kind provider chain parsed from
// the [routing] section of config.toml.
type RoutingKindEntry struct {
	// Provider: "claude-opus", "claude-sonnet", "gemini-3.1-pro",
	// "gemini-3.8-flash", or "claude-cloud".
	Provider string `toml:"provider" json:"provider"`
	// Model is the explicit CLI flag value (e.g. "opus", "gemini-3.1-pro-high").
	Model string `toml:"model" json:"model"`
	// Enabled defaults to true when the key is absent.
	Enabled *bool `toml:"enabled" json:"enabled,omitempty"`
	// CloudCreditExpires is RFC3339; slots with an expired date are skipped.
	CloudCreditExpires string `toml:"cloud_credit_expires" json:"cloud_credit_expires,omitempty"`
}

// RoutingConfig holds the optional [routing] table from config.toml.
// Absence of this table causes the built-in DefaultKindChains to be used.
//
// TODO(STA-316): add Routing *RoutingConfig field to Config struct.
type RoutingConfig struct {
	Coding       []RoutingKindEntry `toml:"coding"       json:"coding"`
	Architecture []RoutingKindEntry `toml:"architecture" json:"architecture"`
	Planning     []RoutingKindEntry `toml:"planning"     json:"planning"`
	QA           []RoutingKindEntry `toml:"qa"           json:"qa"`
}
