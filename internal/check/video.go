package check

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
)

// RunVideo verifies controlled MP4 bytes and seek ranges while a bulk transfer is active.
func RunVideo(normal config.Client, strict *config.Client, wantHash string, wantSize int64, progress func(string)) Report {
	s := &Suite{Progress: progress, Report: Report{Started: time.Now().Format(time.RFC3339), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Server: normal.ServerIP, Passed: true, Limitations: []string{"Controlled 20-second H.264/AAC MP4; this checks transfer and seek integrity, not player rebuffer rate."}}}
	if b, e := hex.DecodeString(wantHash); e != nil || len(b) != 32 || wantSize < 131072 || wantSize > 32<<20 {
		s.run("video/configuration", func() (map[string]any, error) {
			return nil, fmt.Errorf("valid video SHA256 and positive size required")
		})
		s.Report.Finished = time.Now().Format(time.RFC3339)
		return s.Report
	}
	configs := []config.Client{normal}
	if strict != nil {
		configs = append(configs, *strict)
	}
	for _, cfg := range configs {
		for _, mode := range []string{"h2"} {
			c := cfg
			c.Transport = mode
			s.run(c.Privacy+"/"+mode+"/video-with-bulk-download", func() (map[string]any, error) {
				m, e := client.New(c)
				if e != nil {
					return nil, e
				}
				defer m.Close()
				var bulk map[string]any
				var bulkErr error
				var wg sync.WaitGroup
				wg.Add(1)
				go func() { defer wg.Done(); bulk, bulkErr = Download(m, 64<<20) }()
				defer wg.Wait()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				start := time.Now()
				var whole bytes.Buffer
				n, hash, _, e := videoRequest(ctx, m, "", &whole)
				if e != nil || n != wantSize || hash != wantHash {
					return nil, fmt.Errorf("video hash or length mismatch: bytes=%d error=%v", n, e)
				}
				seconds := time.Since(start).Seconds()
				var ranges []map[string]any
				for _, offset := range []int64{0, wantSize / 2} {
					begin := time.Now()
					var part bytes.Buffer
					_, _, header, e := videoRequest(ctx, m, fmt.Sprintf("bytes=%d-%d", offset, offset+65535), &part)
					if e != nil {
						return nil, e
					}
					if header != fmt.Sprintf("bytes %d-%d/%d", offset, offset+65535, wantSize) || !bytes.Equal(part.Bytes(), whole.Bytes()[offset:offset+65536]) {
						return nil, fmt.Errorf("video seek range bytes differ from verified full file")
					}
					ranges = append(ranges, map[string]any{"content_range": header, "seconds": time.Since(begin).Seconds()})
				}
				wg.Wait()
				if bulkErr != nil {
					return nil, bulkErr
				}
				if c.Privacy == "strict" && !m.LastECH.Load() {
					return nil, fmt.Errorf("ECH not accepted")
				}
				return map[string]any{"bytes": n, "sha256": hash, "video_seconds": seconds, "video_goodput_mbps": float64(n) * 8 / seconds / 1e6, "ranges": ranges, "concurrent_bulk": bulk, "ech_accepted": m.LastECH.Load()}, nil
			})
		}
	}
	s.Report.Finished = time.Now().Format(time.RFC3339)
	return s.Report
}

func videoRequest(ctx context.Context, m *client.Mux, rangeHeader string, dst io.Writer) (int64, string, string, error) {
	t, e := m.OpenTCP(ctx, "127.0.0.1:18480")
	if e != nil {
		return 0, "", "", e
	}
	defer t.Close()
	r, _ := http.NewRequest("GET", "http://fixture/video", nil)
	r.Close = true
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	if e = r.Write(t); e != nil {
		return 0, "", "", e
	}
	t.CloseWrite()
	res, e := http.ReadResponse(bufio.NewReader(t), r)
	if e != nil {
		return 0, "", "", e
	}
	defer res.Body.Close()
	wantStatus := 200
	if rangeHeader != "" {
		wantStatus = 206
	}
	if res.StatusCode != wantStatus {
		return 0, "", "", fmt.Errorf("video HTTP %d", res.StatusCode)
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(h, dst), io.LimitReader(res.Body, (32<<20)+1))
	if e == nil && n > 32<<20 {
		e = fmt.Errorf("controlled video exceeds 32MiB test limit")
	}
	if e == nil && rangeHeader != "" && (n != 65536 || res.Header.Get("Content-Range") == "") {
		e = fmt.Errorf("range length or Content-Range missing")
	}
	return n, hex.EncodeToString(h.Sum(nil)), res.Header.Get("Content-Range"), e
}
