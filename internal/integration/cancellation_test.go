package integration

import (
	"context"
	"sync"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/control"
	"tunnelx/internal/server"
)

type stressGate struct{ token string }
type stressPermit struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (g stressGate) Authorize(ctx context.Context, h []string, _ string) (server.Permit, bool) {
	if len(h) != 1 || h[0] != "Bearer "+g.token {
		return nil, false
	}
	c, cancel := context.WithCancel(ctx)
	return &stressPermit{c, cancel}, true
}
func (p *stressPermit) Context() context.Context { return p.ctx }
func (p *stressPermit) Allowed() bool            { return p.ctx.Err() == nil }
func (p *stressPermit) Account(bool, int)        {}
func (p *stressPermit) SpeedLimit() int64        { return 0 }
func (p *stressPermit) Close()                   { p.cancel() }
func TestManagedCancellationStress(t *testing.T) {
	env := start(t)
	token := control.Token()
	env.server.Access = stressGate{token}
	cfg := env.cfg
	cfg.Token = token
	cfg.DeviceID = control.ID()
	cfg.H2Connections = 4
	m, e := client.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if i%4 == 0 {
					ps, e := m.OpenUDP(ctx, env.udp)
					if e != nil {
						errs <- e
						return
					}
					ps.Send([]byte("cancel UDP"))
					ps.Close()
				} else {
					st, e := m.OpenTCP(ctx, env.half)
					if e != nil {
						errs <- e
						return
					}
					st.Write([]byte("cancel TCP"))
					st.Close()
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for env.server.Active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if env.server.Active.Load() != 0 {
		t.Fatal("cancelled handlers retained active slots")
	}
}
