//go:build windows

package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEngineLateUnauthorizedCannotClearReplacementSession(t *testing.T) {
	e, err := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	e.login.Token = "original-session"
	e.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer original-session" {
			t.Fatal("request must carry the captured session")
		}
		// Inject a replacement in the transport, before the old response returns.
		// A delayed request must not clear a new login's credentials.
		e.mu.Lock()
		e.login.Token = "replacement-session"
		e.mu.Unlock()
		return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	if _, err = e.Query("/api/me", nil); err == nil {
		t.Fatal("expected the original session rejection")
	}
	if e.login.Token != "replacement-session" {
		t.Fatal("the old request cleared a replacement session")
	}
}

func TestEngineExpiredSessionReturnsToLogin(t *testing.T) {
	for _, action := range []string{"query", "connect", "probe"} {
		t.Run(action, func(t *testing.T) {
			var status atomic.Int32
			status.Store(http.StatusUnauthorized)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/login" {
					json.NewEncoder(w).Encode(map[string]string{"token": strings.Repeat("a", 43)})
					return
				}
				w.WriteHeader(int(status.Load()))
				w.Write([]byte(`{"error":"session expired"}`))
			}))
			defer server.Close()
			root := t.TempDir()
			e, err := New(root, Settings{APIURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.LoginSecure("member@example.test", "test-password", "", "", false); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "query":
				_, err = e.Query("/api/me", nil)
			case "connect":
				err = e.Connect(strings.Repeat("a", 32), false)
			case "probe":
				_, err = e.Probe(strings.Repeat("a", 32))
			}
			if err == nil || e.Status()["logged_in"] != false {
				t.Fatalf("401 must clear the current local session; logged_in=%v error=%v", e.Status()["logged_in"], err)
			}
			restarted, err := New(root, Settings{APIURL: server.URL})
			if err != nil || restarted.Status()["logged_in"] != false {
				t.Fatalf("expired credentials survived restart: %v", err)
			}
		})
	}
}

func TestEnginePermissionAndNetworkErrorsKeepSession(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, 0} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/login" {
					json.NewEncoder(w).Encode(map[string]string{"token": strings.Repeat("b", 43)})
					return
				}
				w.WriteHeader(status)
				w.Write([]byte(`{"mfa_required":true}`))
			}))
			defer server.Close()
			e, err := New(t.TempDir(), Settings{APIURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Login("member@example.test", "test-password", false); err != nil {
				t.Fatal(err)
			}
			if status == 0 {
				server.Close()
			}
			if _, err = e.Query("/api/me", nil); err == nil {
				t.Fatal("expected the HTTP/network error")
			}
			if e.Status()["logged_in"] != true {
				t.Fatal("403/MFA, throttling, server failure or network loss must not log out")
			}
		})
	}
}

func registryEngine(t *testing.T, address string) (*Engine, registry.Key) {
	t.Helper()
	keyPath := fmt.Sprintf(`Software\tunnelX-desktop-tests\%d`, time.Now().UnixNano())
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close(); registry.DeleteKey(registry.CURRENT_USER, keyPath) })
	if err = k.SetStringValue("ProxyServer", "original.example:3128"); err != nil {
		t.Fatal(err)
	}
	if err = k.SetDWordValue("ProxyEnable", 0); err != nil {
		t.Fatal(err)
	}
	e, err := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1", HTTP: address})
	if err != nil {
		t.Fatal(err)
	}
	e.system.KeyPath = keyPath
	if err = e.system.Enable(); err != nil {
		t.Fatal(err)
	}
	return e, k
}

func TestEngineCloseRecoversOrphanProxyWithoutAConnectedMux(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	e, k := registryEngine(t, address)
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	got, _, _ := k.GetStringValue("ProxyServer")
	if got != "original.example:3128" {
		t.Fatalf("orphaned system proxy was not restored: %q", got)
	}
	if _, err = os.Stat(filepath.Join(e.root, "state", "system-proxy.json")); !os.IsNotExist(err) {
		t.Fatal("recovered snapshot should be removed")
	}
}

func TestEngineCloseDoesNotRestoreAnotherLiveProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	e, k := registryEngine(t, ln.Addr().String())
	if err = e.Close(); err == nil {
		t.Fatal("must report that another proxy still owns the listener")
	}
	got, _, _ := k.GetStringValue("ProxyServer")
	if got != ln.Addr().String() || !e.system.Enabled() {
		t.Fatal("another live proxy or its recovery snapshot was modified")
	}
	ln.Close()
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	got, _, _ = k.GetStringValue("ProxyServer")
	if got != "original.example:3128" {
		t.Fatal("proxy was not recoverable after the other listener stopped")
	}
}

func TestEngineOrphanRecoveryPreservesExternalProxyChanges(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	e, k := registryEngine(t, address)
	k.SetStringValue("ProxyServer", "external.example:8080")
	if err = e.Close(); err == nil {
		t.Fatal("externally changed proxy requires explicit recovery")
	}
	got, _, _ := k.GetStringValue("ProxyServer")
	if got != "external.example:8080" || !e.system.Enabled() {
		t.Fatal("external settings or the saved recovery state was modified")
	}
}
