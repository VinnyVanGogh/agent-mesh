package bridge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/config"
)

// TunnelOptions configures dev server port forwarding over SSH.
type TunnelOptions struct {
	Host        string
	RemotePort  int
	LocalPort   int
	OpenBrowser bool
}

// ProbeRemotePortFunc allows mocking remote port checking in unit tests.
var ProbeRemotePortFunc = ProbeRemotePort

// OpenBrowserFunc allows mocking browser launch in unit tests.
var OpenBrowserFunc = OpenBrowserURL

// StartSSHPortForwardFunc allows mocking SSH execution in unit tests.
var StartSSHPortForwardFunc = RunSSHPortForward

// ProbeRemotePort checks whether a service is actively listening on remotePort on host via SSH.
// It tries `nc -z 127.0.0.1 <port>` or `lsof -i :<port>`.
// Returns true if listening, false otherwise.
func ProbeRemotePort(ctx context.Context, host string, port int) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmdStr := fmt.Sprintf("nc -z 127.0.0.1 %d 2>/dev/null || lsof -i :%d >/dev/null 2>&1", port, port)
	cmd := exec.CommandContext(probeCtx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=2",
		"-o", "StrictHostKeyChecking=accept-new",
		host,
		cmdStr,
	)
	return cmd.Run() == nil
}

// OpenBrowserURL opens the specified URL in the default browser using macOS `open`.
func OpenBrowserURL(url string) error {
	cmd := exec.Command("open", url)
	return cmd.Run()
}

// RunSSHPortForward starts the SSH tunnel command: `ssh -N -L <localPort>:127.0.0.1:<remotePort> <host>`
// It blocks until the command exits or ctx is cancelled.
func RunSSHPortForward(ctx context.Context, host string, localPort, remotePort int) error {
	args := []string{
		"-N",
		"-L", fmt.Sprintf("%d:127.0.0.1:%d", localPort, remotePort),
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		host,
	}

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// ValidatePort verifies that a port number is within the valid TCP range (1-65535).
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}
	return nil
}

// StartTunnel forwards a remote port on host to a local port over SSH.
// If localPort <= 0, it defaults to remotePort.
// It probes whether remote server is actively listening or waiting for launch,
// opens the browser if openBrowser is true,
// displays the Tokyo Night status banner,
// and gracefully shuts down on SIGINT/SIGTERM or context cancellation.
func StartTunnel(ctx context.Context, host string, remotePort int, localPort int, openBrowser bool) error {
	if localPort <= 0 {
		localPort = remotePort
	}

	if err := ValidatePort(remotePort); err != nil {
		return fmt.Errorf("remote port error: %w", err)
	}
	if err := ValidatePort(localPort); err != nil {
		return fmt.Errorf("local port error: %w", err)
	}

	if host == "" {
		cfg, _ := config.LoadConfig()
		if cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		} else {
			host = DefaultRemoteHost
		}
	}

	// Probe remote host SSH connectivity
	probe := ProbeSSHFunc(ctx, host, DefaultSSHTimeout)
	if !probe.Reachable {
		return fmt.Errorf("remote host %s unreachable: %s", host, probe.Error)
	}

	// Probe remote port listening status
	isListening := ProbeRemotePortFunc(ctx, host, remotePort)
	if isListening {
		fmt.Printf("\033[38;2;158;206;106m✔ [bridge]\033[0m Remote service detected listening on %s:%d\n", host, remotePort)
	} else {
		fmt.Printf("\033[38;2;224;175;104m⚡ [bridge]\033[0m Remote port %d not yet listening on %s (waiting for dev server launch)...\n", remotePort, host)
	}

	// Tokyo Night status banner
	// ⚡ [bridge] Tunnel active: %s:%d -> http://localhost:%d (Ctrl+C to stop)
	fmt.Printf("\033[38;2;125;207;255m⚡ [bridge]\033[0m \033[1mTunnel active:\033[0m \033[38;2;187;154;247m%s:%d\033[0m \033[38;2;86;95;137m->\033[0m \033[38;2;115;218;202mhttp://localhost:%d\033[0m \033[38;2;86;95;137m(Ctrl+C to stop)\033[0m\n",
		host, remotePort, localPort)

	// Context for signal handling and forward lifecycle
	tunnelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			fmt.Printf("\n\033[38;2;224;175;104m⚡ [bridge]\033[0m Closing tunnel %s:%d -> localhost:%d...\n", host, remotePort, localPort)
			cancel()
		case <-tunnelCtx.Done():
		}
	}()

	// Open browser if requested
	if openBrowser {
		targetURL := fmt.Sprintf("http://localhost:%d", localPort)
		go func() {
			// Short delay to give SSH tunnel listener a moment to bind
			time.Sleep(300 * time.Millisecond)
			if err := OpenBrowserFunc(targetURL); err != nil {
				fmt.Fprintf(os.Stderr, "\033[38;2;247;118;142m✖ [bridge] Failed to open browser:\033[0m %v\n", err)
			}
		}()
	}

	err := StartSSHPortForwardFunc(tunnelCtx, host, localPort, remotePort)
	if err != nil && tunnelCtx.Err() != context.Canceled {
		return fmt.Errorf("ssh tunnel error: %w", err)
	}

	return nil
}
