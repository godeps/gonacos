package web

import (
	_ "embed"
	"net/http"
)

//go:embed console.html
var consoleHTML []byte

// ConsoleHandler serves the embedded single-page console. The HTML is a
// zero-build vanilla JS application that talks to the /v3/** HTTP API.
func ConsoleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(consoleHTML)
	}
}
