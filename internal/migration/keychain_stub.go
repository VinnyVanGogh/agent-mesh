//go:build !darwin

package migration

// GetProjectDSN is a stub on non-darwin platforms.
// The Keychain credential store is macOS-only; other platforms always return
// ("", nil), causing the caller to fall back to manual verification mode.
func GetProjectDSN(_ string) (string, error) { return "", nil }

// SetProjectDSN is a stub on non-darwin platforms.
func SetProjectDSN(_, _ string) error { return nil }

// DeleteProjectDSN is a stub on non-darwin platforms.
func DeleteProjectDSN(_ string) error { return nil }
