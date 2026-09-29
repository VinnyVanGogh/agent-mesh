package quota

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// KeychainReader reads a generic-password secret by service name.
type KeychainReader interface {
	Read(ctx context.Context, service string) (string, error)
}

// SecurityKeychain reads the macOS Keychain via the `security` CLI.
type SecurityKeychain struct{}

func (SecurityKeychain) Read(ctx context.Context, service string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("%w: keychain unavailable on %s", ErrNoCredentials, runtime.GOOS)
	}
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		// Do not surface stderr; exit status is enough.
		return "", fmt.Errorf("%w: keychain item %q not readable", ErrNoCredentials, service)
	}
	return strings.TrimSpace(string(out)), nil
}
