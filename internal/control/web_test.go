package control

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedConsoleHasNoRedirectLoop(t *testing.T) {
	for _, path := range []string{"/", "/admin", "/app.js", "/style.css", "/icons.svg", "/network.svg", "/mark.svg"} {
		a := &API{}
		r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("console asset %s returned %d", path, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing console CSP")
		}
		if strings.HasSuffix(path, ".svg") && !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") {
			t.Fatal("SVG asset has incorrect content type")
		}
	}
}
