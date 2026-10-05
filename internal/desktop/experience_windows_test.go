//go:build windows

package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/net/http2"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
	"tunnelx/internal/diagnostics"
	"tunnelx/internal/pki"
)

func responsiveStatus(t *testing.T, e *Engine) map[string]any {
	t.Helper()
	done := make(chan map[string]any, 1)
	go func() { done <- e.Status() }()
	select {
	case status := <-done:
		return status
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Status blocked behind network I/O")
		return nil
	}
}
func isolatedListenerSettings(t *testing.T, api string) Settings {
	t.Helper()
	socks, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpListener.Close()
	return Settings{APIURL: api, SOCKS: socks.Addr().String(), HTTP: httpListener.Addr().String()}
}
func TestConnectProfileWaitCanBeObservedAndCancelled(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	e, err := New(t.TempDir(), Settings{APIURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	e.login.Token = "test-session"
	done := make(chan error, 1)
	go func() { done <- e.Connect(strings.Repeat("a", 32), false) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("profile request not started")
	}
	state := responsiveStatus(t, e)
	if state["connection_state"] != "connecting" || state["connection_stage"] != "fetching_profile" {
		t.Fatal(state)
	}
	if err = e.Connect(strings.Repeat("b", 32), false); err == nil {
		t.Fatal("second connection was accepted")
	}
	if err = e.CancelConnect(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connect did not cancel")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("HTTP profile request was not cancelled")
	}
	state = responsiveStatus(t, e)
	if state["connection_state"] != "disconnected" || state["connected"] != false || state["system_proxy"] != false {
		t.Fatal(state)
	}
}
func TestStatusRemainsResponsiveDuringQueryProbeAndLogin(t *testing.T) {
	for _, operation := range []string{"query", "probe", "login"} {
		t.Run(operation, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				once.Do(func() { close(started) })
				<-release
				if r.URL.Path == "/api/login" {
					json.NewEncoder(w).Encode(map[string]string{"token": "test-session"})
				} else {
					w.WriteHeader(403)
				}
			}))
			defer server.Close()
			e, err := New(t.TempDir(), Settings{APIURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			e.login.Token = "previous-session"
			done := make(chan error, 1)
			go func() {
				var operationErr error
				switch operation {
				case "query":
					_, operationErr = e.Query("/api/me", nil)
				case "probe":
					_, operationErr = e.Probe(strings.Repeat("a", 32))
				case "login":
					operationErr = e.Login("test@example.test", "not-a-real-password", false)
				}
				done <- operationErr
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request not started")
			}
			responsiveStatus(t, e)
			if err = e.CancelConnect(); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("operation did not finish")
			}
		})
	}
}
func TestDisconnectCancelsOutstandingConnect(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: server.URL})
	done := make(chan error, 1)
	go func() { done <- e.Connect(strings.Repeat("a", 32), false) }()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- e.Disconnect() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect waited for the profile timeout")
	}
	<-done
	if e.Status()["connection_state"] != "disconnected" {
		t.Fatal(e.Status())
	}
}
func TestPreferencesPersistIncludingExplicitProxyOff(t *testing.T) {
	root := t.TempDir()
	settings := Settings{APIURL: "http://127.0.0.1:1"}
	e, _ := New(root, settings)
	if !e.Status()["preferences"].(Preferences).SystemProxy {
		t.Fatal("proxy default should be on")
	}
	id := strings.Repeat("ab", 16)
	if err := e.SavePreferences(id, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(root, settings)
	if err != nil {
		t.Fatal(err)
	}
	p := restarted.Status()["preferences"].(Preferences)
	if p.SelectedNodeID != id || p.SystemProxy {
		t.Fatal(p)
	}
	if err = restarted.SavePreferences("../invalid", true); err == nil {
		t.Fatal("invalid node ID accepted")
	}
	again, _ := New(root, settings)
	if !reflect.DeepEqual(again.preferences, p) {
		t.Fatal("invalid preference replaced the previous snapshot")
	}
}
func TestPublicQueriesNeverPostOrAttachCredentials(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Errorf("public request leaked auth or changed method")
		}
		json.NewEncoder(w).Encode(map[string]bool{"registration": false, "invite_only": true})
	}))
	defer server.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: server.URL})
	e.login.Token = "private-session"
	for _, route := range []string{"/api/public/settings", "/api/public/commerce"} {
		out, err := e.Query(route, nil)
		if err != nil || out.(map[string]any)["invite_only"] != true {
			t.Fatal(out, err)
		}
		if _, err = e.Query(route, map[string]any{}); err == nil {
			t.Fatal("public POST accepted")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("rejected requests reached the network")
	}
}
func TestDamagedDPAPILoginIsPreservedAndCanBeReplaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "new-private-session"})
	}))
	defer server.Close()
	for _, format := range []string{"dpapi", "json"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			settings := Settings{APIURL: server.URL}
			e, _ := New(root, settings)
			previousDevice := e.login.DeviceID
			if err := e.Login("test@example.test", "not-a-real-password", false); err != nil {
				t.Fatal(err)
			}
			auth := filepath.Join(root, "state", "auth.dpapi")
			b, _ := os.ReadFile(auth)
			if format == "dpapi" {
				b[len(b)-1] ^= 1
			} else {
				b, _ = protect([]byte(`not-json`))
			}
			if err := os.WriteFile(auth, b, 0600); err != nil {
				t.Fatal(err)
			}
			recovered, err := New(root, settings)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.login.Token != "" || recovered.login.DeviceID == previousDevice || recovered.startupWarning == "" {
				t.Fatal("corrupt state did not reset safely")
			}
			original, _ := os.ReadFile(auth)
			if !bytes.Equal(original, b) {
				t.Fatal("New mutated shared credentials before acquiring the shell instance lock")
			}
			if err = recovered.Login("test@example.test", "not-a-real-password", false); err != nil {
				t.Fatal(err)
			}
			saved, _ := filepath.Glob(filepath.Join(root, "state", "auth-corrupt-*.dpapi"))
			if len(saved) != 1 {
				t.Fatal(saved)
			}
			again, err := New(root, settings)
			if err != nil || again.login.Token != "new-private-session" || again.login.DeviceID != recovered.login.DeviceID {
				t.Fatal("new login was not persisted", err)
			}
			original, _ = os.ReadFile(saved[0])
			if !bytes.Equal(original, b) {
				t.Fatal("successful login changed the retained corrupt file")
			}
		})
	}
}

func healthFixture(t *testing.T) (config.Client, *atomic.Int32, func()) {
	return healthFixtureHandler(t, nil)
}
func healthFixtureHandler(t *testing.T, handler func(http.ResponseWriter, *http.Request) bool) (config.Client, *atomic.Int32, func()) {
	t.Helper()
	dir := t.TempDir()
	outerCA, cert, key, err := pki.Certificate("gateway.example.test")
	if err != nil {
		t.Fatal(err)
	}
	innerCA, ic, ik, err := pki.Certificate("edge.tunnelx.invalid")
	if err != nil {
		t.Fatal(err)
	}
	ech, list, err := pki.ECH("gateway.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ek, _ := json.Marshal(ech)
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sc := config.Server{CertFile: write("cert.pem", cert), KeyFile: write("key.pem", key), ECHKeyFile: write("ech.json", ek), InnerName: "edge.tunnelx.invalid", InnerCertFile: write("inner.pem", ic), InnerKeyFile: write("inner-key.pem", ik)}
	tc, err := sc.TLS()
	if err != nil {
		t.Fatal(err)
	}
	tc.NextProtos = []string{"h2"}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var status atomic.Int32
	status.Store(200)
	srv := &http.Server{TLSConfig: tc, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "CONNECT" {
			w.Header().Set("Tunnelx-Version", config.Version)
		}
		if handler != nil && handler(w, r) {
			return
		}
		w.WriteHeader(int(status.Load()))
		io.WriteString(w, "gateway-health")
	})}
	if err = http2.ConfigureServer(srv, &http2.Server{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ServeTLS(ln, "", "") }()
	cleanup := func() { srv.Close(); <-done }
	t.Cleanup(cleanup)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	cfg := config.Client{ServerName: "edge.tunnelx.invalid", ServerIP: "127.0.0.1", Port: n, Token: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), Transport: "h2", Privacy: "strict", ECHConfig: base64.StdEncoding.EncodeToString(list), CAFile: write("roots.pem", append(outerCA, innerCA...)), ConnectTimeout: 1}
	return cfg, &status, func() { srv.Close() }
}
func TestHealthReflectsRealECHFailuresAndRecovery(t *testing.T) {
	cfg, status, _ := healthFixture(t)
	m, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
	e.mux = m
	e.healthInterval = 20 * time.Millisecond
	e.healthTimeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := e.startHealth(ctx, m)
	waitFor := func(check func(Health) bool) Health {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			h := e.Status()["health"].(Health)
			if check(h) {
				return h
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("health state not observed", e.Status())
		return Health{}
	}
	good := waitFor(func(h Health) bool { return h.LastSuccess > 0 })
	if e.Status()["connection_state"] != "connected" || !m.LastECH.Load() {
		t.Fatal("not a real strict ECH health check")
	}
	status.Store(503)
	failed := waitFor(func(h Health) bool { return h.ConsecutiveFailures > 0 })
	if failed.ErrorKind != "http_status" || failed.LastSuccess != good.LastSuccess || e.Status()["connection_state"] != "degraded" {
		t.Fatal(failed)
	}
	status.Store(200)
	waitFor(func(h Health) bool { return h.ConsecutiveFailures == 0 && h.LastSuccess >= failed.CheckedAt })
	if e.Status()["connection_state"] != "connected" {
		t.Fatal(e.Status())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health did not stop")
	}
	e.mu.Lock()
	e.mux = nil
	e.mu.Unlock()
	prior := e.health
	e.applyHealth(m, time.Now().Unix()+10, "timeout")
	if e.health != prior {
		t.Fatal("late health response replaced disconnected state")
	}
}
func TestDiagnosticsSummaryOmitsIdentitySecretsAndPrivatePaths(t *testing.T) {
	e, _ := New(t.TempDir(), Settings{APIURL: "https://secret-domain.example"})
	logger, err := diagnostics.New(filepath.Join(e.root, "private-log-directory"), diagnostics.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.Record("private-event", map[string]any{"host": "private-history.example", "token": "private-logged-token"})
	e.login.Token = "secret-token-value"
	e.login.Email = "private-user@example.test"
	e.nodeName = "secret-node-host.example"
	e.root = `C:\Users\private-owner\secret-folder`
	e.stage = "secret-stage.example"
	e.health.ErrorKind = "secret-error.example"
	e.mux = &client.Mux{}
	e.mux.SetDiagnostics(logger)
	e.mux.Stats.Downloaded.Store(2048)
	report, err := e.DiagnosticsReport()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-token-value", "private-user", "secret-domain", "secret-node", "private-owner", "secret-folder", "secret-stage", "secret-error", "auth.dpapi", "private-log-directory", "private-history", "private-logged-token"} {
		if strings.Contains(report, secret) {
			t.Fatal("diagnostic summary leaked", secret)
		}
	}
	var data map[string]any
	if err = json.Unmarshal([]byte(report), &data); err != nil {
		t.Fatal(err)
	}
	if data["download"] != float64(2048) || data["connection_stage"] != "unknown" {
		t.Fatal(data)
	}
}

func TestCancelledConnectCannotCommitAfterCancellationStarts(t *testing.T) {
	cfg, _, _ := healthFixture(t)
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	reached, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"client": cfg, "ca_pem": string(ca)})
	}))
	defer server.Close()
	e, _ := New(t.TempDir(), isolatedListenerSettings(t, server.URL))
	defer e.Close()
	done := make(chan error, 1)
	go func() { done <- e.Connect(strings.Repeat("a", 32), false) }()
	<-reached
	invoked, resume := make(chan struct{}), make(chan struct{})
	e.mu.Lock()
	cancel := e.connectCancel
	// Pause exactly where the old implementation released mu before cancel.
	e.connectCancel = func() { close(invoked); <-resume; cancel() }
	e.mu.Unlock()
	cancelDone := make(chan struct{})
	go func() { e.CancelConnect(); close(cancelDone) }()
	<-invoked
	close(release)
	select {
	case err := <-done:
		close(resume)
		<-cancelDone
		t.Fatalf("connection passed an in-progress cancellation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resume)
	<-cancelDone
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection did not unwind after cancellation")
	}
	if e.Status()["connected"] != false {
		t.Fatal("cancelled connection committed")
	}
}

func TestLateUnauthorizedTeardownCannotCloseReplacementConnection(t *testing.T) {
	cfg, _, _ := healthFixture(t)
	old, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	replacement, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: server.URL})
	e.login.Token = "old-session"
	e.mux = old
	e.lifecycle.Lock()
	done := make(chan error, 1)
	go func() { _, err := e.Query("/api/me", nil); done <- err }()
	deadline := time.Now().Add(time.Second)
	for e.Status()["logged_in"] == true && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.Status()["logged_in"] != false {
		e.lifecycle.Unlock()
		t.Fatal("401 did not invalidate original session")
	}
	// Model disconnect, login and a replacement connection completing before
	// the old request obtains the lifecycle lock to perform its teardown.
	e.mu.Lock()
	e.mux = replacement
	e.login.Token = "new-session"
	e.stage = "connected"
	e.mu.Unlock()
	e.lifecycle.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected original request rejection")
		}
	case <-time.After(time.Second):
		t.Fatal("old authorization response did not finish")
	}
	if e.Status()["connected"] != true || e.Status()["logged_in"] != true {
		t.Fatal("late teardown cleared replacement", e.Status())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if kind := probeHealth(ctx, replacement); kind != "" {
		t.Fatal("replacement transport was closed", kind)
	}
}

func TestDisconnectCancelsInFlightHealthWithoutLockDeadlock(t *testing.T) {
	reached := make(chan struct{})
	var once sync.Once
	cfg, _, _ := healthFixtureHandler(t, func(w http.ResponseWriter, r *http.Request) bool {
		once.Do(func() { close(reached) })
		<-r.Context().Done()
		return true
	})
	m, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
	e.mux = m
	e.healthInterval = time.Millisecond
	e.healthTimeout = 30 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.healthDone = e.startHealth(ctx, m)
	select {
	case <-reached:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("health request did not begin")
	}
	responsiveStatus(t, e)
	done := make(chan error, 1)
	go func() { done <- e.Disconnect() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect was blocked behind its health callback")
	}
	if e.Status()["connection_state"] != "disconnected" || e.Status()["health"].(Health).ConsecutiveFailures != 0 {
		t.Fatal(e.Status())
	}
}

func TestConnectCancelsDuringTLSVerification(t *testing.T) {
	cfg, _, _ := healthFixture(t)
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	cfg.Port, _ = strconv.Atoi(port)
	accepted, peerClosed := make(chan struct{}), make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			close(peerClosed)
			return
		}
		defer connection.Close()
		close(accepted)
		io.Copy(io.Discard, connection)
		close(peerClosed)
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"client": cfg, "ca_pem": string(ca)})
	}))
	defer server.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: server.URL})
	done := make(chan error, 1)
	go func() { done <- e.Connect(strings.Repeat("a", 32), false) }()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		e.CancelConnect()
		t.Fatal("TLS handshake did not begin")
	}
	if responsiveStatus(t, e)["connection_stage"] != "verifying_tls" {
		t.Fatal(e.Status())
	}
	e.CancelConnect()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("TLS setup was not cancellable")
	}
	select {
	case <-peerClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled TLS setup leaked its socket")
	}
}

func TestCorruptCredentialsDoNotOverwriteAnotherInstancesReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state", "auth.dpapi")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt-original"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "new-session"})
	}))
	defer server.Close()
	e, err := New(root, Settings{APIURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte("other-instance-replacement")
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Login("member@example.test", "test-password", false); err == nil {
		t.Fatal("stale instance overwrote replacement credentials")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(current, replacement) {
		t.Fatal("replacement credentials were changed")
	}
}

func TestConnectAdmissionUsesConfiguredNodePort(t *testing.T) {
	requested := make(chan string, 1)
	cfg, _, _ := healthFixtureHandler(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "CONNECT" {
			requested <- r.Host
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return true
		}
		return false
	})
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"client": cfg, "ca_pem": string(ca)})
	}))
	defer server.Close()
	e, _ := New(t.TempDir(), isolatedListenerSettings(t, server.URL))
	defer e.Close()
	if err := e.Connect(strings.Repeat("a", 32), false); err != nil {
		t.Fatal(err)
	}
	select {
	case target := <-requested:
		if target != net.JoinHostPort(cfg.ServerIP, strconv.Itoa(cfg.Port)) {
			t.Fatal("admission ignored the configured node port", target)
		}
	case <-time.After(time.Second):
		t.Fatal("admission target was not requested")
	}
}
