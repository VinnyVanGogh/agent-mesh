//go:build !windows

package main

import (
	"context"
	"errors"
)

// isWindowsService always returns false on non-Windows platforms.
func isWindowsService() (bool, error) { return false, nil }

// runAsService is unreachable on non-Windows; present for build symmetry.
func runAsService(_ func(ctx context.Context) error) error {
	return errors.New("Windows services are only supported on Windows")
}

// installService is unreachable on non-Windows; present for build symmetry.
func installService(_ string) error {
	return errors.New("Windows services are only supported on Windows")
}

// removeService is unreachable on non-Windows; present for build symmetry.
func removeService() error {
	return errors.New("Windows services are only supported on Windows")
}
