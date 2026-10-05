package check

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
)

type benchConn struct{ *client.Tunnel }

func (c benchConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c benchConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c benchConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c benchConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

// Benchmark compares equal HTTPS payloads, keeping origin identity and privacy constant.
func Benchmark(cfg config.Client, rawURL string, rounds int, direct bool, expected string, progress func(string)) Report {
	s := &Suite{Progress: progress, Report: Report{Started: time.Now().Format(time.RFC3339), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Server: cfg.ServerIP, Passed: true, Limitations: []string{"Serial single-file samples; not the fast.com browser algorithm.", "Origin is unchanged between carrier tests; local competing traffic may affect results."}}}
	u, e := url.Parse(rawURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || rounds < 1 || rounds > 10 {
		s.run("configuration", func() (map[string]any, error) { return nil, fmt.Errorf("HTTPS URL and 1..10 rounds required") })
		return s.Report
	}
	if direct {
		s.run("direct", func() (map[string]any, error) { return benchmarkDownload(cfg, u, "direct", expected) })
	}
	for i := 0; i < rounds; i++ {
		modes := []string{"h2"}
		for _, mode := range modes {
			s.run(fmt.Sprintf("%s/round-%d", mode, i+1), func() (map[string]any, error) { return benchmarkDownload(cfg, u, mode, expected) })
		}
	}
	s.Report.Finished = time.Now().Format(time.RFC3339)
	return s.Report
}

func benchmarkDownload(cfg config.Client, u *url.URL, mode, expected string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	target := net.JoinHostPort(u.Hostname(), port)
	var raw net.Conn
	var mux *client.Mux
	var e error
	if mode == "direct" {
		raw, e = (&net.Dialer{}).DialContext(ctx, "tcp", target)
	} else {
		cfg.Transport = mode
		mux, e = client.New(cfg)
		if e != nil {
			return nil, e
		}
		defer mux.Close()
		var t *client.Tunnel
		t, e = mux.OpenTCP(ctx, target)
		if e == nil {
			raw = benchConn{t}
		}
	}
	if e != nil {
		return nil, e
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	tc := tls.Client(raw, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	if e = tc.HandshakeContext(ctx); e != nil {
		return nil, e
	}
	r, e := http.NewRequest("GET", u.String(), nil)
	if e != nil {
		return nil, e
	}
	r.Close = true
	r.Header.Set("Accept-Encoding", "identity")
	if e = r.Write(tc); e != nil {
		return nil, e
	}
	res, e := http.ReadResponse(bufio.NewReader(tc), r)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("origin returned HTTP %d", res.StatusCode)
	}
	ttfb := time.Since(start).Seconds()
	bodyStart := time.Now()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(res.Body, (256<<20)+1))
	elapsed := time.Since(start).Seconds()
	hash := hex.EncodeToString(h.Sum(nil))
	if e == nil && (n == 0 || n > 256<<20 || (expected != "" && hash != expected)) {
		e = fmt.Errorf("payload length or hash mismatch")
	}
	metrics := map[string]any{"bytes": n, "sha256": hash, "goodput_mbps": float64(n) * 8 / elapsed / 1e6, "goodput_MBps": float64(n) / elapsed / 1e6, "ttfb_seconds": ttfb, "body_seconds": time.Since(bodyStart).Seconds(), "privacy": cfg.Privacy}
	if mux != nil {
		metrics["ech_accepted"] = mux.LastECH.Load()
		if cfg.Privacy == "strict" && !mux.LastECH.Load() {
			e = fmt.Errorf("ECH not accepted")
		}
	}
	return metrics, e
}
