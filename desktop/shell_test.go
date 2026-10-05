package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shellFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestShellFirstStartDefaultsAndAppData(t *testing.T) {
	base := t.TempDir()
	exe, local := filepath.Join(base, "program"), filepath.Join(base, "local")
	paths, err := resolveDesktopPaths(exe, local, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != filepath.Join(local, "tunnelXDesktop") || paths.Mode != "appdata" {
		t.Fatal("new user does not use LocalAppData")
	}
	settings, err := readDesktopSettings(paths)
	if err != nil || settings.APIURL != defaultDesktopAPI {
		t.Fatalf("missing initial settings prevented launch: %v", err)
	}
	if _, err = os.Stat(paths.Root); !os.IsNotExist(err) {
		t.Fatal("path selection unexpectedly created or copied user state")
	}
	shellFixtureFile(t, filepath.Join(exe, "settings.json"), `{"api_url":"https://bundled.example.test/control"}`)
	paths, err = resolveDesktopPaths(exe, local, "", "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err = readDesktopSettings(paths)
	if err != nil || settings.APIURL != "https://bundled.example.test/control" || paths.Root != filepath.Join(local, "tunnelXDesktop") {
		t.Fatal("bundled config forced portable storage or was lost")
	}
	shellFixtureFile(t, filepath.Join(paths.Root, "settings.json"), `{"api_url":"https://saved.example.test/control"}`)
	paths, err = resolveDesktopPaths(exe, local, "", "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err = readDesktopSettings(paths)
	if err != nil || settings.APIURL != "https://saved.example.test/control" {
		t.Fatal("persistent settings did not override bundled defaults")
	}
}

func TestShellLegacyCompatibilityAndExplicitIsolation(t *testing.T) {
	base := t.TempDir()
	exe, local, isolated := filepath.Join(base, "legacy"), filepath.Join(base, "local"), filepath.Join(base, "isolated")
	shellFixtureFile(t, filepath.Join(exe, "state", "auth.dpapi"), "not real credentials")
	shellFixtureFile(t, filepath.Join(exe, "settings.json"), `{"api_url":"https://legacy.example.test/control"}`)
	paths, err := resolveDesktopPaths(exe, local, "", "")
	if err != nil || paths.Root != exe || paths.Mode != "legacy-portable" {
		t.Fatal("legacy data was silently moved or ignored")
	}
	settings, err := readDesktopSettings(paths)
	if err != nil || settings.APIURL != "https://legacy.example.test/control" {
		t.Fatal("legacy configuration was not selected")
	}
	// The explicit isolated path must not read even a deliberately malformed
	// neighboring legacy configuration or import its state.
	shellFixtureFile(t, filepath.Join(exe, "settings.json"), "malformed legacy config")
	paths, err = resolveDesktopPaths(exe, local, "", isolated)
	if err != nil || paths.Root != isolated || paths.Mode != "portable" {
		t.Fatal("explicit portable root not selected")
	}
	settings, err = readDesktopSettings(paths)
	if err != nil || settings.APIURL != defaultDesktopAPI {
		t.Fatal("isolated start consulted legacy files")
	}
	if _, err = os.Stat(filepath.Join(isolated, "state", "auth.dpapi")); !os.IsNotExist(err) {
		t.Fatal("legacy credentials copied into isolated state")
	}
	custom := filepath.Join(base, "custom.json")
	shellFixtureFile(t, custom, `{"api_url":"http://127.0.0.1:19081"}`)
	paths, err = resolveDesktopPaths(exe, local, custom, isolated)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = readDesktopSettings(paths)
	if err != nil || settings.APIURL != "http://127.0.0.1:19081" || paths.Root != isolated {
		t.Fatal("explicit isolated configuration ignored")
	}
}

func TestShellInvalidConfigFailsClearly(t *testing.T) {
	base := t.TempDir()
	for _, body := range []string{`broken`, `{"api_url":"javascript:alert(1)"}`, `{"api_url":"https://user:pass@example.test"}`, `{"unknown_option":true}`} {
		path := filepath.Join(base, "settings.json")
		shellFixtureFile(t, path, body)
		if _, err := readDesktopSettings(desktopPaths{Config: path, ExplicitConfig: true}); err == nil {
			t.Fatalf("invalid settings accepted: %s", body)
		}
	}
	if _, err := readDesktopSettings(desktopPaths{Config: filepath.Join(base, "missing.json"), ExplicitConfig: true}); err == nil {
		t.Fatal("explicit missing config was silently ignored")
	}
	if _, err := resolveDesktopPaths(filepath.Join(base, "clean"), "", "", ""); err == nil {
		t.Fatal("unavailable LocalAppData fell back to an arbitrary writable directory")
	}
	message := startupFailureMessage(errors.New("isolated startup error"))
	if !strings.Contains(message, "isolated startup error") || !strings.Contains(message, webView2DownloadURL) || !strings.Contains(message, "Evergreen Runtime") {
		t.Fatal("startup error lost actionable runtime instructions")
	}
}

func TestShellDownloadOnlyOpensHTTPS(t *testing.T) {
	for _, url := range []string{"javascript:alert(1)", "file:///C:/test.exe", "http://example.test/file.zip", "https://user:pass@example.test/file.zip", "//example.test/file.zip", "https://", "https://example.test/\nfile.zip"} {
		if _, err := releaseDownloadURL(map[string]any{"url": url}); err == nil {
			t.Fatalf("unsafe download URL accepted: %q", url)
		}
	}
	for _, value := range []any{nil, "https://example.test/file.zip", map[string]any{"url": 42}, map[string]any{}} {
		if _, err := releaseDownloadURL(value); err == nil {
			t.Fatal("malformed release response accepted")
		}
	}
	url := "https://downloads.example.test/tunnelX.zip?signature=public-fixture"
	if got, err := releaseDownloadURL(map[string]any{"url": url}); err != nil || got != url {
		t.Fatal("valid HTTPS download was rejected")
	}
}

func TestShellPublicReleaseDownloadExtraction(t *testing.T) {
	want := "https://downloads.example.test/tunnelX.zip"
	if got, err := publicReleaseDownloadURL(map[string]any{"release": map[string]any{"url": want}}); err != nil || got != want {
		t.Fatal("public release could not be downloaded without an account", got, err)
	}
	for _, value := range []any{nil, "not an object", map[string]any{}, map[string]any{"release": "not a release"}, map[string]any{"release": map[string]any{"url": "javascript:alert(1)"}}} {
		if _, err := publicReleaseDownloadURL(value); err == nil {
			t.Fatal("malformed or unsafe public release accepted")
		}
	}
}

func TestShellSmallLogicalScreenUsesWindowsMaximise(t *testing.T) {
	for _, c := range []struct {
		width, height int
		want          bool
	}{{1093, 614, true}, {1366, 768, false}, {1920, 1080, false}, {1000, 760, false}, {999, 1080, true}, {1920, 759, true}, {0, 600, false}, {1000, 0, false}, {-1, 800, false}} {
		if got := shouldMaximise(c.width, c.height); got != c.want {
			t.Fatalf("screen %d×%d: got %v, want %v", c.width, c.height, got, c.want)
		}
	}
}

type fakeHeadlessApp struct {
	calls []string
	node  string
	proxy bool
	err   error
}

func (a *fakeHeadlessApp) Status() map[string]any { return map[string]any{"connected": false} }
func (a *fakeHeadlessApp) LoginSecure(string, string, string, string, bool) error {
	a.calls = append(a.calls, "login")
	return a.err
}
func (a *fakeHeadlessApp) Logout() error { a.calls = append(a.calls, "logout"); return a.err }
func (a *fakeHeadlessApp) Query(path string, _ map[string]any) (any, error) {
	a.calls = append(a.calls, "query:"+path)
	return map[string]any{"public": true}, a.err
}
func (a *fakeHeadlessApp) Connect(string, bool) error {
	a.calls = append(a.calls, "connect")
	return a.err
}
func (a *fakeHeadlessApp) Disconnect() error   { a.calls = append(a.calls, "disconnect"); return a.err }
func (a *fakeHeadlessApp) RecoverProxy() error { a.calls = append(a.calls, "recover"); return a.err }
func (a *fakeHeadlessApp) SavePreferences(node string, proxy bool) error {
	a.calls = append(a.calls, "preferences")
	a.node, a.proxy = node, proxy
	return a.err
}
func (a *fakeHeadlessApp) CancelConnect() error { a.calls = append(a.calls, "cancel"); return a.err }
func (a *fakeHeadlessApp) SetAPI(string) error  { a.calls = append(a.calls, "settings"); return a.err }
func (a *fakeHeadlessApp) Probe(string) (int64, error) {
	a.calls = append(a.calls, "probe")
	return 12, a.err
}
func (a *fakeHeadlessApp) DiagnosticsReport() (string, error) {
	a.calls = append(a.calls, "diagnostics")
	return "sanitized fixture report", a.err
}

func TestHeadlessPreferencesCancelDiagnosticsAndPublicQuery(t *testing.T) {
	a := &fakeHeadlessApp{}
	addr, token := "127.0.0.1:19091", "isolated-csrf-fixture"
	h := headlessHandler(a, addr, token, func() {})
	send := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://"+addr+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://"+addr)
		r.Header.Set("X-CSRF-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := send("/api/preferences", `{"node_id":"fixture-node","system_proxy":true}`); w.Code != 200 || a.node != "fixture-node" || !a.proxy {
		t.Fatal("preferences not forwarded")
	}
	if w := send("/api/cancel", `{}`); w.Code != 200 || a.calls[len(a.calls)-1] != "cancel" {
		t.Fatal("cancel not forwarded")
	}
	if w := send("/api/diagnostics", `{}`); w.Code != 200 || !strings.Contains(w.Body.String(), "sanitized fixture report") {
		t.Fatal("diagnostics not returned without a dialog")
	}
	if w := send("/api/query", `{"path":"/api/public/commerce"}`); w.Code != 200 || a.calls[len(a.calls)-1] != "query:/api/public/commerce" {
		t.Fatal("public query not forwarded")
	}
	a.err = errors.New("fixture error")
	if w := send("/api/settings", `{"api_url":"https://fixture.example.test/"}`); w.Code != 400 {
		t.Fatal("settings error incorrectly reported as success")
	}
	a.err = nil
	if w := send("/api/probe", `{"node_id":"fixture-node"}`); w.Code != 200 || !strings.Contains(w.Body.String(), "latency_ms") {
		t.Fatal("probe result not returned")
	}
	a.err = errors.New("fixture error")
	if w := send("/api/cancel", `{}`); w.Code != 400 {
		t.Fatal("engine failure incorrectly reported as success")
	}
}

func TestHeadlessRejectsCrossOriginAndMalformedRequests(t *testing.T) {
	a := &fakeHeadlessApp{}
	addr, token := "127.0.0.1:19091", "isolated-csrf-fixture"
	h := headlessHandler(a, addr, token, func() {})
	for _, kind := range []string{"host", "origin", "csrf", "method", "unknown-field", "extra-json", "oversize"} {
		r := httptest.NewRequest("POST", "http://"+addr+"/api/preferences", strings.NewReader(`{}`))
		r.Header.Set("Origin", "http://"+addr)
		r.Header.Set("X-CSRF-Token", token)
		want := 403
		switch kind {
		case "host":
			r.Host = "attacker.example.test"
		case "origin":
			r.Header.Set("Origin", "https://attacker.example.test")
		case "csrf":
			r.Header.Set("X-CSRF-Token", "wrong")
		case "method":
			r.Method = "GET"
		case "unknown-field", "extra-json", "oversize":
			body := map[string]string{"unknown-field": `{"ignored":true}`, "extra-json": `{} {}`, "oversize": `{"node_id":"` + strings.Repeat("a", 1<<20) + `"}`}[kind]
			r = httptest.NewRequest("POST", "http://"+addr+"/api/preferences", strings.NewReader(body))
			r.Header.Set("Origin", "http://"+addr)
			r.Header.Set("X-CSRF-Token", token)
			want = 400
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s returned %d, want %d", kind, w.Code, want)
		}
	}
	if len(a.calls) != 0 {
		t.Fatal("rejected request reached engine")
	}
	r := httptest.NewRequest("GET", "http://"+addr+"/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var status map[string]any
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("isolated status endpoint unavailable")
	}
}
