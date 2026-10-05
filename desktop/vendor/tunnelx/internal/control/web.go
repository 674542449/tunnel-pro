package control

import (
	"bytes"
	"embed"
	"net/http"
	"time"
)

//go:embed web/*
var assets embed.FS

func (a *API) web(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	path := r.URL.Path
	if path == "/" || path == "/admin" {
		path = "/index.html"
	}
	b, e := assets.ReadFile("web" + path)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	// Serve one script so restored sessions cannot render before commerce and
	// account-security handlers have loaded over a slower second request.
	if path == "/app.js" {
		commerce, err := assets.ReadFile("web/commerce.js")
		if err != nil {
			http.Error(w, "Application unavailable", http.StatusServiceUnavailable)
			return
		}
		b = append(append(b, '\n'), commerce...)
	}
	http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(b))
}
