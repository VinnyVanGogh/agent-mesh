package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server is the StayPoint local HTTP and SSE daemon server.
type Server struct {
	opts       Options
	listener   net.Listener
	httpServer *http.Server
	hub        *EventHub
	secMid     *SecurityMiddleware
	addr       string
	port       int
	mu         sync.Mutex
	running    bool
}

// New creates and configures a new Server instance.
func New(opts Options) (*Server, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	hub := opts.Hub
	if hub == nil {
		hub = NewEventHub(opts.ReplayBufferSize, opts.SubscriberBufferSize)
	}

	secMid := NewSecurityMiddleware(opts.AuthToken, opts.Port)

	s := &Server{
		opts:   opts,
		hub:    hub,
		secMid: secMid,
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	// Wrap entire mux with security middleware (DNS rebinding, Origin, CORS, Auth)
	secureHandler := secMid.Wrap(mux)

	s.httpServer = &http.Server{
		Handler:      secureHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Zero for SSE streaming
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// SSE endpoint
	mux.HandleFunc("GET /api/events", s.hub.HandleSSE())
	mux.HandleFunc("GET /api/sse", s.hub.HandleSSE())

	// Health check (within security wrapper)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":"1.0"}`)
	})

	// Tasks REST API
	if s.opts.DB != nil {
		tasksH := NewTasksHandler(s.opts.DB, s.hub)
		mux.HandleFunc("GET /api/tasks", tasksH.ListTasks)
		mux.HandleFunc("POST /api/tasks", tasksH.CreateTask)
		mux.HandleFunc("GET /api/tasks/{id}", tasksH.GetTask)
		mux.HandleFunc("GET /api/tasks/{id}/comments", tasksH.GetComments)
		mux.HandleFunc("POST /api/tasks/{id}/comments", tasksH.AddComment)
		mux.HandleFunc("POST /api/tasks/{id}/done", tasksH.MarkDone)
		mux.HandleFunc("POST /api/tasks/{id}/block", tasksH.BlockTask)
		mux.HandleFunc("POST /api/tasks/{id}/unblock", tasksH.UnblockTask)
		mux.HandleFunc("POST /api/tasks/{id}/stage", tasksH.SetStage)

		// Threads REST API
		threadsH := NewThreadsHandler(s.opts.DB, s.hub)
		mux.HandleFunc("GET /api/threads", threadsH.ListThreads)
		mux.HandleFunc("POST /api/threads", threadsH.CreateThread)
		mux.HandleFunc("GET /api/threads/{id}", threadsH.GetThread)
		mux.HandleFunc("GET /api/threads/{id}/messages", threadsH.GetMessages)
		mux.HandleFunc("POST /api/threads/{id}/messages", threadsH.AppendMessage)

		// Sessions REST API
		sessionsH := NewSessionsHandler(s.opts.DB, s.hub)
		mux.HandleFunc("GET /api/sessions", sessionsH.ListSessions)
		mux.HandleFunc("POST /api/sessions", sessionsH.RegisterSession)
		mux.HandleFunc("GET /api/sessions/{id}", sessionsH.GetSession)
		mux.HandleFunc("POST /api/sessions/{id}/heartbeat", sessionsH.Heartbeat)
		mux.HandleFunc("POST /api/sessions/{id}/close", sessionsH.CloseSession)

		// Telemetry & Fleet REST API
		telemetryH := NewTelemetryHandler(s.opts.DB, s.hub, s.opts.TelemetryDBPath)
		mux.HandleFunc("GET /api/telemetry", telemetryH.GetTelemetry)
		mux.HandleFunc("GET /api/fleet/overview", telemetryH.GetFleetOverview)
	}

	// Embedded web UI (must be registered last so /api/* patterns take precedence)
	RegisterUIRoutes(mux, s.opts.AuthToken)
}

// Start binds to 127.0.0.1 and starts serving requests in the background.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server already running")
	}

	// Strictly verify loopback bind address
	if s.opts.BindHost != "127.0.0.1" {
		return fmt.Errorf("%w: attempted to bind %q", ErrNonLoopbackBind, s.opts.BindHost)
	}

	addr := fmt.Sprintf("%s:%d", s.opts.BindHost, s.opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = ln
	s.addr = ln.Addr().String()

	// Extract the actual port if dynamically assigned (port 0)
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		s.port = tcpAddr.Port
		s.secMid.SetPort(s.port)
	}

	s.running = true

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			// server closed unexpectedly
		}
	}()

	return nil
}

// Shutdown gracefully terminates the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the network address the server is listening on.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Port returns the TCP port the server is listening on.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// URL returns the base URL of the running server (e.g. http://127.0.0.1:41421).
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Port())
}

// Token returns the authentication token required by the server.
func (s *Server) Token() string {
	return s.opts.AuthToken
}

// Hub returns the server's EventHub for publishing events.
func (s *Server) Hub() *EventHub {
	return s.hub
}
