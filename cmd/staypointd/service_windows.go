//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "staypointd"
const serviceDisplayName = "Staypoint Background Daemon"
const serviceDescription = "AI agent ops daemon: quota telemetry, named-pipe IPC, and file watcher."

// staypointService implements svc.Handler so the SCM can start/stop the daemon.
type staypointService struct {
	runDaemon func(ctx context.Context) error
}

func (s *staypointService) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.runDaemon(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case err := <-done:
			if err != nil {
				slog.Error("daemon exited with error", slog.Any("error", err))
			}
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			default:
				slog.Warn("unexpected service control", slog.Uint64("cmd", uint64(c.Cmd)))
			}
		}
	}
}

// runAsService runs staypointd under the Windows Service Control Manager.
func runAsService(runDaemon func(ctx context.Context) error) error {
	elog, err := eventlog.Open(serviceName)
	if err != nil {
		// Event log not available (e.g. service not registered); continue without it.
		elog = nil
	}
	if elog != nil {
		defer elog.Close()
		_ = elog.Info(1, "Staypoint service starting")
	}
	return svc.Run(serviceName, &staypointService{runDaemon: runDaemon})
}

// isWindowsService reports whether the process is running under the SCM.
func isWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

// installService registers staypointd with the Windows Service Control Manager
// using the given executable path.
func installService(exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service install: connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", serviceName)
	}

	s, err = m.CreateService(serviceName, exePath,
		mgr.Config{
			DisplayName: serviceDisplayName,
			Description: serviceDescription,
			StartType:   mgr.StartAutomatic,
		},
		"--service",
	)
	if err != nil {
		return fmt.Errorf("service install: CreateService: %w", err)
	}
	defer s.Close()

	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		// Non-fatal: event log source registration may require elevation.
		slog.Warn("event log source not registered", slog.Any("error", err))
	}
	fmt.Printf("Service %q installed. Start with: sc start %s\n", serviceName, serviceName)
	return nil
}

// removeService unregisters staypointd from the Windows Service Control Manager.
func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service remove: connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("service remove: Delete: %w", err)
	}
	_ = eventlog.Remove(serviceName)
	fmt.Printf("Service %q removed.\n", serviceName)
	return nil
}
