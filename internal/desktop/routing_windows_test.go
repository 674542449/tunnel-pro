//go:build windows

package desktop

import (
	"strings"
	"testing"
	"tunnelx/internal/routing"
)

func TestProxyModePersistenceAndActiveGuard(t *testing.T) {
	root := t.TempDir()
	e, err := New(root, Settings{APIURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.SaveProxyMode(routing.Bypass, "*.qq.com\n192.168.1.7/24"); err != nil {
		t.Fatal(err)
	}
	if err = e.SavePreferences(strings.Repeat("a", 32), false); err != nil {
		t.Fatal(err)
	}
	again, err := New(root, Settings{APIURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if again.preferences.ProxyMode != routing.Bypass || len(again.preferences.DirectDomains) != 2 {
		t.Fatal(again.preferences)
	}
	e.connecting = true
	if err = e.SaveProxyMode(routing.TUN, ""); err == nil {
		t.Fatal("changed mode during connect")
	}
	e.connecting = false
	if err = e.SaveProxyMode("made-up", ""); err == nil {
		t.Fatal("accepted invalid mode")
	}
	if err = e.SaveProxyMode(routing.Bypass, "0.0.0.0/0"); err == nil {
		t.Fatal("accepted catch-all")
	}
}
