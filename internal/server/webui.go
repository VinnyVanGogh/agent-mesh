package server

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed webui/*
var webuiFiles embed.FS

// webuiFS strips the leading "webui/" prefix so paths resolve as "/ui/style.css" etc.
var webuiFS, _ = fs.Sub(webuiFiles, "webui")

// indexTmpl is the root HTML page; it injects the local auth token so JS can
// pass it to the Bearer-protected API endpoints.
var indexTmpl = template.Must(template.ParseFS(webuiFiles, "webui/index.html"))

// RegisterUIRoutes mounts the embedded web UI onto the given mux.
//
// GET /        → index.html (with auth token injected into a meta tag)
// GET /ui/     → embedded static assets (CSS, JS)
func RegisterUIRoutes(mux *http.ServeMux, authToken string) {
	fileServer := http.FileServer(http.FS(webuiFS))

	// Serve static assets under /ui/
	mux.Handle("GET /ui/", http.StripPrefix("/ui", fileServer))

	// Root → index.html with token injected
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Only handle exact root or paths that look like SPA navigation
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/ui/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = indexTmpl.Execute(w, map[string]string{"Token": authToken})
	})
}
