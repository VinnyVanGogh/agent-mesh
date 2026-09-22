package bridge

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidatePort(t *testing.T) {
	tests := []struct {
		port    int
		wantErr bool
	}{
		{0, true},
		{-1, true},
		{1, false},
		{80, false},
		{3000, false},
		{5173, false},
		{65535, false},
		{65536, true},
		{99999, true},
	}

	for _, tt := range tests {
		err := ValidatePort(tt.port)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePort(%d) error = %v, wantErr = %v", tt.port, err, tt.wantErr)
		}
	}
}

func TestStartTunnel_PortDefaultsAndValidation(t *testing.T) {
	origProbeSSH := ProbeSSHFunc
	origProbePort := ProbeRemotePortFunc
	origSSH := StartSSHPortForwardFunc
	origBrowser := OpenBrowserFunc
	defer func() {
		ProbeSSHFunc = origProbeSSH
		ProbeRemotePortFunc = origProbePort
		StartSSHPortForwardFunc = origSSH
		OpenBrowserFunc = origBrowser
	}()

	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{Host: host, Reachable: true, Latency: 10 * time.Millisecond}
	}

	// 1. Invalid remote port
	ctx := context.Background()
	err := StartTunnel(ctx, "mock-host", -5, 0, false)
	if err == nil || !strings.Contains(err.Error(), "remote port error") {
		t.Fatalf("expected remote port error, got: %v", err)
	}

	// 2. Invalid remote port > 65535
	err = StartTunnel(ctx, "mock-host", 70000, 0, false)
	if err == nil || !strings.Contains(err.Error(), "remote port error") {
		t.Fatalf("expected remote port error, got: %v", err)
	}

	// 3. Invalid local port > 65535 when specified
	err = StartTunnel(ctx, "mock-host", 3000, 70000, false)
	if err == nil || !strings.Contains(err.Error(), "local port error") {
		t.Fatalf("expected local port error, got: %v", err)
	}

	// 4. Default localPort = remotePort when localPort <= 0
	var forwardedLocalPort, forwardedRemotePort int
	var forwardedHost string
	StartSSHPortForwardFunc = func(ctx context.Context, host string, localPort, remotePort int) error {
		forwardedHost = host
		forwardedLocalPort = localPort
		forwardedRemotePort = remotePort
		return nil
	}
	ProbeRemotePortFunc = func(ctx context.Context, host string, port int) bool {
		return true
	}

	err = StartTunnel(ctx, "custom-host", 3000, 0, false)
	if err != nil {
		t.Fatalf("StartTunnel failed: %v", err)
	}
	if forwardedHost != "custom-host" {
		t.Errorf("expected host custom-host, got %s", forwardedHost)
	}
	if forwardedLocalPort != 3000 || forwardedRemotePort != 3000 {
		t.Errorf("expected 3000 -> 3000, got %d -> %d", forwardedLocalPort, forwardedRemotePort)
	}
}

func TestStartTunnel_HostUnreachable(t *testing.T) {
	origProbeSSH := ProbeSSHFunc
	defer func() { ProbeSSHFunc = origProbeSSH }()

	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{Host: host, Reachable: false, Error: "connection timed out"}
	}

	err := StartTunnel(context.Background(), "unreachable-host", 8080, 8080, false)
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected unreachable error, got: %v", err)
	}
}

func TestStartTunnel_ListeningAndBrowserOpen(t *testing.T) {
	origProbeSSH := ProbeSSHFunc
	origProbePort := ProbeRemotePortFunc
	origSSH := StartSSHPortForwardFunc
	origBrowser := OpenBrowserFunc
	defer func() {
		ProbeSSHFunc = origProbeSSH
		ProbeRemotePortFunc = origProbePort
		StartSSHPortForwardFunc = origSSH
		OpenBrowserFunc = origBrowser
	}()

	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{Host: host, Reachable: true, Latency: 5 * time.Millisecond}
	}

	var probedPort int
	ProbeRemotePortFunc = func(ctx context.Context, host string, port int) bool {
		probedPort = port
		return false // test branch where port is not yet listening
	}

	var openedURL string
	var browserCalled atomic.Bool
	OpenBrowserFunc = func(url string) error {
		openedURL = url
		browserCalled.Store(true)
		return nil
	}

	StartSSHPortForwardFunc = func(ctx context.Context, host string, localPort, remotePort int) error {
		// Wait for browser call goroutine to trigger
		time.Sleep(350 * time.Millisecond)
		return nil
	}

	err := StartTunnel(context.Background(), "mock-box", 5173, 5173, true)
	if err != nil {
		t.Fatalf("StartTunnel error: %v", err)
	}

	if probedPort != 5173 {
		t.Errorf("expected probed port 5173, got %d", probedPort)
	}
	if !browserCalled.Load() {
		t.Errorf("expected browser to be opened")
	}
	if openedURL != "http://localhost:5173" {
		t.Errorf("expected url http://localhost:5173, got %s", openedURL)
	}
}

func TestStartTunnel_ContextCancellation(t *testing.T) {
	origProbeSSH := ProbeSSHFunc
	origProbePort := ProbeRemotePortFunc
	origSSH := StartSSHPortForwardFunc
	defer func() {
		ProbeSSHFunc = origProbeSSH
		ProbeRemotePortFunc = origProbePort
		StartSSHPortForwardFunc = origSSH
	}()

	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{Host: host, Reachable: true}
	}
	ProbeRemotePortFunc = func(ctx context.Context, host string, port int) bool {
		return true
	}

	StartSSHPortForwardFunc = func(ctx context.Context, host string, localPort, remotePort int) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := StartTunnel(ctx, "mock-box", 3000, 3000, false)
	if err != nil {
		t.Fatalf("expected clean exit on context cancel, got error: %v", err)
	}
}

func TestStartTunnel_SSHError(t *testing.T) {
	origProbeSSH := ProbeSSHFunc
	origProbePort := ProbeRemotePortFunc
	origSSH := StartSSHPortForwardFunc
	defer func() {
		ProbeSSHFunc = origProbeSSH
		ProbeRemotePortFunc = origProbePort
		StartSSHPortForwardFunc = origSSH
	}()

	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{Host: host, Reachable: true}
	}
	ProbeRemotePortFunc = func(ctx context.Context, host string, port int) bool {
		return true
	}

	StartSSHPortForwardFunc = func(ctx context.Context, host string, localPort, remotePort int) error {
		return errors.New("port already in use locally")
	}

	err := StartTunnel(context.Background(), "mock-box", 3000, 3000, false)
	if err == nil || !strings.Contains(err.Error(), "port already in use") {
		t.Fatalf("expected port in use error, got: %v", err)
	}
}
