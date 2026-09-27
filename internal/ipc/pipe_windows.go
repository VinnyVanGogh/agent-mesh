//go:build windows

// Package ipc provides Windows named pipe transport for staypointd IPC.
package ipc

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/windows"
)

const (
	pipeName         = `\\.\pipe\staypointd`
	pipeBufferSize   = 65536
	maxPipeInstances = 10
)

// SocketPath returns the named pipe path for staypointd on Windows.
func SocketPath() string {
	return pipeName
}

// pipeConn wraps a Windows HANDLE as a net.Conn using synchronous byte-stream IO.
type pipeConn struct {
	handle windows.Handle
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return pipeName }

func (c *pipeConn) Read(b []byte) (int, error) {
	var n uint32
	if err := windows.ReadFile(c.handle, b, &n, nil); err != nil {
		return 0, &net.OpError{Op: "read", Net: "pipe", Err: err}
	}
	return int(n), nil
}

func (c *pipeConn) Write(b []byte) (int, error) {
	var n uint32
	if err := windows.WriteFile(c.handle, b, &n, nil); err != nil {
		return 0, &net.OpError{Op: "write", Net: "pipe", Err: err}
	}
	return int(n), nil
}

func (c *pipeConn) Close() error               { return windows.CloseHandle(c.handle) }
func (c *pipeConn) LocalAddr() net.Addr        { return pipeAddr{} }
func (c *pipeConn) RemoteAddr() net.Addr       { return pipeAddr{} }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }

func newPipeInstance() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return windows.InvalidHandle, err
	}
	h, err := windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		maxPipeInstances,
		pipeBufferSize,
		pipeBufferSize,
		0, // default timeout (50 ms)
		nil,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("ipc: CreateNamedPipe: %w", err)
	}
	return h, nil
}

// Listen creates a named pipe server and calls handleConn for each client connection.
// The socketPath argument is ignored; the pipe name is always pipeName.
func Listen(ctx context.Context, _ string, handleConn func(net.Conn)) error {
	for {
		h, err := newPipeInstance()
		if err != nil {
			return err
		}

		// ConnectNamedPipe blocks until a client connects; run it off the main goroutine
		// so we can honour context cancellation.
		connErr := make(chan error, 1)
		go func() { connErr <- windows.ConnectNamedPipe(h, nil) }()

		select {
		case <-ctx.Done():
			_ = windows.CloseHandle(h)
			return ctx.Err()
		case err := <-connErr:
			// ERROR_PIPE_CONNECTED means the client connected before ConnectNamedPipe was called.
			if err != nil && err != windows.ERROR_PIPE_CONNECTED {
				_ = windows.CloseHandle(h)
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return fmt.Errorf("ipc: ConnectNamedPipe: %w", err)
				}
			}
			go handleConn(&pipeConn{handle: h})
		}
	}
}

// Dial connects to the staypointd named pipe.
// The socketPath argument is ignored; the pipe name is always pipeName.
func Dial(_ string) (net.Conn, error) {
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial: %w", err)
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial %s: %w (is staypointd running?)", pipeName, err)
	}
	mode := uint32(windows.PIPE_READMODE_BYTE)
	if err := windows.SetNamedPipeHandleState(h, &mode, nil, nil); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("ipc: SetNamedPipeHandleState: %w", err)
	}
	return &pipeConn{handle: h}, nil
}
