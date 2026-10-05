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

func TestManagedH2TCPUDPAndLiveRevocation(t *testing.T) {
	env := start(t)
	api, e := control.NewAPI(control.Config{Listen: "127.0.0.1:1", PublicURL: "http://127.0.0.1:1", DataFile: filepath.Join(testTempDir(t), "control.json"), AdminEmail: "admin@example.com", AdminPassword: "test-admin-password-for-store"})
	if e != nil {
		t.Fatal(e)
	}
	n := control.Node{ID: control.ID(), Name: "local", Enabled: true, AgentKey: control.Token()}
	u := control.User{ID: control.ID(), TunnelToken: control.Token(), Role: "user", ExpiresAt: time.Now().Add(time.Hour).Unix(), Devices: 2}
	api.Store.Update(func(d *control.State) error { d.Nodes = append(d.Nodes, n); d.Users = append(d.Users, u); return nil })
	h := httptest.NewServer(api)
	defer h.Close()
	agent, e := nodeagent.New(nodeagent.Config{APIURL: h.URL, NodeID: n.ID, AgentKey: n.AgentKey, StateFile: filepath.Join(testTempDir(t), "agent.json")})
	if e != nil {
		t.Fatal(e)
	}
	if e = agent.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	env.server.Access = agent
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, e := m.OpenTCP(ctx, env.half)
	if e != nil {
		t.Fatal(e)
	}
	payload := bytes.Repeat([]byte("managed-half-close"), 1024)
	if _, e = stream.Write(payload); e != nil {
		t.Fatal(e)
	}
	stream.CloseWrite()
	if b, e := io.ReadAll(stream); e != nil || len(b) == 0 {
		t.Fatal("managed half-close", e)
	}
	stream.Close()
	ps, e := m.OpenUDP(ctx, env.udp)
	if e != nil {
		t.Fatal(e)
	}
	packet := []byte("managed-udp")
	ps.Send(packet)
	echo, e := ps.Receive(ctx)
	if e != nil || !bytes.Equal(echo, packet) {
		t.Fatal("managed UDP", e)
	}
	ps.Close()
	if !m.LastECH.Load() {
		t.Fatal("managed ECH not accepted")
	}
	if e = agent.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	state := api.Store.Snapshot()
	for _, v := range state.Users {
		if v.ID == u.ID && (v.Upload < int64(len(payload)+len(packet)) || v.Download == 0) {
			t.Fatal("managed usage not recorded")
		}
	}
	live, e := m.OpenTCP(ctx, env.half)
	if e != nil {
		t.Fatal(e)
	}
	defer live.Close()
	live.Write([]byte("live before ban"))
	api.Store.Update(func(d *control.State) error {
		for i := range d.Users {
			if d.Users[i].ID == u.ID {
				d.Users[i].Disabled = true
			}
		}
		return nil
	})
	if e = agent.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, err := live.Read(make([]byte, 1)); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("banned stream still readable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("banned stream not closed")
	}
	before := env.server.TargetAttempts.Load()
	if st, e := m.OpenTCP(ctx, "not-resolved.invalid:443"); e == nil {
		st.Close()
		t.Fatal("banned new stream admitted")
	}
	if env.server.TargetAttempts.Load() != before {
		t.Fatal("banned request resolved target")
	}
}
