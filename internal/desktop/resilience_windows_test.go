//go:build windows

package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"tunnelx/internal/client"
)

func TestLayerHealthDoesNotConfuseDNSWithGateway(t *testing.T) {
	e, err := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	m := &client.Mux{}
	e.mux = m
	e.applyLayers(m, 10, [3]string{"", "dns_resolution", ""})
	h := e.Status()["health"].(Health)
	if h.Gateway.ErrorKind != "" || h.Gateway.LastSuccess != 10 || h.DNS.Failures != 1 || h.ErrorKind != "dns_unavailable" || e.Status()["connection_state"] != "degraded" {
		t.Fatal(h)
	}
	e.applyLayers(m, 20, [3]string{"", "", "egress_https"})
	if e.health.DNS.Failures != 0 || e.health.ErrorKind != "egress_unavailable" {
		t.Fatal(e.health)
	}
	e.applyLayers(m, 30, [3]string{})
	if e.health.LastSuccess != 30 || e.Status()["connection_state"] != "connected" {
		t.Fatal(e.health)
	}
	prior := e.health
	e.mux = nil
	e.applyLayers(m, 40, [3]string{"timeout", "dns_resolution", "egress_https"})
	if e.health != prior {
		t.Fatal("stale health changed a disconnected session")
	}
}
func TestRecoveryCancellationPreventsDelayedConnect(t *testing.T) {
	for _, action := range []string{"cancel", "disconnect", "logout"} {
		t.Run(action, func(t *testing.T) {
			e, err := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e.recovering = true
			e.recoveryContext = ctx
			e.recoveryCancel = cancel
			e.recoveryEpoch = 21
			e.stage = "reconnecting"
			if e.Status()["connection_state"] != "connecting" {
				t.Fatal("recovery not cancellable in UI")
			}
			switch action {
			case "cancel":
				err = e.CancelConnect()
			case "disconnect":
				err = e.Disconnect()
			case "logout":
				err = e.Logout()
			}
			if err != nil {
				t.Fatal(err)
			}
			if ctx.Err() == nil || e.recovering {
				t.Fatal("retry loop survived user cancellation")
			}
			if err = e.connect(strings.Repeat("a", 32), false, 21); !errors.Is(err, context.Canceled) {
				t.Fatal("delayed attempt not rejected", err)
			}
			if e.mux != nil || e.connecting {
				t.Fatal("cancelled recovery installed a connection")
			}
		})
	}
}
func TestStaleTUNWatcherCannotRestartNewSession(t *testing.T) {
	e, err := New(t.TempDir(), Settings{APIURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	current := &client.Mux{}
	e.mux = current
	done := make(chan struct{})
	go func() { e.recoverTUN(&client.Mux{}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale watcher blocked")
	}
	if e.mux != current || e.recovering {
		t.Fatal("stale watcher replaced new connection")
	}
}
