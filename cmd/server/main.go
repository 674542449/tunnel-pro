package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"tunnel/internal/mux"
	"tunnel/internal/proto"
	"tunnel/internal/relay"

	"github.com/gorilla/websocket"
)

type Config struct {
	Listen    string `json:"listen"`
	PSK       string `json:"psk"`
	WebRoot   string `json:"web_root"`
	APIURL    string `json:"api_url"`
	NodeID    int64  `json:"node_id"`
	ReportKey string `json:"report_key"`
}

type userTraffic struct {
	upload   atomic.Int64
	download atomic.Int64
}

type TrafficTracker struct {
	mu    sync.Mutex
	users map[string]*userTraffic
}

func NewTrafficTracker() *TrafficTracker {
	return &TrafficTracker{users: make(map[string]*userTraffic)}
}

func (t *TrafficTracker) Add(uid string, up, down int64) {
	if uid == "" {
		uid = "unknown"
	}
	t.mu.Lock()
	ut, ok := t.users[uid]
	if !ok {
		ut = &userTraffic{}
		t.users[uid] = ut
	}
	t.mu.Unlock()
	ut.upload.Add(up)
	ut.download.Add(down)
}

type trafficEntry struct {
	UserID   string `json:"user_id"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

func (t *TrafficTracker) SwapAndReset() []trafficEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	var entries []trafficEntry
	for uid, ut := range t.users {
		up := ut.upload.Swap(0)
		down := ut.download.Swap(0)
		if up > 0 || down > 0 {
			entries = append(entries, trafficEntry{UserID: uid, Upload: up, Download: down})
		}
	}
	return entries
}

type App struct {
	cfg       Config
	tracker   *TrafficTracker
	connCount atomic.Int64
	client    *http.Client
}

func NewApp(cfg Config) *App {
	return &App{
		cfg:     cfg,
		tracker: NewTrafficTracker(),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (app *App) handleStream(s *mux.Stream, target string) {
	defer s.Close()
	app.connCount.Add(1)
	defer app.connCount.Add(-1)

	remote, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		log.Printf("dial %s: %v", target, err)
		s.SendAck(proto.StatusFail)
		return
	}
	defer remote.Close()
	if tc, ok := remote.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetReadBuffer(256 * 1024)
		tc.SetWriteBuffer(256 * 1024)
	}

	s.SendAck(proto.StatusOK)
	log.Printf("stream: %s (user=%s)", target, s.UserID())

	var up, down atomic.Int64
	relay.CountingRelay(s, remote, &up, &down)
	app.tracker.Add(s.UserID(), up.Load(), down.Load())
}

func (app *App) postJSON(path string, body any) error {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", app.cfg.APIURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (app *App) startHeartbeat() {
	for {
		time.Sleep(30 * time.Second)
		err := app.postJSON("/api/node/heartbeat", map[string]any{
			"node_id":    app.cfg.NodeID,
			"report_key": app.cfg.ReportKey,
			"conn_count": app.connCount.Load(),
		})
		if err != nil {
			log.Printf("[heartbeat] %v", err)
		}
	}
}

func (app *App) startTrafficReport() {
	for {
		time.Sleep(60 * time.Second)
		entries := app.tracker.SwapAndReset()
		if len(entries) == 0 {
			continue
		}
		err := app.postJSON("/api/node/traffic", map[string]any{
			"node_id":    app.cfg.NodeID,
			"report_key": app.cfg.ReportKey,
			"entries":    entries,
		})
		if err != nil {
			log.Printf("[traffic] report failed: %v (lost %d entries)", err, len(entries))
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  mux.WsBufSize(),
	WriteBufferSize: mux.WsBufSize(),
}

func main() {
	cfgPath := flag.String("c", "server.json", "config file path")
	flag.Parse()

	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}

	app := NewApp(cfg)

	if cfg.APIURL != "" {
		go app.startHeartbeat()
		go app.startTrafficReport()
		log.Printf("reporting to %s (node_id=%d)", cfg.APIURL, cfg.NodeID)
	}

	var fileHandler http.Handler
	if cfg.WebRoot != "" {
		fileHandler = http.FileServer(http.Dir(cfg.WebRoot))
	} else {
		fileHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<!DOCTYPE html><html><head><title>Welcome</title></head><body><h1>It works!</h1><p>Server is running normally.</p></body></html>"))
		})
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) && proto.IsValidPath(r.URL.Path, cfg.PSK) {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Printf("upgrade: %v", err)
				return
			}
			defer ws.Close()

			log.Printf("mux: %s", r.RemoteAddr)
			if err := mux.ServeConn(ws, cfg.PSK, app.handleStream); err != nil {
				log.Printf("mux %s: %v", r.RemoteAddr, err)
			}
			return
		}

		fileHandler.ServeHTTP(w, r)
	})

	log.Printf("server listening on %s", cfg.Listen)
	log.Fatal(http.ListenAndServe(cfg.Listen, nil))
}
