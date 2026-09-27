//go:build !windows

// Package ipc provides POSIX Unix domain socket transport for staypointd IPC.
package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

const socketName = "staypointd.sock"

// SocketPath returns the canonical Unix domain socket path for staypointd.
//
// Priority order on Linux:
//  1. $RUNTIME_DIRECTORY (set by systemd RuntimeDirectory=staypoint)
//  2. $XDG_RUNTIME_DIR/staypoint/staypointd.sock
//  3. $HOME/.staypoint/staypointd.sock
//
// On non-Linux: $HOME/.staypoint/staypointd.sock.
func SocketPath() string {
	if runtime.GOOS == "linux" {
		// systemd injects $RUNTIME_DIRECTORY when RuntimeDirectory= is set in the unit.
		if dir := os.Getenv("RUNTIME_DIRECTORY"); dir != "" {
			return filepath.Join(dir, socketName)
		}
		if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
			return filepath.Join(dir, "staypoint", socketName)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/tmp", socketName)
	}
	return filepath.Join(home, ".staypoint", socketName)
}

// Listen creates a Unix domain socket listener at socketPath and calls
// handleConn for each accepted connection in a new goroutine.
// Blocks until ctx is cancelled, then closes the listener and removes the socket.
func Listen(ctx context.Context, socketPath string, handleConn func(net.Conn)) error {
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("ipc: mkdir %s: %w", dir, err)
	}
	// Remove a stale socket file left by a previous run.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("ipc: listen %s: %w", socketPath, err)
	}
	// Restrict to owner only; prevents other users from connecting.
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("ipc: chmod %s: %w", socketPath, err)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				_ = os.Remove(socketPath)
				return ctx.Err()
			default:
				return fmt.Errorf("ipc: accept: %w", err)
			}
		}
		go handleConn(conn)
	}
}

// Dial connects to the staypointd Unix domain socket at socketPath.
func Dial(socketPath string) (net.Conn, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial %s: %w (is staypointd running?)", socketPath, err)
	}
	return conn, nil
}
