// tunnelx-soak performs bounded H2/ECH and byte-integrity checks against a private fixture.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
)

type sample struct {
	Time      string  `json:"time"`
	OK        bool    `json:"ok"`
	Bytes     int64   `json:"bytes"`
	ElapsedMS int64   `json:"elapsed_ms"`
	MBps      float64 `json:"mbps"`
	ECH       bool    `json:"ech"`
	Error     string  `json:"error,omitempty"`
}

func main() {
	path := flag.String("config", "client.json", "private client configuration")
	duration := flag.Duration("duration", 8*time.Minute, "bounded test duration")
	interval := flag.Duration("interval", 10*time.Second, "sample interval")
	target := flag.String("target", "127.0.0.1:18480", "explicitly allowed private fixture")
	out := flag.String("out", "network-soak.json", "public report path")
	size := flag.Int("bytes", 8<<20, "download bytes per sample")
	flag.Parse()
	if *duration < time.Second || *duration > time.Hour || *interval < time.Second || *size < 1024 || *size > 64<<20 {
		fatal("invalid test parameters")
	}
	var c config.Client
	if config.Read(*path, &c) != nil {
		fatal("private configuration unavailable")
	}
	c.CAFile = config.Resolve(*path, c.CAFile)
	m, e := client.New(c)
	if e != nil {
		fatal("client setup failed")
	}
	defer m.Close()
	started := time.Now()
	samples := []sample{}
	passed := true
	for time.Since(started) < *duration {
		start := time.Now()
		s := sample{Time: start.UTC().Format(time.RFC3339)}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		response, e := m.PublicGET(ctx)
		if e == nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			if response.StatusCode != 200 || response.ProtoMajor != 2 {
				e = fmt.Errorf("public H2 check rejected")
			}
		}
		if e == nil {
			stream, err := m.OpenTCP(ctx, *target)
			if err != nil {
				e = err
			} else {
				fmt.Fprintf(stream, "GET /download?size=%d HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n", *size)
				stream.CloseWrite()
				res, err := http.ReadResponse(bufio.NewReader(stream), nil)
				if err == nil {
					buffer := make([]byte, 64<<10)
					for {
						n, readErr := res.Body.Read(buffer)
						for i := 0; i < n; i++ {
							if buffer[i] != byte(((s.Bytes+int64(i))%(32<<10))%251) {
								err = fmt.Errorf("payload byte-integrity mismatch")
								break
							}
						}
						s.Bytes += int64(n)
						if err != nil || readErr != nil {
							if readErr != nil && readErr != io.EOF {
								err = readErr
							}
							break
						}
					}
					res.Body.Close()
					if res.StatusCode != 200 || s.Bytes != int64(*size) {
						err = fmt.Errorf("download incomplete")
					}
				}
				stream.Close()
				e = err
			}
		}
		s.ElapsedMS = time.Since(start).Milliseconds()
		s.ECH = m.LastECH.Load()
		s.OK = e == nil && (c.Privacy != "strict" || s.ECH)
		if s.ElapsedMS > 0 {
			s.MBps = float64(s.Bytes) / 1e6 / (float64(s.ElapsedMS) / 1000)
		}
		if !s.OK {
			s.Error = "connection or payload validation failed"
			passed = false
		}
		cancel()
		samples = append(samples, s)
		report := map[string]any{"passed": passed, "started": started.UTC().Format(time.RFC3339), "finished": time.Now().UTC().Format(time.RFC3339), "duration_seconds": time.Since(started).Seconds(), "server_ip": c.ServerIP, "node_port": c.Port, "transport": "h2", "sample_count": len(samples), "samples": samples, "scope": "Windows to isolated QA node; private deterministic download fixture"}
		raw, _ := json.MarshalIndent(report, "", "  ")
		os.MkdirAll(filepath.Dir(*out), 0755)
		if e = os.WriteFile(*out, append(raw, '\n'), 0644); e != nil {
			fatal("report write failed")
		}
		fmt.Printf("sample=%d ok=%v bytes=%d MB/s=%.2f ECH=%v\n", len(samples), s.OK, s.Bytes, s.MBps, s.ECH)
		remaining := *interval - time.Since(start)
		if remaining > 0 {
			time.Sleep(remaining)
		}
	}
	if !passed {
		os.Exit(1)
	}
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
