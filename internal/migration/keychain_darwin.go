//go:build darwin

package migration

import (
	"os/exec"
	"strings"
)

// keychainService is the Keychain service name for StayPoint project DB credentials.
const keychainService = "staypoint-readonly-db"

// GetProjectDSN retrieves the read-only PostgreSQL DSN for projectID from the
// macOS Keychain. Returns ("", nil) when no item exists for that project.
func GetProjectDSN(projectID string) (string, error) {
	out, err := exec.Command(
		"security", "find-generic-password",
		"-s", keychainService,
		"-a", projectID,
		"-w",
	).Output()
	if err != nil {
		// exit 44 means "item not found" — not an error for callers
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
			return "", nil
		}
		return "", nil // treat any error as "not configured"
	}
	return strings.TrimSpace(string(out)), nil
}

// SetProjectDSN stores dsn in the Keychain under service=keychainService,
// account=projectID. Used by tests and the settings UI.
func SetProjectDSN(projectID, dsn string) error {
	// Delete existing item first (ignore errors)
	_ = exec.Command(
		"security", "delete-generic-password",
		"-s", keychainService,
		"-a", projectID,
	).Run()

	return exec.Command(
		"security", "add-generic-password",
		"-s", keychainService,
		"-a", projectID,
		"-w", dsn,
	).Run()
}

// DeleteProjectDSN removes the Keychain item for projectID.
func DeleteProjectDSN(projectID string) error {
	err := exec.Command(
		"security", "delete-generic-password",
		"-s", keychainService,
		"-a", projectID,
	).Run()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
		return nil // already gone
	}
	return err
}
