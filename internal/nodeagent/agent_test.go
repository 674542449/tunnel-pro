package nodeagent

import (
	"context"
	"crypto/sha256"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"tunnelx/internal/control"
)

func fixture(t *testing.T) (*Agent, *control.API, control.User) {
	t.Helper()
	api, e := control.NewAPI(control.Config{Listen: "127.0.0.1:1", PublicURL: "http://127.0.0.1:1", DataFile: filepath.Join(testTempDir(t), "control.json"), AdminEmail: "admin@example.com", AdminPassword: "admin-password-at-least-16"})
	if e != nil {
		t.Fatal(e)
	}
	n := control.Node{ID: control.ID(), Name: "node", Enabled: true, AgentKey: control.Token()}
	u := control.User{ID: control.ID(), TunnelToken: control.Token(), Role: "user", ExpiresAt: time.Now().Add(time.Hour).Unix(), Devices: 1, Limit: 1000}
	if e = api.Store.Update(func(d *control.State) error { d.Nodes = append(d.Nodes, n); d.Users = append(d.Users, u); return nil }); e != nil {
		t.Fatal(e)
	}
	h := httptest.NewServer(api)
	t.Cleanup(h.Close)
	a, e := New(Config{APIURL: h.URL, NodeID: n.ID, AgentKey: n.AgentKey, StateFile: filepath.Join(testTempDir(t), "agent.json")})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	return a, api, u
}
func TestDevicesQuotaRetryAndRestart(t *testing.T) {
	a, api, u := fixture(t)
	headers := []string{"Bearer " + u.TunnelToken}
	device := control.ID()
	p, ok := a.Authorize(context.Background(), headers, device)
	if !ok {
		t.Fatal("active user denied")
	}
	defer p.Close()
	same, ok := a.Authorize(context.Background(), headers, device)
	if !ok {
		t.Fatal("same device stream denied")
	}
	same.Close()
	if extra, ok := a.Authorize(context.Background(), headers, control.ID()); ok {
		extra.Close()
		t.Fatal("device limit not enforced")
	}
	p.Account(true, 200)
	p.Account(false, 100)
	if e := a.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := a.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, v := range api.Store.Snapshot().Users {
		if v.ID == u.ID && (v.Upload != 200 || v.Download != 100) {
			t.Fatal("duplicated or lost traffic")
		}
	}
	restored, e := New(a.Config)
	if e != nil {
		t.Fatal(e)
	}
	if restored.state.Counters[u.ID].Upload != 200 || restored.state.BootID != a.state.BootID {
		t.Fatal("pending counters lost on restart")
	}
	p.Account(false, 701)
	select {
	case <-p.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("quota exhausted stream remains live")
	}
	if _, ok = a.Authorize(context.Background(), headers, device); ok {
		t.Fatal("quota exhausted user admitted")
	}
}
func TestRevocationStalePolicyAndRotatedToken(t *testing.T) {
	a, api, u := fixture(t)
	p, ok := a.Authorize(context.Background(), []string{"Bearer " + u.TunnelToken}, control.ID())
	if !ok {
		t.Fatal("user denied")
	}
	defer p.Close()
	api.Store.Update(func(d *control.State) error {
		for i := range d.Users {
			if d.Users[i].ID == u.ID {
				d.Users[i].Disabled = true
			}
		}
		return nil
	})
	if e := a.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-p.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("banned stream remains live")
	}
	a.mu.Lock()
	a.grants[sha256.Sum256([]byte("Bearer "+u.TunnelToken))] = control.Grant{UserID: u.ID, Token: u.TunnelToken, ExpiresAt: time.Now().Add(time.Hour).Unix(), Devices: 1, Unlimited: true}
	a.validUntil = time.Now().Add(-time.Second)
	a.mu.Unlock()
	if _, ok = a.Authorize(context.Background(), []string{"Bearer " + u.TunnelToken}, control.ID()); ok {
		t.Fatal("stale policy admitted user")
	}
}
