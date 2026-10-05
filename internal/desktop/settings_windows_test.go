//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameServiceAddressDoesNotClearSession(t *testing.T) {
	e, err := New(t.TempDir(), Settings{APIURL: "https://portal.example.test/control"})
	if err != nil {
		t.Fatal(err)
	}
	e.login.Token = "fixture-session"
	e.login.Email = "fixture@example.test"
	if err = e.saveLogin(); err != nil {
		t.Fatal(err)
	}
	if err = e.SetAPI(" https://portal.example.test/control/ "); err != nil {
		t.Fatal(err)
	}
	if !e.Status()["logged_in"].(bool) {
		t.Fatal("same service must preserve login")
	}
	restored, err := New(e.root, e.Settings())
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Status()["logged_in"].(bool) {
		t.Fatal("same service lost persisted login")
	}
}

func TestServiceAddressTrailingSlashWorksAtStartup(t *testing.T) {
	e, err := New(t.TempDir(), Settings{APIURL: "https://portal.example.test/control/"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Settings().APIURL != "https://portal.example.test/control" || e.PortalURL() != "https://portal.example.test/control/" {
		t.Fatal("service URL not normalized")
	}
}

func TestLogoutPersistenceFailureDoesNotPretendSessionWasRemoved(t *testing.T) {
	e, err := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	e.login.Token = "fixture-session"
	e.login.Email = "fixture@example.test"
	// A directory at the credential filename deterministically rejects atomic replacement.
	if err = os.MkdirAll(filepath.Join(e.root, "state", "auth.dpapi"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = e.Logout(); err == nil {
		t.Fatal("expected persistence failure")
	}
	if !e.Status()["logged_in"].(bool) {
		t.Fatal("UI must not claim logout when disk credentials could not be replaced")
	}
}
