package integration

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
)

func TestLivePublicService(t *testing.T) {
	path := os.Getenv("TUNNELX_LIVE_CONFIG")
	if path == "" {
		t.Skip("live endpoint not configured")
	}
	var c config.Client
	if e := config.Read(path, &c); e != nil {
		t.Fatal(e)
	}
	c.CAFile = config.Resolve(path, c.CAFile)
	for _, mode := range []string{"h2"} {
		t.Run(mode, func(t *testing.T) {
			cfg := c
			cfg.Transport = mode
			m, e := client.New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			res, e := m.PublicGET(ctx)
			t.Logf("TLS version=%x cipher=%x ECH=%v", m.TLSVersion.Load(), m.CipherSuite.Load(), m.LastECH.Load())
			if e != nil {
				t.Fatal(e)
			}
			defer res.Body.Close()
			b, e := io.ReadAll(res.Body)
			if e != nil || res.StatusCode != 200 {
				t.Fatal(res.StatusCode, e)
			}
			t.Logf("HTTP %d body=%d", res.ProtoMajor, len(b))
		})
	}
}
func TestLiveTunnels(t *testing.T) {
	path := os.Getenv("TUNNELX_LIVE_CONFIG")
	if path == "" {
		t.Skip("live endpoint not configured")
	}
	var c config.Client
	if e := config.Read(path, &c); e != nil {
		t.Fatal(e)
	}
	c.CAFile = config.Resolve(path, c.CAFile)
	for _, mode := range []string{"h2"} {
		t.Run(mode, func(t *testing.T) {
			cfg := c
			cfg.Transport = mode
			m, e := client.New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for i := 0; i < 2; i++ {
				st, e := m.OpenTCP(ctx, "127.0.0.1:18480")
				if e != nil {
					t.Fatal(e)
				}
				fmt.Fprintf(st, "GET /download?size=8388608 HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n")
				st.CloseWrite()
				res, e := http.ReadResponse(bufio.NewReader(st), nil)
				if e != nil {
					t.Fatal(e)
				}
				n, e := io.Copy(io.Discard, res.Body)
				res.Body.Close()
				st.Close()
				if e != nil || n != 8388608 {
					t.Fatal(n, e)
				}
			}
			ps, e := m.OpenUDP(ctx, "127.0.0.1:18481")
			if e != nil {
				t.Fatal(e)
			}
			defer ps.Close()
			data := bytes.Repeat([]byte{42}, 900)
			for i := 0; i < 5; i++ {
				if e = ps.Send(data); e != nil {
					t.Fatal(e)
				}
				b, e := ps.Receive(ctx)
				if e != nil || !bytes.Equal(data, b) {
					t.Fatal(e)
				}
			}
			t.Logf("mode=%s ECH=%v downloads=16MiB UDP=5 echoes", mode, m.LastECH.Load())
		})
	}
}
