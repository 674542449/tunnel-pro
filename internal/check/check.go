package check

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
	"tunnelx/internal/pki"
)

type Case struct {
	Name    string         `json:"name"`
	Passed  bool           `json:"passed"`
	Seconds float64        `json:"seconds"`
	Error   string         `json:"error,omitempty"`
	Metrics map[string]any `json:"metrics,omitempty"`
}
type Report struct {
	Started     string   `json:"started_at"`
	Finished    string   `json:"finished_at"`
	Platform    string   `json:"platform"`
	GoVersion   string   `json:"go_version"`
	Server      string   `json:"server"`
	Passed      bool     `json:"passed"`
	Cases       []Case   `json:"cases"`
	Limitations []string `json:"limitations"`
}
type Suite struct {
	Report   Report
	Progress func(string)
	Filter   string
}

func (s *Suite) run(name string, fn func() (map[string]any, error)) {
	if s.Filter != "" && !strings.Contains(name, s.Filter) {
		return
	}
	if s.Progress != nil {
		s.Progress("Running " + name)
	}
	start := time.Now()
	metrics, e := fn()
	c := Case{Name: name, Passed: e == nil, Seconds: time.Since(start).Seconds(), Metrics: metrics}
	if e != nil {
		c.Error = e.Error()
		s.Report.Passed = false
	}
	s.Report.Cases = append(s.Report.Cases, c)
	if s.Progress != nil {
		s.Progress(fmt.Sprintf("%s passed=%v duration=%.2fs", name, c.Passed, c.Seconds))
	}
}
func Run(normal config.Client, strict *config.Client, fixtures bool, progress func(string), filters ...string) Report {
	s := &Suite{Progress: progress, Report: Report{Started: time.Now().Format(time.RFC3339), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Server: normal.ServerIP, Passed: true, Limitations: []string{"Single client and server; not a multi-ISP or seven-day availability study.", "Synthetic file transfer and UDP echo; no player rebuffer-rate measurement.", "No proof of resistance to classification, IP blocking, or ISP QoS."}}}
	if len(filters) > 0 {
		s.Filter = filters[0]
	}
	configs := []config.Client{normal}
	if strict != nil {
		configs = append(configs, *strict)
	}
	for _, cfg := range configs {
		for _, mode := range []string{"h2"} {
			c := cfg
			c.Transport = mode
			prefix := c.Privacy + "/" + mode + "/"
			m, e := client.New(c)
			if e != nil {
				s.run(prefix+"configuration", func() (map[string]any, error) { return nil, e })
				continue
			}
			s.run(prefix+"public-TLS", func() (map[string]any, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				r, e := m.PublicGET(ctx)
				if e != nil {
					return nil, e
				}
				defer r.Body.Close()
				_, e = io.Copy(io.Discard, io.LimitReader(r.Body, 8192))
				if e == nil && r.StatusCode != 200 {
					e = fmt.Errorf("HTTP %d", r.StatusCode)
				}
				if e == nil && c.Privacy == "strict" && !m.LastECH.Load() {
					e = fmt.Errorf("ECH was not accepted")
				}
				return map[string]any{"tls_version": fmt.Sprintf("0x%x", m.TLSVersion.Load()), "cipher": tls.CipherSuiteName(uint16(m.CipherSuite.Load())), "ech_accepted": m.LastECH.Load(), "http_major": r.ProtoMajor}, e
			})
			if fixtures {
				s.run(prefix+"download-64MiB-hash", func() (map[string]any, error) { return Download(m, 64<<20) })
				s.run(prefix+"upload-half-close-16MiB", func() (map[string]any, error) { return Upload(m, 16<<20) })
				s.run(prefix+"UDP-echo", func() (map[string]any, error) { return UDPEcho(m, 20) })
				s.run(prefix+"concurrent-streams", func() (map[string]any, error) { return Concurrent(m) })
			} else {
				s.run(prefix+"private-target-denied", func() (map[string]any, error) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					st, e := m.OpenTCP(ctx, "127.0.0.1:18480")
					if st != nil {
						st.Close()
					}
					var re *client.RequestError
					if !errors.As(e, &re) || !re.Known || re.Status != 403 {
						return nil, fmt.Errorf("expected authenticated private-target denial: %v", e)
					}
					return map[string]any{"http_status": 403}, nil
				})
			}
			m.Close()
			s.run(prefix+"wrong-token-denied", func() (map[string]any, error) {
				bad := c
				bad.Token = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{123}, 32))
				b, e := client.New(bad)
				if e != nil {
					return nil, e
				}
				defer b.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, e = b.OpenTCP(ctx, "invalid-target.invalid:443")
				var re *client.RequestError
				if !errors.As(e, &re) || re.Known || re.Status != http.StatusMethodNotAllowed {
					return nil, fmt.Errorf("expected explicit unauthenticated rejection, not a transport error: %v", e)
				}
				return map[string]any{"denied": true, "http_status": re.Status}, nil
			})
		}
	}
	s.run("h2/certificate-error-no-downgrade", func() (map[string]any, error) {
		c := normal
		c.Transport = "h2"
		c.ServerName = "incorrect.tunnelx.invalid"
		m, e := client.New(c)
		if e != nil {
			return nil, e
		}
		defer m.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, e = m.PublicGET(ctx)
		if e == nil {
			return nil, fmt.Errorf("certificate error accepted or transport downgraded")
		}
		return map[string]any{"denied": true}, nil
	})
	if strict != nil {
		s.run("strict/ECH-rejection-no-downgrade", func() (map[string]any, error) {
			c := *strict
			c.Transport = "h2"
			_, list, e := pki.ECH(normal.ServerName)
			if e != nil {
				return nil, e
			}
			c.ECHConfig = base64.StdEncoding.EncodeToString(list)
			m, e := client.New(c)
			if e != nil {
				return nil, e
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, e = m.PublicGET(ctx)
			if e == nil {
				return nil, fmt.Errorf("ECH rejection accepted or downgraded")
			}
			return map[string]any{"denied": true}, nil
		})
	}
	s.Report.Finished = time.Now().Format(time.RFC3339)
	if len(s.Report.Cases) == 0 {
		s.Report.Passed = false
		s.Report.Limitations = append(s.Report.Limitations, "No case matched the requested filter; no acceptance result.")
	}
	return s.Report
}

func pattern() []byte {
	b := make([]byte, 32<<10)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}
func expectedHash(size int) string {
	h := sha256.New()
	b := pattern()
	for left := size; left > 0; {
		n := len(b)
		if left < n {
			n = left
		}
		h.Write(b[:n])
		left -= n
	}
	return hex.EncodeToString(h.Sum(nil))
}
func Download(m *client.Mux, size int) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	t, e := m.OpenTCP(ctx, "127.0.0.1:18480")
	if e != nil {
		return nil, e
	}
	defer t.Close()
	fmt.Fprintf(t, "GET /download?size=%d HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n", size)
	t.CloseWrite()
	r, e := http.ReadResponse(bufio.NewReader(t), nil)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	h := sha256.New()
	n, e := io.Copy(h, r.Body)
	actual := hex.EncodeToString(h.Sum(nil))
	seconds := time.Since(start).Seconds()
	metrics := map[string]any{"bytes": n, "sha256": actual, "duration_seconds": seconds, "goodput_mbps": float64(n) * 8 / seconds / 1e6, "carrier": t.Transport()}
	if e == nil && (int(n) != size || actual != expectedHash(size)) {
		e = fmt.Errorf("file length or SHA256 mismatch")
	}
	return metrics, e
}
func Upload(m *client.Mux, size int) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	t, e := m.OpenTCP(ctx, "127.0.0.1:18482")
	if e != nil {
		return nil, e
	}
	defer t.Close()
	b := pattern()
	for left := size; left > 0; {
		n := len(b)
		if left < n {
			n = left
		}
		if _, e = t.Write(b[:n]); e != nil {
			return nil, e
		}
		left -= n
	}
	if e = t.CloseWrite(); e != nil {
		return nil, e
	}
	reply, e := io.ReadAll(io.LimitReader(t, 256))
	want := fmt.Sprintf("%d %s", size, expectedHash(size))
	if e == nil && strings.TrimSpace(string(reply)) != want {
		e = fmt.Errorf("upload hash or half-close response mismatch")
	}
	seconds := time.Since(start).Seconds()
	return map[string]any{"bytes": size, "sha256": expectedHash(size), "duration_seconds": seconds, "goodput_mbps": float64(size) * 8 / seconds / 1e6, "carrier": t.Transport()}, e
}
func UDPEcho(m *client.Mux, count int) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, e := m.OpenUDP(ctx, "127.0.0.1:18481")
	if e != nil {
		return nil, e
	}
	defer s.Close()
	received := 0
	var rtts []float64
	for i := 0; i < count; i++ {
		b := bytes.Repeat([]byte{byte(i)}, 1000)
		start := time.Now()
		if e = s.Send(b); e != nil {
			return nil, e
		}
		receive, cc := context.WithTimeout(ctx, 2*time.Second)
		r, e := s.Receive(receive)
		cc()
		if e == nil && bytes.Equal(b, r) {
			received++
			rtts = append(rtts, time.Since(start).Seconds()*1000)
		}
	}
	sort.Float64s(rtts)
	metrics := map[string]any{"sent": count, "received": received, "loss_percent": float64(count-received) * 100 / float64(count)}
	if len(rtts) > 0 {
		metrics["median_rtt_ms"] = rtts[len(rtts)/2]
		metrics["p95_rtt_ms"] = rtts[(len(rtts)-1)*95/100]
	}
	if received != count {
		return metrics, fmt.Errorf("UDP echo received %d of %d", received, count)
	}
	return metrics, nil
}
func Concurrent(m *client.Mux) (map[string]any, error) {
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	start := time.Now()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := Download(m, 4<<20)
			if e != nil {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		return nil, e
	}
	return map[string]any{"streams": 8, "total_bytes": 32 << 20, "seconds": time.Since(start).Seconds()}, nil
}
