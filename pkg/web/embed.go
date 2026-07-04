package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed console.html
var consoleHTML []byte

//go:embed console-ui/dist
var consoleUIDist embed.FS

// consoleUIFS is the fs.Sub tree rooted at console-ui/dist. It is resolved at
// init time so embedding failures surface immediately at startup.
var consoleUIFS, _ = fs.Sub(consoleUIDist, "console-ui/dist")

// ConsoleHandler serves the embedded single-page console. The HTML is a
// zero-build vanilla JS application that talks to the /v3/** HTTP API.
// Retained at /v3/console/ui/legacy for backward compatibility with callers
// that have not yet migrated to the React SPA.
func ConsoleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(consoleHTML)
	}
}

// SpaHandler serves the embedded React SPA built from pkg/web/console-ui.
// Files are served from console-ui/dist; any path that does not match a
// real file falls back to index.html so client-side routing works on
// deep links such as /v3/console/ui/namespace.
func SpaHandler() http.HandlerFunc {
	fileServer := http.FileServer(http.FS(consoleUIFS))
	return func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/v3/console/ui")
		rel = strings.TrimPrefix(rel, "/")
		// Root path or explicit index.html → serve index.html via "/".
		// http.FileServer redirects /index.html → ./, so we use / directly.
		if rel == "" || rel == "index.html" {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for unknown paths so react-router
		// can resolve them client-side.
		if _, err := fs.Stat(consoleUIFS, rel); err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/" + path.Clean(rel)
		fileServer.ServeHTTP(w, r)
	}
}
