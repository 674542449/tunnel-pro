package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"
)

type headlessApp interface {
	Status() map[string]any
	LoginSecure(string, string, string, string, bool) error
	Logout() error
	Query(string, map[string]any) (any, error)
	Connect(string, bool) error
	Disconnect() error
	RecoverProxy() error
	SavePreferences(string, bool) error
	CancelConnect() error
	SetAPI(string) error
	Probe(string) (int64, error)
	DiagnosticsReport() (string, error)
}

func headlessHandler(app headlessApp, addr, csrf string, stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != addr {
			http.Error(w, "Forbidden", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+csrf+"'")
		if r.Method == "GET" && r.URL.Path == "/api/status" {
			json.NewEncoder(w).Encode(app.Status())
			return
		}
		if r.Method != "POST" || r.Header.Get("Origin") != "http://"+addr || r.Header.Get("X-CSRF-Token") != csrf {
			http.Error(w, "Forbidden", 403)
			return
		}
		var body struct {
			Email         string         `json:"email"`
			Password      string         `json:"password"`
			Code          string         `json:"code"`
			Invite        string         `json:"invite"`
			Register      bool           `json:"register"`
			NodeID        string         `json:"node_id"`
			SystemProxy   bool           `json:"system_proxy"`
			ProxyMode     string         `json:"proxy_mode"`
			DirectDomains string         `json:"direct_domains"`
			Path          string         `json:"path"`
			APIURL        string         `json:"api_url"`
			Body          map[string]any `json:"body"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Bad Request", 400)
			return
		}
		var err error
		var out any = map[string]bool{"ok": true}
		switch r.URL.Path {
		case "/api/login":
			err = app.LoginSecure(body.Email, body.Password, body.Code, body.Invite, body.Register)
		case "/api/logout":
			err = app.Logout()
		case "/api/query":
			out, err = app.Query(body.Path, body.Body)
		case "/api/connect":
			err = app.Connect(body.NodeID, body.SystemProxy)
		case "/api/disconnect":
			err = app.Disconnect()
		case "/api/recover-proxy":
			err = app.RecoverProxy()
		case "/api/preferences":
			err = app.SavePreferences(body.NodeID, body.SystemProxy)
		case "/api/proxy-mode":
			if a, ok := app.(interface{ SaveProxyMode(string, string) error }); ok {
				err = a.SaveProxyMode(body.ProxyMode, body.DirectDomains)
			} else {
				err = fmt.Errorf("proxy modes unavailable")
			}
		case "/api/routing/update":
			if a, ok := app.(interface{ UpdateRoutingRules() error }); ok {
				err = a.UpdateRoutingRules()
			} else {
				err = fmt.Errorf("routing update unavailable")
			}
		case "/api/cancel":
			err = app.CancelConnect()
		case "/api/settings":
			err = app.SetAPI(body.APIURL)
		case "/api/probe":
			var ms int64
			ms, err = app.Probe(body.NodeID)
			out = map[string]int64{"latency_ms": ms}
		case "/api/diagnostics":
			var report string
			report, err = app.DiagnosticsReport()
			out = map[string]string{"report": report}
		case "/api/exit":
			err = app.Disconnect()
			if err == nil {
				time.AfterFunc(100*time.Millisecond, stop)
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(out)
	})
}

func serveHeadless(app headlessApp, addr string) error {
	if addr == "" {
		addr = "127.0.0.1:9080"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("invalid acceptance listener")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	addr = listener.Addr().String()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	h := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 30 * time.Second}
	app.RecoverProxy()
	h.Handler = headlessHandler(app, addr, rand.Text(), stop)
	go func() { <-ctx.Done(); h.Close() }()
	if err = h.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
