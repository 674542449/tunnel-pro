package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
	"tunnelx/internal/pki"
	"tunnelx/internal/server"
)

type environment struct {
	cfg            config.Client
	server         *server.Server
	tcp, udp, half string
	inner          string
	ech            string
}

func TestCanceledProbeDoesNotAffectBusinessTunnel(t *testing.T) {
	env := start(t)
	for _, mode := range []string{"h2"} {
		for _, privacy := range []string{"normal", "strict"} {
			cfg := env.cfg
			cfg.Transport = mode
			cfg.Privacy = privacy
			if privacy == "strict" {
				cfg.ServerName = env.inner
				cfg.ECHConfig = env.ech
			}
			m, err := client.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			response, err := m.PublicGET(ctx)
			if err != nil {
				cancel()
				m.Close()
				t.Fatal(mode, privacy, err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			cancel()
			business, stop := context.WithTimeout(context.Background(), 5*time.Second)
			stream, err := m.OpenTCP(business, env.half)
			if err != nil {
				stop()
				m.Close()
				t.Fatal(mode, privacy, err)
			}
			stream.Write([]byte("probe-complete"))
			stream.CloseWrite()
			body, err := io.ReadAll(stream)
			stream.Close()
			stop()
			if err != nil || !strings.HasPrefix(string(body), "14 ") || stream.Transport() != mode || (privacy == "strict" && !m.LastECH.Load()) {
				m.Close()
				t.Fatal("probe cancellation affected business stream", mode, privacy, string(body), err)
			}
			m.Close()
		}
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func start(t *testing.T, extraTargets ...string) environment {
	t.Helper()
	dir := testTempDir(t)
	ca, cert, key, e := pki.Certificate("proxy.test")
	if e != nil {
		t.Fatal(e)
	}
	innerCA, innerCert, innerKey, e := pki.Certificate("edge.tunnelx.invalid")
	if e != nil {
		t.Fatal(e)
	}
	ek, list, e := pki.ECH("proxy.test")
	if e != nil {
		t.Fatal(e)
	}
	write := func(n string, b []byte) string {
		p := filepath.Join(dir, n)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	jsonKey, _ := json.Marshal(ek)
	root := write("roots.pem", append(ca, innerCA...))
	cf := write("cert.pem", cert)
	kf := write("key.pem", key)
	ef := write("ech.json", jsonKey)
	icf := write("inner-cert.pem", innerCert)
	ikf := write("inner-key.pem", innerKey)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			h := sha256.New()
			n, _ := io.Copy(h, r.Body)
			fmt.Fprintf(w, "%d %x", n, h.Sum(nil))
		default:
			w.Header().Set("Content-Length", "1048576")
			io.CopyN(w, strings.NewReader(strings.Repeat("x", 1<<20)), 1<<20)
		}
	}))
	t.Cleanup(fixture.Close)
	tcp := strings.TrimPrefix(fixture.URL, "http://")
	u, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { u.Close() })
	go func() {
		b := make([]byte, 65535)
		for {
			n, a, e := u.ReadFromUDP(b)
			if e != nil {
				return
			}
			u.WriteToUDP(b[:n], a)
		}
	}()
	half, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { half.Close() })
	go func() {
		for {
			c, e := half.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(10 * time.Second))
				h := sha256.New()
				n, _ := io.Copy(h, c)
				fmt.Fprintf(c, "%d %x", n, h.Sum(nil))
			}()
		}
	}()
	addr := freePort(t)
	_, port, _ := net.SplitHostPort(addr)
	var pn int
	fmt.Sscan(port, &pn)
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	sc := config.Server{Listen: addr, CertFile: cf, KeyFile: kf, ECHKeyFile: ef, InnerName: "edge.tunnelx.invalid", InnerCertFile: icf, InnerKeyFile: ikf, Tokens: []string{token}, AllowedPrivateTargets: []string{tcp, u.LocalAddr().String(), half.Addr().String()}, DialTimeout: 2, IdleTimeout: 10}
	sc.AllowedPrivateTargets = append(sc.AllowedPrivateTargets, extraTargets...)
	s, e := server.New(sc)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server shutdown timed out")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server failed to listen")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := config.Client{ServerName: "proxy.test", ServerIP: "127.0.0.1", Port: pn, Token: token, CAFile: root, Transport: "h2", Privacy: "normal", SOCKSListen: freePort(t), HTTPListen: freePort(t), WebListen: freePort(t), ConnectTimeout: 2}
	return environment{cfg: c, server: s, tcp: tcp, udp: u.LocalAddr().String(), half: half.Addr().String(), inner: "edge.tunnelx.invalid", ech: base64.StdEncoding.EncodeToString(list)}
}
func TestTransportsAndPrivacy(t *testing.T) {
	env := start(t)
	for _, mode := range []string{"h2"} {
		for _, privacy := range []string{"normal", "strict"} {
			t.Run(mode+"/"+privacy, func(t *testing.T) {
				c := env.cfg
				c.Transport = mode
				c.Privacy = privacy
				if privacy == "strict" {
					c.ServerName = env.inner
					c.ECHConfig = env.ech
				}
				m, e := client.New(c)
				if e != nil {
					t.Fatal(e)
				}
				defer m.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				stream, e := m.OpenTCP(ctx, env.tcp)
				if e != nil {
					t.Fatal(e)
				}
				req, _ := http.NewRequest("GET", "http://"+env.tcp+"/", nil)
				if e = req.Write(stream); e != nil {
					t.Fatal(e)
				}
				stream.CloseWrite()
				res, e := http.ReadResponse(bufReader(stream), req)
				if e != nil {
					t.Fatal(e)
				}
				h := sha256.New()
				n, e := io.Copy(h, res.Body)
				res.Body.Close()
				stream.Close()
				expected := sha256.Sum256(bytes.Repeat([]byte{'x'}, 1<<20))
				if e != nil || n != 1<<20 || hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(expected[:]) {
					t.Fatalf("download hash mismatch: n=%d e=%v", n, e)
				}
				data := bytes.Repeat([]byte("half-close-test"), 1000)
				st, e := m.OpenTCP(ctx, env.half)
				if e != nil {
					t.Fatal(e)
				}
				st.Write(data)
				st.CloseWrite()
				reply, e := io.ReadAll(st)
				st.Close()
				h2 := sha256.Sum256(data)
				if e != nil || string(reply) != fmt.Sprintf("%d %x", len(data), h2) {
					t.Fatalf("half-close reply: %q %v", reply, e)
				}
				ps, e := m.OpenUDP(ctx, env.udp)
				if e != nil {
					t.Fatal(e)
				}
				defer ps.Close()
				for i := 0; i < 10; i++ {
					data := bytes.Repeat([]byte{byte(i)}, 700)
					if e = ps.Send(data); e != nil {
						t.Fatal(e)
					}
					b, e := ps.Receive(ctx)
					if e != nil || !bytes.Equal(data, b) {
						t.Fatalf("UDP mismatch %v", e)
					}
				}
				if privacy == "strict" && !m.LastECH.Load() {
					t.Fatal("ECH was not accepted")
				}
			})
		}
	}
}

func TestAuthAndCertificateFailures(t *testing.T) {
	env := start(t)
	for _, mode := range []string{"h2"} {
		t.Run(mode, func(t *testing.T) {
			c := env.cfg
			c.Transport = mode
			c.Token = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{99}, 32))
			m, _ := client.New(c)
			before := env.server.TargetAttempts.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, e := m.OpenTCP(ctx, "unresolved.invalid:443")
			m.Close()
			if e == nil {
				t.Fatal("wrong token accepted")
			}
			if env.server.TargetAttempts.Load() != before {
				t.Fatal("unauthenticated request triggered target activity")
			}
			c = env.cfg
			c.Transport = mode
			c.ServerName = "wrong.test"
			m, _ = client.New(c)
			_, e = m.OpenTCP(ctx, env.tcp)
			m.Close()
			if e == nil {
				t.Fatal("wrong hostname accepted or downgraded")
			}
		})
	}
}

func TestECHRejectionDoesNotDowngrade(t *testing.T) {
	env := start(t)
	for _, mode := range []string{"h2"} {
		c := env.cfg
		c.Transport = mode
		c.Privacy = "strict"
		c.ServerName = env.inner
		_, list, _ := pki.ECH("proxy.test")
		c.ECHConfig = base64.StdEncoding.EncodeToString(list)
		m, e := client.New(c)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, e = m.OpenTCP(ctx, env.tcp)
		cancel()
		m.Close()
		if e == nil {
			t.Fatal("wrong ECH key accepted")
		}
	}
}

func TestStreamIsolationAndConcurrency(t *testing.T) {
	env := start(t)
	for _, mode := range []string{"h2"} {
		c := env.cfg
		c.Transport = mode
		m, _ := client.New(c)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var wg sync.WaitGroup
		errc := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st, e := m.OpenTCP(ctx, env.half)
				if e != nil {
					errc <- e
					return
				}
				defer st.Close()
				st.Write([]byte("parallel"))
				st.CloseWrite()
				b, e := io.ReadAll(st)
				if e != nil || !strings.HasPrefix(string(b), "8 ") {
					errc <- fmt.Errorf("parallel stream: %s %v", b, e)
				}
			}()
		}
		wg.Wait()
		cancel()
		m.Close()
		close(errc)
		for e := range errc {
			t.Error(mode, e)
		}
	}
}

func TestSOCKSAndHTTPProxy(t *testing.T) {
	env := start(t)
	m, _ := client.New(env.cfg)
	p := client.NewProxy(m)
	if e := p.Start(); e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	proxyURL, _ := urlParse("http://" + env.cfg.HTTPListen)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 10 * time.Second}
	res, e := hc.Get("http://" + env.tcp + "/")
	if e != nil {
		t.Fatal(e)
	}
	n, e := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if e != nil || n != 1<<20 {
		t.Fatal("HTTP application proxy failed", n, e)
	}
	c, e := net.DialTimeout("tcp", env.cfg.SOCKSListen, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	c.Write([]byte{5, 1, 0})
	a := make([]byte, 2)
	io.ReadFull(c, a)
	if !bytes.Equal(a, []byte{5, 0}) {
		t.Fatal("SOCKS negotiation")
	}
	h, ps, _ := net.SplitHostPort(env.half)
	ip := net.ParseIP(h).To4()
	var port int
	fmt.Sscan(ps, &port)
	req := append([]byte{5, 1, 0, 1}, ip...)
	req = append(req, byte(port>>8), byte(port))
	c.Write(req)
	reply := make([]byte, 10)
	if _, e = io.ReadFull(c, reply); e != nil || reply[1] != 0 {
		t.Fatal("SOCKS CONNECT", reply, e)
	}
	c.Write([]byte("socks"))
	c.(*net.TCPConn).CloseWrite()
	b, e := io.ReadAll(c)
	if e != nil || !strings.HasPrefix(string(b), "5 ") {
		t.Fatal("SOCKS data", string(b), e)
	}
}

func TestSOCKSUDPAssociate(t *testing.T) {
	env := start(t)
	for _, mode := range []string{"h2"} {
		t.Run(mode, func(t *testing.T) {
			cfg := env.cfg
			cfg.Transport = mode
			m, e := client.New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			p := client.NewProxy(m)
			if e = p.Start(); e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			control, e := net.DialTimeout("tcp", cfg.SOCKSListen, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			defer control.Close()
			control.SetDeadline(time.Now().Add(5 * time.Second))
			control.Write([]byte{5, 1, 0})
			greeting := make([]byte, 2)
			io.ReadFull(control, greeting)
			control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
			reply := make([]byte, 10)
			if _, e = io.ReadFull(control, reply); e != nil || reply[1] != 0 {
				t.Fatalf("UDP associate: %x %v", reply, e)
			}
			a := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:10]))}
			u, e := net.DialUDP("udp", nil, a)
			if e != nil {
				t.Fatal(e)
			}
			defer u.Close()
			h, ps, _ := net.SplitHostPort(env.udp)
			var port int
			fmt.Sscan(ps, &port)
			packet := append([]byte{0, 0, 0, 1}, net.ParseIP(h).To4()...)
			packet = binary.BigEndian.AppendUint16(packet, uint16(port))
			packet = append(packet, bytes.Repeat([]byte{73}, 700)...)
			u.SetDeadline(time.Now().Add(5 * time.Second))
			u.Write(packet)
			b := make([]byte, 2048)
			n, e := u.Read(b)
			if e != nil || !bytes.Equal(b[:n], packet) {
				t.Fatalf("SOCKS UDP echo: n=%d error=%v", n, e)
			}
			packet[2] = 1
			u.Write(packet)
			u.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			if _, e = u.Read(b); e == nil {
				t.Fatal("fragmented SOCKS packet was forwarded")
			}
		})
	}
}

func TestIPv6TargetsAndUDPURI(t *testing.T) {
	ln, e := net.Listen("tcp6", "[::1]:0")
	if e != nil {
		t.Skip("IPv6 loopback unavailable:", e)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	u, e := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::1")})
	if e != nil {
		t.Fatal(e)
	}
	defer u.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, e := u.ReadFromUDP(b)
			if e != nil {
				return
			}
			u.WriteToUDP(b[:n], a)
		}
	}()
	env := start(t, ln.Addr().String(), u.LocalAddr().String())
	for _, mode := range []string{"h2"} {
		cfg := env.cfg
		cfg.Transport = mode
		m, e := client.New(cfg)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		st, e := m.OpenTCP(ctx, ln.Addr().String())
		if e != nil {
			t.Fatal(mode, e)
		}
		st.Write([]byte("ipv6"))
		st.CloseWrite()
		b, e := io.ReadAll(st)
		st.Close()
		if e != nil || string(b) != "ipv6" {
			t.Fatal("IPv6 CONNECT", e)
		}
		ps, e := m.OpenUDP(ctx, u.LocalAddr().String())
		if e != nil {
			t.Fatal(mode, e)
		}
		ps.Send([]byte("udp6"))
		b, e = ps.Receive(ctx)
		ps.Close()
		m.Close()
		cancel()
		if e != nil || string(b) != "udp6" {
			t.Fatal("IPv6 UDP URI", e)
		}
	}
}

func TestAuthGateBeforeTargetLookup(t *testing.T) {
	env := start(t)
	for _, test := range []struct {
		name                          string
		auth                          []string
		version, protocol, path, host string
		want                          int
	}{
		{"missing auth", nil, "1", "", "/", "unknown.invalid:443", 405},
		{"duplicate auth", []string{"Bearer " + env.cfg.Token, "Bearer " + env.cfg.Token}, "1", "", "/", "unknown.invalid:443", 405},
		{"bad version", []string{"Bearer " + env.cfg.Token}, "999", "", "/", "unknown.invalid:443", 400},
		{"invalid port", []string{"Bearer " + env.cfg.Token}, "1", "", "/", "unknown.invalid:99999", 400},
		{"unsupported protocol", []string{"Bearer " + env.cfg.Token}, "1", "other", "/", "unknown.invalid:443", 501},
		{"invalid UDP path", []string{"Bearer " + env.cfg.Token}, "1", "connect-udp", "/.well-known/masque/udp/%2F/443/", "proxy.test", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("CONNECT", "https://proxy.test"+test.path, nil)
			r.ProtoMajor = 2
			r.Host = test.host
			r.Header["Authorization"] = test.auth
			r.Header.Set("Tunnelx-Version", test.version)
			r.Header.Set(":protocol", test.protocol)
			before := env.server.TargetAttempts.Load()
			w := httptest.NewRecorder()
			env.server.ServeHTTP(w, r)
			if w.Code != test.want || env.server.TargetAttempts.Load() != before {
				t.Fatalf("status=%d target attempted=%v", w.Code, env.server.TargetAttempts.Load() != before)
			}
		})
	}
}

func TestUnsupportedHTTPBeforeTargetActivity(t *testing.T) {
	env := start(t)
	r := httptest.NewRequest("CONNECT", "https://proxy.test/", nil)
	r.Host = env.tcp
	r.Header.Set("Authorization", "Bearer "+env.cfg.Token)
	r.Header.Set("Tunnelx-Version", "1")
	w := httptest.NewRecorder()
	before := env.server.TargetAttempts.Load()
	env.server.ServeHTTP(w, r)
	if w.Code != 505 || env.server.TargetAttempts.Load() != before {
		t.Fatal("unsupported HTTP version caused target activity")
	}
	r.ProtoMajor = 3
	w = httptest.NewRecorder()
	env.server.ServeHTTP(w, r)
	if w.Code != 505 || env.server.TargetAttempts.Load() != before {
		t.Fatal("removed HTTP version caused target activity")
	}
}

func TestServerDoesNotBindUDP(t *testing.T) {
	env := start(t)
	address, e := net.ResolveUDPAddr("udp", env.server.Config.Listen)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.ListenUDP("udp", address)
	if e != nil {
		t.Fatal("server unexpectedly owns an outer UDP listener", e)
	}
	listener.Close()
}
