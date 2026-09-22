package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateSessionToken creates a cryptographically secure random token.
func GenerateSessionToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// StartReverseBridgeServer runs a local loopback HTTP server that securely serves
// requested files to the remote node over the SSH reverse tunnel.
func StartReverseBridgeServer(clientHome string, token string, preferredPort int) (int, func(), error) {
	if preferredPort <= 0 {
		preferredPort = 4119
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
	if err != nil {
		// Fallback to any free port
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, nil, fmt.Errorf("failed to bind loopback listener: %w", err)
		}
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	// File fetch endpoint
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, r *http.Request) {
		reqToken := r.Header.Get("X-Mesh-Token")
		if reqToken == "" {
			reqToken = r.URL.Query().Get("token")
		}
		if reqToken != token {
			http.Error(w, "Unauthorized: invalid session token", http.StatusUnauthorized)
			return
		}

		rawPath := r.URL.Query().Get("path")
		if rawPath == "" {
			http.Error(w, "Missing path parameter", http.StatusBadRequest)
			return
		}

		cleanPath := filepath.Clean(rawPath)

		// Security constraint: Path must reside under clientHome or system temp directory
		cleanHome := filepath.Clean(clientHome)
		cleanTmp := filepath.Clean(os.TempDir())
		isUnderHome := strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator)) || cleanPath == cleanHome
		isUnderTmp := strings.HasPrefix(cleanPath, cleanTmp+string(filepath.Separator)) || strings.HasPrefix(cleanPath, "/tmp/")

		if !isUnderHome && !isUnderTmp {
			http.Error(w, "Forbidden: path outside client home or temporary storage", http.StatusForbidden)
			return
		}

		info, err := os.Stat(cleanPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("File not found: %v", err), http.StatusNotFound)
			return
		}
		if info.IsDir() {
			http.Error(w, "Cannot fetch directories directly", http.StatusBadRequest)
			return
		}

		http.ServeFile(w, r, cleanPath)
	})

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
	}

	return actualPort, shutdown, nil
}
