//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	wailsassets "github.com/wailsapp/wails/v2/pkg/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options"
	assetoptions "github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"golang.org/x/net/html"
)

type shellRuntimeAssets struct{ ipc, runtime []byte }

func (a shellRuntimeAssets) DesktopIPC() []byte       { return a.ipc }
func (a shellRuntimeAssets) WebsocketIPC() []byte     { return nil }
func (a shellRuntimeAssets) RuntimeDesktopJS() []byte { return a.runtime }

// Exercise the production asset server and actual vendored runtime bytes, without
// creating a WebView or replacing window.go with a fake application bridge.
// Bindings below reflect the shell's public names; native Wails reflection and
// WebView2 IPC dispatch are separately verified by reading the framework source.
func TestShellProductionAssetServerInjectsNativeRuntimeBeforeApplication(t *testing.T) {
	readRuntime := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("vendor", "github.com", "wailsapp", "wails", "v2", "internal", "frontend", "runtime", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	typ := reflect.TypeOf(&App{})
	methods := map[string]any{}
	for i := 0; i < typ.NumMethod(); i++ {
		methods[typ.Method(i).Name] = map[string]any{}
	}
	for _, method := range []string{"Status", "LoginSecure", "Query", "Connect", "CancelConnect", "SavePreferences", "OpenDownload"} {
		if _, ok := methods[method]; !ok {
			t.Fatalf("native shell lacks %s", method)
		}
	}
	bindings, err := json.Marshal(map[string]any{"main": map[string]any{"App": methods}})
	if err != nil {
		t.Fatal(err)
	}
	ui, err := fs.Sub(assets, "ui")
	if err != nil {
		t.Fatal(err)
	}
	bundle := shellRuntimeAssets{ipc: readRuntime("ipc.js"), runtime: readRuntime("runtime_prod_desktop.js")}
	server, err := wailsassets.NewAssetServerMainPage(string(bindings), &options.App{AssetServer: &assetoptions.Options{Assets: ui}}, false, nil, bundle)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://wails.localhost"+path, nil)
		// Embedded files carry no modification timestamp. A cache validator must
		// not substitute a stale app script after a new executable is installed.
		r.Header.Set("If-Modified-Since", "Tue, 01 Jan 2030 00:00:00 GMT")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d", path, w.Code)
		}
		return w
	}
	index := get("/")
	if !strings.Contains(index.Header().Get("Content-Type"), "text/html") {
		t.Fatal("main page not served as HTML")
	}
	doc, err := html.Parse(bytes.NewReader(index.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "src" {
					scripts = append(scripts, attr.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if len(scripts) != 3 || scripts[0] != "/wails/ipc.js" || scripts[1] != "/wails/runtime.js" || scripts[2] != "app.js" {
		t.Fatalf("unexpected native initialization order: %v", scripts)
	}
	for _, path := range []string{"/app.js", "/style.css"} {
		response := get(path)
		expected, err := fs.ReadFile(ui, strings.TrimPrefix(path, "/"))
		if err != nil || !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatalf("%s does not match embedded release asset: %v", path, err)
		}
		if response.Header().Get("Last-Modified") != "" || response.Header().Get("ETag") != "" {
			t.Fatalf("%s unexpectedly emits a persistent cache validator", path)
		}
	}
	if !bytes.Equal(get("/wails/ipc.js").Body.Bytes(), bundle.ipc) {
		t.Fatal("native IPC script was not served")
	}
	runtime := get("/wails/runtime.js").Body.Bytes()
	if !bytes.HasPrefix(runtime, []byte("window.wailsbindings='")) || !bytes.HasSuffix(runtime, bundle.runtime) || !bytes.Contains(runtime, []byte("LoginSecure")) {
		t.Fatal("runtime does not contain shell bindings and production runtime together")
	}
	t.Logf("production resource order: %v; native shell exposes %d methods", scripts, len(methods))
}
