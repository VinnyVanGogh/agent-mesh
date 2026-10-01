//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// tempSockPath returns a short socket path under /tmp to stay under the
// 104-byte Unix domain socket path limit on macOS/BSD.
func tempSockPath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("/tmp", fmt.Sprintf("ipctest-%d-%s.sock", os.Getpid(), name))
	t.Cleanup(func() { os.Remove(path) })
	return path
}

// -- SocketPath tests --

func TestSocketPath_HomeDir(t *testing.T) {
	// Clear env vars so we hit the HOME fallback.
	t.Setenv("RUNTIME_DIRECTORY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available")
	}
	want := filepath.Join(home, ".staypoint", socketName)
	if got := SocketPath(); got != want {
		t.Errorf("SocketPath() = %q; want %q", got, want)
	}
}

func TestSocketPath_XDGRuntimeDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_RUNTIME_DIR logic is Linux-only")
	}
	t.Setenv("RUNTIME_DIRECTORY", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	want := filepath.Join("/run/user/1000", "staypoint", socketName)
	if got := SocketPath(); got != want {
		t.Errorf("SocketPath() = %q; want %q", got, want)
	}
}

func TestSocketPath_RuntimeDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RUNTIME_DIRECTORY logic is Linux-only")
	}
	t.Setenv("RUNTIME_DIRECTORY", "/run/staypoint")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000") // lower priority; should be ignored

	want := filepath.Join("/run/staypoint", socketName)
	if got := SocketPath(); got != want {
		t.Errorf("SocketPath() = %q; want %q (RUNTIME_DIRECTORY must take priority)", got, want)
	}
}

// -- Listen + Dial tests --

func TestListen_AcceptsConnection(t *testing.T) {
	sockPath := tempSockPath(t, "accept")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	received := make(chan []byte, 1)
	listenErr := make(chan error, 1)

	go func() {
		listenErr <- Listen(ctx, sockPath, func(conn net.Conn) {
			defer conn.Close()
			buf, err := io.ReadAll(conn)
			if err != nil {
				return
			}
			received <- buf
		})
	}()

	if err := waitForSocket(sockPath, 2*time.Second); err != nil {
		t.Fatalf("socket never appeared: %v", err)
	}

	conn, err := Dial(sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, _ = conn.Write([]byte("hello"))
	conn.Close()

	select {
	case msg := <-received:
		if string(msg) != "hello" {
			t.Errorf("received %q; want %q", msg, "hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	cancel()
	select {
	case err := <-listenErr:
		if err != context.Canceled {
			t.Errorf("Listen returned %v; want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Listen to return")
	}
}

func TestListen_Bidirectional(t *testing.T) {
	sockPath := tempSockPath(t, "bidir")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- Listen(ctx, sockPath, func(conn net.Conn) {
			defer conn.Close()
			buf := make([]byte, 5)
			if _, err := io.ReadFull(conn, buf); err != nil {
				return
			}
			_, _ = conn.Write([]byte(strings.ToUpper(string(buf))))
		})
	}()

	if err := waitForSocket(sockPath, 2*time.Second); err != nil {
		t.Fatalf("socket never appeared: %v", err)
	}

	conn, err := Dial(sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("world")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	reply := make([]byte, 5)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(reply) != "WORLD" {
		t.Errorf("reply = %q; want %q", reply, "WORLD")
	}

	cancel()
}

func TestListen_SocketRemovedOnCancel(t *testing.T) {
	sockPath := tempSockPath(t, "cleanup")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- Listen(ctx, sockPath, func(net.Conn) {}) }()

	if err := waitForSocket(sockPath, 2*time.Second); err != nil {
		t.Fatalf("socket never appeared: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return after cancel")
	}

	if _, err := os.Stat(sockPath); err == nil {
		t.Errorf("socket file %q still exists after cancel; expected cleanup", sockPath)
	}
}

func TestDial_FailsWithNoServer(t *testing.T) {
	sockPath := tempSockPath(t, "absent")
	conn, err := Dial(sockPath)
	if err == nil {
		conn.Close()
		t.Fatal("expected Dial to fail when no server is listening; got nil error")
	}
	if !strings.Contains(err.Error(), "ipc:") {
		t.Errorf("error %q does not have expected prefix 'ipc:'", err)
	}
}

func TestListen_SocketPermissions(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission check not reliable as root")
	}
	sockPath := tempSockPath(t, "perms")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = Listen(ctx, sockPath, func(conn net.Conn) { conn.Close() }) }()

	if err := waitForSocket(sockPath, 2*time.Second); err != nil {
		t.Fatalf("socket never appeared: %v", err)
	}

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		t.Errorf("socket permissions %04o are too permissive; want 0600", mode)
	}
}

// waitForSocket polls until sockPath appears or timeout elapses.
func waitForSocket(sockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return os.ErrNotExist
}
