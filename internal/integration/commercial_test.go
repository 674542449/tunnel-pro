package integration

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/control"
	"tunnelx/internal/nodeagent"
)

func TestCommercialStrictH2QuotaAndRefundRevocation(t *testing.T) {
	env := start(t)
	api, e := control.NewAPI(control.Config{Listen: "127.0.0.1:1", PublicURL: "http://127.0.0.1:1", DataFile: filepath.Join(testTempDir(t), "control.json"), AdminEmail: "admin@example.test", AdminPassword: "commercial-test-password"})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	n := control.Node{ID: control.ID(), Name: "strict", Enabled: true, AgentKey: control.Token(), TestOnly: true}
	u := control.User{ID: control.ID(), Role: "user", Beta: true, EmailVerifiedAt: now, TunnelToken: control.Token()}
	entitlement := control.Entitlement{ID: control.ID(), UserID: u.ID, StartsAt: now - 1, EndsAt: now + 3600, Bytes: 64 << 20, Devices: 1, Kind: "subscription", Test: true}
	api.Store.Update(func(d *control.State) error {
		d.Nodes = append(d.Nodes, n)
		d.Users = append(d.Users, u)
		d.Entitlements = append(d.Entitlements, entitlement)
		return nil
	})
	h := httptest.NewServer(api)
	defer h.Close()
	agent, e := nodeagent.New(nodeagent.Config{APIURL: h.URL, NodeID: n.ID, AgentKey: n.AgentKey, StateFile: filepath.Join(testTempDir(t), "agent.json")})
	if e != nil {
		t.Fatal(e)
	}
	agent.ManagedOnly = true
	if e = agent.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	env.server.Access = agent
	env.server.Config.ManagedOnly = true
	cfg := env.cfg
	cfg.Privacy = "strict"
	cfg.ServerName = env.inner
	cfg.ECHConfig = env.ech
	cfg.Token = u.TunnelToken
	cfg.DeviceID = control.ID()
	m, e := client.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, e := m.OpenTCP(ctx, env.half)
	if e != nil {
		t.Fatal("leased TCP authorization", e)
	}
	data := bytes.Repeat([]byte("billing-test"), 1024)
	stream.Write(data)
	stream.CloseWrite()
	out, e := io.ReadAll(stream)
	stream.Close()
	if e != nil || len(out) == 0 {
		t.Fatal("leased H2 transfer", e)
	}
	if e = agent.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	stored := api.Store.Snapshot()
	if stored.TestUsage[u.ID].Upload == 0 || stored.Users[1].Upload > 0 {
		t.Fatal("usage not metered separately")
	}
	api.Store.Update(func(d *control.State) error { d.Entitlements[0].RevokedAt = time.Now().Unix(); return nil })
	if e = agent.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if st, e := m.OpenTCP(ctx, env.half); e == nil {
		st.Close()
		t.Fatal("refunded grant still connects")
	}
	owner := env.cfg
	owner.DeviceID = control.ID()
	original, e := client.New(owner)
	if e != nil {
		t.Fatal(e)
	}
	defer original.Close()
	if st, e := original.OpenTCP(ctx, env.half); e == nil {
		st.Close()
		t.Fatal("static owner token bypassed managed-only billing")
	}
}
