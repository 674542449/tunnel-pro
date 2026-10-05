package integration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/control"
	"tunnelx/internal/nodeagent"
)

func TestCommercialExactTCPQuotaStopsLargeDownload(t *testing.T) {
	env := start(t)
	api, e := control.NewAPI(control.Config{Listen: "127.0.0.1:1", PublicURL: "http://127.0.0.1:1", DataFile: filepath.Join(testTempDir(t), "state.json"), AdminEmail: "admin@example.test", AdminPassword: "exact-quota-test-password"})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	n := control.Node{ID: control.ID(), Enabled: true, TestOnly: true, AgentKey: control.Token()}
	u := control.User{ID: control.ID(), Role: "user", Beta: true, EmailVerifiedAt: now, TunnelToken: control.Token()}
	const quota = 100 << 10
	api.Store.Update(func(d *control.State) error {
		d.Nodes = append(d.Nodes, n)
		d.Users = append(d.Users, u)
		d.Entitlements = append(d.Entitlements, control.Entitlement{ID: control.ID(), UserID: u.ID, StartsAt: now - 1, EndsAt: now + 3600, Bytes: quota, Devices: 1, Test: true, Kind: "subscription"})
		return nil
	})
	h := httptest.NewServer(api)
	defer h.Close()
	agent, e := nodeagent.New(nodeagent.Config{APIURL: h.URL, NodeID: n.ID, AgentKey: n.AgentKey, StateFile: filepath.Join(testTempDir(t), "agent.json")})
	if e != nil {
		t.Fatal(e)
	}
	agent.ManagedOnly = true
	agent.Sync(context.Background())
	env.server.Config.ManagedOnly = true
	env.server.Access = agent
	cfg := env.cfg
	cfg.Token = u.TunnelToken
	cfg.DeviceID = control.ID()
	m, e := client.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, e := m.OpenTCP(ctx, env.tcp)
	if e != nil {
		t.Fatal(e)
	}
	fmt.Fprint(st, "GET /download?size=524288 HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n")
	st.CloseWrite()
	response, e := http.ReadResponse(bufio.NewReader(st), nil)
	if e != nil {
		t.Fatal(e)
	}
	received, _ := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	st.Close()
	if received <= 0 || received > quota {
		t.Fatalf("quota overspent: body %d quota %d", received, quota)
	}
	state := api.Store.Snapshot()
	if len(state.Leases) != 1 {
		t.Fatal("no global lease")
	}
	l := state.Leases[0]
	if l.Used > quota {
		t.Fatal("central usage exceeds quota")
	}
}
