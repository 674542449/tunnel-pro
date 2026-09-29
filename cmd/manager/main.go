package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tunnel/internal/mux"
	"tunnel/internal/proto"
	"tunnel/internal/relay"
	"tunnel/internal/socks5"

	"github.com/getlantern/systray"
	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"
)

const apiBase = "https://cloud.xiaguamail.com"
const clientVersion = "1.0.0"

var uaPool = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
}

// ---------- share link (legacy) ----------

type ServerConfig struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
	IP   string `json:"ip,omitempty"`
	PSK  string `json:"psk"`
	Path string `json:"path,omitempty"`
}

func (s ServerConfig) ShareLink() string {
	data, _ := json.Marshal(s)
	return "tunnel://" + base64.RawURLEncoding.EncodeToString(data)
}

func ParseShareLink(link string) (ServerConfig, error) {
	var cfg ServerConfig
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "tunnel://") {
		return cfg, errors.New("链接格式不对，需要 tunnel:// 开头")
	}
	raw := strings.TrimPrefix(link, "tunnel://")
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return cfg, errors.New("base64 解码失败")
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Addr == "" || cfg.PSK == "" {
		return cfg, errors.New("链接信息不完整")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Addr
	}
	return cfg, nil
}

// ---------- auth state ----------

type AuthState struct {
	Token      string          `json:"token,omitempty"`
	User       json.RawMessage `json:"user,omitempty"`
	LastServer string          `json:"last_server,omitempty"`
}

func parseJWTUID(token string) int64 {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var c struct {
		UID int64 `json:"uid"`
	}
	json.Unmarshal(data, &c)
	return c.UID
}

// ---------- connection pool ----------

type Pool struct {
	mu   sync.Mutex
	conn *mux.Mux
	dial func() (*mux.Mux, error)
}

func (p *Pool) Get() (*mux.Mux, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil && !p.conn.IsClosed() {
		return p.conn, nil
	}
	m, err := p.dial()
	if err != nil {
		return nil, err
	}
	p.conn = m
	return m, nil
}

// ---------- manager ----------

type Manager struct {
	servers    []ServerConfig
	configPath string
	authPath   string

	mu        sync.RWMutex
	activeIdx int
	pool      *Pool
	auth      AuthState

	socksAddr     string
	httpAddr      string
	mixedAddr     string
	panelAddr     string
	proxyOn       bool
	uploadBytes   atomic.Int64
	downloadBytes atomic.Int64
}

func NewManager() *Manager {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	return &Manager{
		configPath: filepath.Join(dir, "servers.json"),
		authPath:   filepath.Join(dir, "auth.json"),
		activeIdx:  -1,
		socksAddr:  "127.0.0.1:1080",
		httpAddr:   "127.0.0.1:1081",
		mixedAddr:  "127.0.0.1:1082",
		panelAddr:  "127.0.0.1:9090",
	}
}

func (m *Manager) load() {
	data, err := os.ReadFile(m.configPath)
	if err == nil {
		var app struct {
			Servers []ServerConfig `json:"servers"`
		}
		if json.Unmarshal(data, &app) == nil {
			m.servers = app.Servers
		}
	}
	data, err = os.ReadFile(m.authPath)
	if err == nil {
		json.Unmarshal(data, &m.auth)
	}
}

func (m *Manager) saveServers() {
	data, _ := json.MarshalIndent(struct {
		Servers []ServerConfig `json:"servers"`
	}{m.servers}, "", "  ")
	os.WriteFile(m.configPath, data, 0644)
}

func (m *Manager) saveAuth() {
	data, _ := json.MarshalIndent(m.auth, "", "  ")
	os.WriteFile(m.authPath, data, 0644)
}

func (m *Manager) connect(idx int) error {
	if idx < 0 || idx >= len(m.servers) {
		return errors.New("无效编号")
	}
	m.disconnect()

	cfg := m.servers[idx]
	dialFn := func() (*mux.Mux, error) {
		ws, err := dialWS(cfg)
		if err != nil {
			return nil, err
		}
		mx, err := mux.NewClientMux(ws, cfg.PSK)
		if err != nil {
			ws.Close()
			return nil, err
		}
		return mx, nil
	}

	mx, err := dialFn()
	if err != nil {
		return err
	}
	if uid := parseJWTUID(m.auth.Token); uid > 0 {
		mx.SendUserID(strconv.FormatInt(uid, 10))
	}

	m.mu.Lock()
	m.pool = &Pool{dial: dialFn, conn: mx}
	m.activeIdx = idx
	m.mu.Unlock()

	setSystemProxy(m.httpAddr)
	m.proxyOn = true

	m.auth.LastServer = m.servers[idx].Addr
	m.saveAuth()
	return nil
}

func (m *Manager) disconnect() {
	m.mu.Lock()
	if m.pool != nil {
		if m.pool.conn != nil {
			m.pool.conn.Close()
		}
		m.pool = nil
	}
	m.activeIdx = -1
	m.mu.Unlock()

	if m.proxyOn {
		clearSystemProxy()
		m.proxyOn = false
	}

	m.auth.LastServer = ""
	m.saveAuth()
}

func (m *Manager) startHealthCheck() {
	for {
		time.Sleep(5 * time.Second)
		m.mu.RLock()
		p := m.pool
		idx := m.activeIdx
		m.mu.RUnlock()

		if p == nil || idx < 0 {
			continue
		}

		p.mu.Lock()
		conn := p.conn
		p.mu.Unlock()

		if conn == nil || !conn.IsClosed() {
			continue
		}

		log.Println("[health] 连接断开，自动重连...")
		ok := false
		for retry := 0; retry < 5; retry++ {
			if retry > 0 {
				time.Sleep(time.Duration(2<<retry) * time.Second)
			}
			mx, err := p.dial()
			if err != nil {
				log.Printf("[health] 重连失败(%d/5): %v", retry+1, err)
				continue
			}
			p.mu.Lock()
			p.conn = mx
			p.mu.Unlock()
			log.Println("[health] 重连成功")
			ok = true
			break
		}
		if !ok {
			log.Println("[health] 重连失败，已断开")
			m.disconnect()
		}
	}
}

func (m *Manager) openStream(host string, port uint16) (*mux.Stream, error) {
	m.mu.RLock()
	p := m.pool
	m.mu.RUnlock()
	if p == nil {
		return nil, errors.New("未连接")
	}
	for i := 0; i < 2; i++ {
		mx, err := p.Get()
		if err != nil {
			if i == 0 {
				continue
			}
			return nil, err
		}
		s, err := mx.OpenStream(host, port)
		if err != nil {
			continue
		}
		return s, nil
	}
	return nil, errors.New("隧道不可用")
}

// ---------- proxy handlers ----------

func (m *Manager) startSOCKS5() {
	s := &socks5.Server{
		Addr: m.socksAddr,
		OnConnect: func(conn net.Conn, host string, port uint16) {
			defer conn.Close()
			stream, err := m.openStream(host, port)
			if err != nil {
				socks5.ReplyFailure(conn)
				return
			}
			defer stream.Close()
			socks5.ReplySuccess(conn)
			relay.CountingRelay(stream, conn, &m.downloadBytes, &m.uploadBytes)
		},
	}
	if err := s.ListenAndServe(); err != nil {
		log.Fatalf("[socks5] %v", err)
	}
}

func (m *Manager) startHTTP() {
	ln, err := net.Listen("tcp", m.httpAddr)
	if err != nil {
		log.Fatalf("[http] %v", err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go m.handleHTTP(c)
	}
}

func (m *Manager) handleHTTP(conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		host, portStr, err := net.SplitHostPort(req.Host)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		port, _ := strconv.Atoi(portStr)
		stream, err := m.openStream(host, uint16(port))
		if err != nil {
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer stream.Close()
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		relay.CountingRelay(stream, conn, &m.downloadBytes, &m.uploadBytes)
		return
	}

	host := req.URL.Hostname()
	port := uint16(80)
	if req.URL.Port() != "" {
		p, _ := strconv.Atoi(req.URL.Port())
		port = uint16(p)
	}
	stream, err := m.openStream(host, port)
	if err != nil {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer stream.Close()

	req.RequestURI = req.URL.RequestURI()
	req.Header.Del("Proxy-Connection")
	var buf bytes.Buffer
	req.Write(&buf)
	stream.Write(buf.Bytes())
	relay.CountingRelay(stream, conn, &m.downloadBytes, &m.uploadBytes)
}

// ---------- mixed port (auto-detect SOCKS5 / HTTP) ----------

type peekConn struct {
	net.Conn
	peeked []byte
	idx    int
}

func (c *peekConn) Read(b []byte) (int, error) {
	if c.idx < len(c.peeked) {
		n := copy(b, c.peeked[c.idx:])
		c.idx += n
		return n, nil
	}
	return c.Conn.Read(b)
}

func (m *Manager) startMixed() {
	ln, err := net.Listen("tcp", m.mixedAddr)
	if err != nil {
		log.Fatalf("[mixed] %v", err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go m.handleMixed(c)
	}
}

func (m *Manager) handleMixed(conn net.Conn) {
	first := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := conn.Read(first)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	wrapped := &peekConn{Conn: conn, peeked: first}
	if first[0] == 0x05 {
		socks5.HandleConn(wrapped, func(c net.Conn, host string, port uint16) {
			defer c.Close()
			stream, err := m.openStream(host, port)
			if err != nil {
				socks5.ReplyFailure(c)
				return
			}
			defer stream.Close()
			socks5.ReplySuccess(c)
			relay.CountingRelay(stream, c, &m.downloadBytes, &m.uploadBytes)
		})
	} else {
		m.handleHTTP(wrapped)
	}
}

// ---------- system proxy ----------

func setSystemProxy(addr string) {
	regPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	run := func(args ...string) { exec.Command("reg", args...).Run() }
	run("add", regPath, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", "", "/f")
	run("add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	run("add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", addr, "/f")
	run("add", regPath, "/v", "ProxyOverride", "/t", "REG_SZ", "/d",
		"localhost;127.*;10.*;192.168.*;<local>", "/f")
	refreshProxy()
}

func clearSystemProxy() {
	regPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	refreshProxy()
}

func refreshProxy() {
	wininet := syscall.NewLazyDLL("wininet.dll")
	set := wininet.NewProc("InternetSetOptionW")
	set.Call(0, 39, 0, 0)
	set.Call(0, 37, 0, 0)
}

// ---------- websocket dial ----------

func dialWS(cfg ServerConfig) (*websocket.Conn, error) {
	wsPath := cfg.Path
	if wsPath == "" {
		wsPath = proto.DynamicPath(cfg.PSK, 0)
	}
	remoteURL := "wss://" + cfg.Addr + wsPath
	u, _ := url.Parse(remoteURL)
	serverHost := u.Hostname()
	serverPort := u.Port()
	if serverPort == "" {
		serverPort = "443"
	}

	dialAddr := serverHost + ":" + serverPort
	if cfg.IP != "" {
		dialAddr = cfg.IP + ":" + serverPort
	}

	dialer := &websocket.Dialer{
		Proxy:            func(*http.Request) (*url.URL, error) { return nil, nil },
		HandshakeTimeout: 15 * time.Second,
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			tcpConn, err := net.DialTimeout(network, dialAddr, 15*time.Second)
			if err != nil {
				return nil, err
			}
			tlsCfg := &utls.Config{ServerName: serverHost}
			spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
			if err != nil {
				tcpConn.Close()
				return nil, err
			}
			for _, ext := range spec.Extensions {
				if alpn, ok := ext.(*utls.ALPNExtension); ok {
					alpn.AlpnProtocols = []string{"http/1.1"}
					break
				}
			}
			tlsConn := utls.UClient(tcpConn, tlsCfg, utls.HelloCustom)
			if err := tlsConn.ApplyPreset(&spec); err != nil {
				tcpConn.Close()
				return nil, err
			}
			if err := tlsConn.Handshake(); err != nil {
				tcpConn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}

	headers := http.Header{}
	headers.Set("User-Agent", uaPool[rand.Intn(len(uaPool))])

	ws, _, err := dialer.Dial(remoteURL, headers)
	return ws, err
}

// ---------- API proxy ----------

func (m *Manager) proxyAPI(w http.ResponseWriter, r *http.Request) {
	target := apiBase + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	var bodyData []byte
	if r.Body != nil {
		bodyData, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(r.Method, target, bytes.NewReader(bodyData))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := client.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		json.NewEncoder(w).Encode(map[string]string{"error": "无法连接服务器: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ---------- local panel APIs ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (m *Manager) apiStatus(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	svrs := m.servers
	if svrs == nil {
		svrs = []ServerConfig{}
	}
	connected := false
	if m.pool != nil {
		m.pool.mu.Lock()
		connected = m.pool.conn != nil && !m.pool.conn.IsClosed()
		m.pool.mu.Unlock()
	}
	writeJSON(w, map[string]any{
		"servers":    svrs,
		"active":     m.activeIdx,
		"connected":  connected,
		"socks_addr": m.socksAddr,
		"http_addr":  m.httpAddr,
		"mixed_addr": m.mixedAddr,
		"proxy_on":   m.proxyOn,
		"auth":       m.auth,
	})
}

func (m *Manager) apiSetAuth(w http.ResponseWriter, r *http.Request) {
	var body AuthState
	json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	m.auth = body
	m.saveAuth()
	m.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (m *Manager) apiLogout(w http.ResponseWriter, r *http.Request) {
	m.disconnect()
	m.mu.Lock()
	m.auth = AuthState{}
	m.servers = nil
	m.saveAuth()
	m.saveServers()
	m.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (m *Manager) apiSyncNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Nodes []struct {
			Name   string `json:"name"`
			Addr   string `json:"addr"`
			IP     string `json:"ip"`
			PSK    string `json:"psk"`
			Region string `json:"region"`
		} `json:"nodes"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	m.mu.Lock()
	m.servers = nil
	for _, n := range body.Nodes {
		m.servers = append(m.servers, ServerConfig{
			Name: n.Name,
			Addr: n.Addr,
			IP:   n.IP,
			PSK:  n.PSK,
		})
	}
	m.saveServers()
	m.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (m *Manager) apiConnect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, "无效编号")
		return
	}

	m.mu.RLock()
	if id < 0 || id >= len(m.servers) {
		m.mu.RUnlock()
		writeError(w, "无效编号")
		return
	}
	name := m.servers[id].Name
	m.mu.RUnlock()

	log.Printf("[panel] 连接: %s ...", name)
	if err := m.connect(id); err != nil {
		log.Printf("[panel] 连接失败: %v", err)
		writeError(w, err.Error())
		return
	}
	log.Printf("[panel] 已连接: %s", name)
	writeJSON(w, map[string]bool{"ok": true})
}

func (m *Manager) apiDisconnect(w http.ResponseWriter, r *http.Request) {
	m.disconnect()
	log.Println("[panel] 已断开")
	writeJSON(w, map[string]bool{"ok": true})
}

func (m *Manager) apiTest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, "无效编号")
		return
	}

	m.mu.RLock()
	if id < 0 || id >= len(m.servers) {
		m.mu.RUnlock()
		writeError(w, "无效编号")
		return
	}
	cfg := m.servers[id]
	m.mu.RUnlock()

	addr := cfg.Addr + ":443"
	if cfg.IP != "" {
		addr = cfg.IP + ":443"
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		writeError(w, "连接超时")
		return
	}
	latency := time.Since(start).Milliseconds()
	conn.Close()

	writeJSON(w, map[string]int64{"latency": latency})
}

func (m *Manager) apiTestAll(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	svrs := make([]ServerConfig, len(m.servers))
	copy(svrs, m.servers)
	m.mu.RUnlock()

	type Result struct {
		ID      int   `json:"id"`
		Latency int64 `json:"latency"`
		OK      bool  `json:"ok"`
	}

	results := make([]Result, len(svrs))
	var wg sync.WaitGroup
	for i, cfg := range svrs {
		wg.Add(1)
		go func(i int, cfg ServerConfig) {
			defer wg.Done()
			addr := cfg.Addr + ":443"
			if cfg.IP != "" {
				addr = cfg.IP + ":443"
			}
			start := time.Now()
			conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				results[i] = Result{ID: i, OK: false}
				return
			}
			latency := time.Since(start).Milliseconds()
			conn.Close()
			results[i] = Result{ID: i, Latency: latency, OK: true}
		}(i, cfg)
	}
	wg.Wait()
	writeJSON(w, map[string]any{"results": results})
}

func (m *Manager) apiSpeed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]int64{
		"upload":   m.uploadBytes.Load(),
		"download": m.downloadBytes.Load(),
	})
}

func isAutoStart() bool {
	out, _ := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", "Tunnel").Output()
	return strings.Contains(string(out), "Tunnel")
}

func setAutoStart(enable bool) error {
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	if enable {
		exe, _ := os.Executable()
		return exec.Command("reg", "add", key, "/v", "Tunnel",
			"/t", "REG_SZ", "/d", exe, "/f").Run()
	}
	return exec.Command("reg", "delete", key, "/v", "Tunnel", "/f").Run()
}

func (m *Manager) apiAutoStart(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, map[string]bool{"enabled": isAutoStart()})
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := setAutoStart(body.Enabled); err != nil {
		writeError(w, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---------- web panel ----------

func (m *Manager) startPanel() {
	pmux := http.NewServeMux()

	pmux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(pageHTML))
	})
	pmux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write([]byte(faviconSVG))
	})

	pmux.HandleFunc("GET /local/status", m.apiStatus)
	pmux.HandleFunc("POST /local/auth", m.apiSetAuth)
	pmux.HandleFunc("POST /local/logout", m.apiLogout)
	pmux.HandleFunc("POST /local/sync-nodes", m.apiSyncNodes)
	pmux.HandleFunc("POST /local/connect", m.apiConnect)
	pmux.HandleFunc("POST /local/disconnect", m.apiDisconnect)
	pmux.HandleFunc("POST /local/test", m.apiTest)
	pmux.HandleFunc("POST /local/test-all", m.apiTestAll)
	pmux.HandleFunc("GET /local/speed", m.apiSpeed)
	pmux.HandleFunc("GET /local/autostart", m.apiAutoStart)
	pmux.HandleFunc("POST /local/autostart", m.apiAutoStart)
	pmux.HandleFunc("GET /local/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": clientVersion})
	})

	pmux.HandleFunc("/api/", m.proxyAPI)

	ln, err := net.Listen("tcp", m.panelAddr)
	if err != nil {
		log.Fatalf("[panel] %v", err)
	}
	log.Printf("[panel] http://%s", m.panelAddr)
	go http.Serve(ln, pmux)
}

// ---------- main ----------

func generateTrayIcon() []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	center := float64(size-1) / 2
	radius := float64(size) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center
			dy := float64(y) - center
			if math.Sqrt(dx*dx+dy*dy) <= radius {
				t := float64(x) / float64(size)
				r := uint8(99 + t*69)
				g := uint8(102 - t*47)
				b := uint8(241)
				img.Set(x, y, color.NRGBA{r, g, b, 255})
			}
		}
	}
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)
	pngData := pngBuf.Bytes()

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	ico.WriteByte(32)
	ico.WriteByte(32)
	ico.WriteByte(0)
	ico.WriteByte(0)
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	binary.Write(&ico, binary.LittleEndian, uint16(32))
	binary.Write(&ico, binary.LittleEndian, uint32(len(pngData)))
	binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(pngData)
	return ico.Bytes()
}

func main() {
	log.SetFlags(log.Ltime)

	clearSystemProxy()

	mgr := NewManager()
	mgr.load()

	go mgr.startSOCKS5()
	go mgr.startHTTP()
	go mgr.startMixed()
	go mgr.startHealthCheck()
	mgr.startPanel()

	if mgr.auth.Token != "" && mgr.auth.LastServer != "" {
		for i, s := range mgr.servers {
			if s.Addr == mgr.auth.LastServer {
				log.Printf("[auto] 连接上次节点: %s", s.Name)
				if err := mgr.connect(i); err != nil {
					log.Printf("[auto] 自动连接失败: %v", err)
				}
				break
			}
		}
	}

	exec.Command("cmd", "/c", "start", "http://"+mgr.panelAddr).Start()

	log.Println("Tunnel Manager running")
	log.Printf("  管理面板: http://%s", mgr.panelAddr)

	systray.Run(func() {
		systray.SetIcon(generateTrayIcon())
		systray.SetTooltip("Tunnel")

		mOpen := systray.AddMenuItem("打开面板", "在浏览器中打开管理面板")
		systray.AddSeparator()
		mStatus := systray.AddMenuItem("未连接", "当前连接状态")
		mStatus.Disable()
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "退出 Tunnel")

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				mgr.mu.RLock()
				idx := mgr.activeIdx
				var name string
				connected := false
				if idx >= 0 && idx < len(mgr.servers) {
					name = mgr.servers[idx].Name
					if mgr.pool != nil {
						mgr.pool.mu.Lock()
						connected = mgr.pool.conn != nil && !mgr.pool.conn.IsClosed()
						mgr.pool.mu.Unlock()
					}
				}
				mgr.mu.RUnlock()
				if connected {
					mStatus.SetTitle("已连接: " + name)
				} else {
					mStatus.SetTitle("未连接")
				}
			}
		}()

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					exec.Command("cmd", "/c", "start", "http://"+mgr.panelAddr).Start()
				case <-mQuit.ClickedCh:
					systray.Quit()
				}
			}
		}()
	}, func() {
		log.Println("正在退出...")
		mgr.disconnect()
	})
}

// ---------- embedded assets ----------

const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><defs><linearGradient id="g" x1="0" y1="0" x2="48" y2="48" gradientUnits="userSpaceOnUse"><stop stop-color="#6366f1"/><stop offset="1" stop-color="#a855f7"/></linearGradient></defs><rect width="48" height="48" rx="12" fill="url(#g)"/><path d="M16 24h18M29 18l7 6-7 6" fill="none" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/><circle cx="24" cy="24" r="11" fill="none" stroke="#fff" stroke-width="2.5" opacity=".3"/></svg>`

const pageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<title>Tunnel</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#0c0c1d;color:#e2e8f0;min-height:100vh}
.app{max-width:520px;margin:0 auto;padding:24px 16px 40px}
.header{display:flex;align-items:center;justify-content:space-between;margin-bottom:24px}
.logo{display:flex;align-items:center;gap:10px}
.logo svg{width:38px;height:38px}
.logo h1{font-size:22px;font-weight:700;background:linear-gradient(135deg,#6366f1,#a855f7);-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}
.status{display:flex;align-items:center;gap:7px;font-size:13px;color:#64748b;padding:6px 14px;border-radius:20px;background:#1a1a2e;border:1px solid #2a2a45}
.dot{width:8px;height:8px;border-radius:50%;background:#475569;transition:all .3s}
.status.on .dot{background:#22c55e;box-shadow:0 0 8px #22c55e80}
.status.on{color:#22c55e;border-color:#22c55e30}
.status.reconnecting .dot{background:#f59e0b;box-shadow:0 0 8px #f59e0b80;animation:pulse 1.5s infinite}
.status.reconnecting{color:#f59e0b;border-color:#f59e0b30}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.4}}

/* auth screens */
.auth-screen{max-width:380px;margin:60px auto 0;text-align:center}
.auth-screen h2{font-size:20px;margin-bottom:8px;background:linear-gradient(135deg,#6366f1,#a855f7);-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}
.auth-screen p{font-size:13px;color:#64748b;margin-bottom:24px}
.auth-form{display:flex;flex-direction:column;gap:12px}
.auth-form input{background:#1a1a2e;border:1px solid #2a2a45;border-radius:10px;padding:12px 16px;color:#e2e8f0;font-size:14px;outline:none;font-family:inherit;transition:border-color .2s}
.auth-form input:focus{border-color:#6366f1}
.auth-form input::placeholder{color:#475569}
.auth-tabs{display:flex;gap:4px;margin-bottom:20px;background:#1a1a2e;border-radius:10px;padding:4px}
.auth-tab{flex:1;padding:8px;border:none;border-radius:8px;background:transparent;color:#94a3b8;cursor:pointer;font-size:13px;font-family:inherit;transition:all .2s}
.auth-tab.active{background:#6366f1;color:#fff}
.btn-main{background:#6366f1;color:#fff;border:none;border-radius:10px;padding:12px;font-size:14px;font-weight:600;cursor:pointer;transition:all .2s;font-family:inherit;width:100%}
.btn-main:hover{background:#818cf8}
.btn-main:disabled{opacity:.5;cursor:not-allowed}
.btn-ghost{background:transparent;border:1px solid #2a2a45;color:#94a3b8;border-radius:10px;padding:10px;font-size:13px;cursor:pointer;transition:all .2s;font-family:inherit;width:100%}
.btn-ghost:hover{border-color:#6366f1;color:#a78bfa}
.divider{display:flex;align-items:center;gap:12px;margin:16px 0;color:#334155;font-size:12px}
.divider::before,.divider::after{content:"";flex:1;height:1px;background:#1e293b}

/* user bar */
.user-bar{background:#1a1a2e;border:1px solid #2a2a45;border-radius:12px;padding:14px 18px;margin-bottom:16px;display:flex;justify-content:space-between;align-items:center}
.user-info{font-size:13px}
.user-email{color:#e2e8f0;font-weight:500}
.user-expire{color:#64748b;font-size:12px;margin-top:2px}
.badge-active{color:#22c55e;background:#22c55e15;padding:2px 8px;border-radius:4px;font-size:11px;font-weight:600}
.badge-expired{color:#ef4444;background:#ef444415;padding:2px 8px;border-radius:4px;font-size:11px;font-weight:600}
.badge-trial{color:#f59e0b;background:#f59e0b15;padding:2px 8px;border-radius:4px;font-size:11px;font-weight:600}
.user-btns{display:flex;gap:8px;align-items:center}
.btn-sm{padding:6px 14px;border:none;border-radius:6px;font-size:12px;cursor:pointer;font-family:inherit;transition:all .2s}
.btn-trial{background:#f59e0b20;color:#f59e0b;border:1px solid #f59e0b40}
.btn-trial:hover{background:#f59e0b35}
.btn-logout{background:#1e293b;color:#94a3b8;border:1px solid #33415540}
.btn-logout:hover{color:#ef4444;border-color:#ef444440}

/* paywall */
.paywall{background:linear-gradient(135deg,#1a1a2e,#1e1b4b);border:1px solid #6366f130;border-radius:14px;padding:24px;text-align:center;margin-bottom:20px}
.paywall h3{font-size:16px;margin-bottom:8px}
.paywall p{font-size:13px;color:#94a3b8;margin-bottom:16px}
.paywall-plans{display:flex;gap:8px;margin-bottom:16px}
.plan-card{flex:1;background:#0c0c1d;border:1px solid #2a2a45;border-radius:10px;padding:12px;cursor:pointer;transition:all .2s;text-align:center}
.plan-card:hover,.plan-card.sel{border-color:#6366f1}
.plan-card .plan-name{font-size:13px;color:#94a3b8;margin-bottom:4px}
.plan-card .plan-price{font-size:20px;font-weight:700;color:#6366f1}
.plan-card .plan-unit{font-size:11px;color:#64748b}

/* conn info */
.conn-info{background:linear-gradient(135deg,#1a1a2e,#1e1b4b);border:1px solid #2a2a45;border-radius:12px;padding:14px 18px;margin-bottom:20px;display:none}
.conn-info.show{display:block}
.conn-row{display:flex;justify-content:space-between;padding:3px 0;font-size:13px;color:#94a3b8}
.conn-row span:last-child{color:#e2e8f0;font-family:"Cascadia Code","Fira Code",monospace;font-size:12px}

/* server card */
.server{background:#1a1a2e;border:1px solid #2a2a45;border-radius:14px;padding:18px;margin-bottom:12px;transition:all .3s}
.server.active{border-color:#6366f150;background:linear-gradient(135deg,#1a1a2e,#1e1b4b)}
.server-top{display:flex;align-items:center;gap:10px;margin-bottom:4px}
.server-dot{width:10px;height:10px;border-radius:50%;background:#475569;flex-shrink:0;transition:all .3s}
.server.active .server-dot{background:#22c55e;box-shadow:0 0 10px #22c55e80}
.server-name{font-size:16px;font-weight:600;flex:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.server-latency{font-size:13px;color:#22c55e;font-weight:600;font-family:monospace}
.server-region{font-size:12px;color:#a78bfa;background:#6366f115;padding:1px 6px;border-radius:4px;margin-left:4px}
.server-addr{font-size:13px;color:#64748b;margin-bottom:14px;padding-left:20px}
.server-btns{display:flex;gap:8px}
.server-btns button{flex:1;padding:8px 0;border:none;border-radius:8px;cursor:pointer;font-size:13px;font-weight:500;transition:all .2s;font-family:inherit}
.server-btns button:disabled{opacity:.5;cursor:not-allowed}
.btn-connect{background:#6366f1;color:#fff}
.btn-connect:hover:not(:disabled){background:#818cf8}
.btn-disconnect{background:#ef444420;color:#ef4444;border:1px solid #ef444440!important}
.btn-disconnect:hover:not(:disabled){background:#ef444435}
.btn-test{background:#1e293b;color:#94a3b8;border:1px solid #334155!important}
.btn-test:hover:not(:disabled){background:#334155;color:#e2e8f0}

.toolbar{display:flex;gap:10px;margin-bottom:14px;align-items:center}
.btn-refresh{background:#1e293b;color:#94a3b8;border:1px solid #334155;border-radius:8px;padding:8px 14px;font-size:12px;cursor:pointer;font-family:inherit;transition:all .2s}
.btn-refresh:hover{background:#334155;color:#e2e8f0}
.btn-auto{background:#6366f120;color:#a78bfa;border:1px solid #6366f140;border-radius:8px;padding:8px 14px;font-size:12px;cursor:pointer;font-family:inherit;transition:all .2s}
.btn-auto:hover{background:#6366f135}

.empty{text-align:center;padding:50px 20px;color:#475569}
.empty p{font-size:14px;margin-bottom:6px}

/* toast */
.toast{position:fixed;top:24px;left:50%;transform:translateX(-50%) translateY(-80px);background:#1e293b;border:1px solid #334155;color:#e2e8f0;padding:10px 22px;border-radius:12px;font-size:14px;transition:transform .35s cubic-bezier(.175,.885,.32,1.275);z-index:2000;white-space:nowrap;box-shadow:0 8px 30px rgba(0,0,0,.3)}
.toast.show{transform:translateX(-50%) translateY(0)}
.toast.error{border-color:#ef4444;color:#ef4444}
.toast.success{border-color:#22c55e;color:#22c55e}

.footer{text-align:center;margin-top:32px;font-size:12px;color:#334155}
</style>
</head>
<body>
<div class="app">
  <div class="header">
    <div class="logo">
      <svg viewBox="0 0 48 48" xmlns="http://www.w3.org/2000/svg"><defs><linearGradient id="lg" x1="0" y1="0" x2="48" y2="48" gradientUnits="userSpaceOnUse"><stop stop-color="#6366f1"/><stop offset="1" stop-color="#a855f7"/></linearGradient></defs><rect width="48" height="48" rx="12" fill="url(#lg)"/><path d="M16 24h18M29 18l7 6-7 6" fill="none" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/><circle cx="24" cy="24" r="11" fill="none" stroke="#fff" stroke-width="2.5" opacity=".3"/></svg>
      <h1>Tunnel</h1>
    </div>
    <div class="status" id="status">
      <span class="dot"></span>
      <span id="statusText"></span>
    </div>
  </div>

  <div id="authScreen" class="auth-screen" style="display:none">
    <h2>欢迎使用 Tunnel</h2>
    <p>安全、快速的网络加速工具</p>
    <div class="auth-tabs">
      <button class="auth-tab active" onclick="switchAuth('login',this)">登录</button>
      <button class="auth-tab" onclick="switchAuth('register',this)">注册</button>
    </div>
    <div class="auth-form" id="authForm">
      <input id="authEmail" type="email" placeholder="邮箱地址">
      <input id="authPass" type="password" placeholder="密码（至少6位）">
      <button class="btn-main" id="authBtn" onclick="doAuth()">登录</button>
    </div>
    <div class="divider">或</div>
    <button class="btn-ghost" onclick="doGuest()">游客模式（机器绑定）</button>
  </div>

  <div id="mainScreen" style="display:none">
    <div id="updateBanner" style="display:none;background:linear-gradient(135deg,#6366f1,#a855f7);border-radius:12px;padding:12px 16px;margin-bottom:14px;cursor:pointer" onclick="if(this.dataset.url)window.open(this.dataset.url)">
      <div style="color:#fff;font-size:14px;font-weight:600" id="updateText"></div>
      <div style="color:#e2e8f0;font-size:12px;margin-top:4px" id="updateLog"></div>
    </div>
    <div class="user-bar" id="userBar">
      <div class="user-info">
        <div class="user-email" id="userEmail"></div>
        <div class="user-expire" id="userExpire"></div>
      </div>
      <div class="user-btns">
        <span id="userBadge"></span>
        <button class="btn-sm btn-trial" id="trialBtn" onclick="doTrial()" style="display:none">免费试用1小时</button>
        <button class="btn-sm btn-logout" onclick="doLogout()">退出</button>
      </div>
    </div>

    <div id="paywall" class="paywall" style="display:none">
      <h3>套餐已到期</h3>
      <p>选择套餐续费后继续使用</p>
      <div id="plans" class="paywall-plans"></div>
      <button class="btn-main" id="buyBtn" onclick="doBuy()" style="margin-bottom:10px;display:none">立即购买</button>
      <p id="buyMsg" style="font-size:12px;color:#475569"></p>
    </div>

    <div class="conn-info" id="connInfo">
      <div class="conn-row" id="speedRow" style="display:none"><span>实时速度</span><span>↑ <span id="speedUp">0 B/s</span>  ↓ <span id="speedDown">0 B/s</span></span></div>
      <div class="conn-row" id="trafficRow" style="display:none"><span>会话流量</span><span>↑ <span id="totalUp">0 B</span>  ↓ <span id="totalDown">0 B</span></span></div>
      <div class="conn-row"><span>SOCKS5</span><span id="infoSocks"></span></div>
      <div class="conn-row"><span>HTTP</span><span id="infoHTTP"></span></div>
      <div class="conn-row"><span>混合端口</span><span id="infoMixed"></span></div>
      <div class="conn-row"><span>系统代理</span><span id="infoProxy"></span></div>
      <div class="conn-row"><span>开机自启</span><span><label style="cursor:pointer"><input type="checkbox" id="autoStartCb" onchange="toggleAutoStart(this.checked)" style="accent-color:#6366f1"> 启用</label></span></div>
    </div>

    <div class="toolbar" id="toolbar" style="display:none">
      <button class="btn-refresh" onclick="refreshNodes()">刷新节点</button>
      <button class="btn-auto" onclick="autoConnect()">智能连接</button>
      <span style="flex:1"></span>
      <span id="nodeCount" style="font-size:12px;color:#64748b"></span>
    </div>
    <div id="servers"></div>
  </div>

  <div class="footer">Tunnel v2.0</div>
</div>

<div class="toast" id="toast"></div>

<script>
var S={servers:[],active:-1,auth:{}};
var lats={},loading={},testing={};
var authMode="login";
var userInfo=null;

function $(id){return document.getElementById(id)}
function esc(s){var d=document.createElement("div");d.textContent=s;return d.innerHTML}
function toast(m,t){var e=$("toast");e.textContent=m;e.className="toast show "+(t||"");clearTimeout(toast.t);toast.t=setTimeout(function(){e.className="toast"},2500)}

function switchAuth(mode,btn){
  authMode=mode;
  document.querySelectorAll(".auth-tab").forEach(function(t){t.classList.remove("active")});
  btn.classList.add("active");
  $("authBtn").textContent=mode==="login"?"登录":"注册";
}

function getMachineId(){
  var id=localStorage.getItem("machine_id");
  if(!id){id="win_"+Math.random().toString(36).substr(2)+Date.now().toString(36);localStorage.setItem("machine_id",id)}
  return id
}

function apiCall(path,opts){
  opts=opts||{};
  var h=Object.assign({"Content-Type":"application/json"},opts.headers||{});
  if(S.auth&&S.auth.token)h["Authorization"]="Bearer "+S.auth.token;
  return fetch(path,Object.assign({},opts,{headers:h})).then(function(r){return r.json()})
}

function doAuth(){
  var email=$("authEmail").value.trim();
  var pass=$("authPass").value;
  if(!email||!pass){toast("请填写邮箱和密码","error");return}
  $("authBtn").disabled=true;
  var endpoint=authMode==="login"?"/api/login":"/api/register";
  apiCall(endpoint,{method:"POST",body:JSON.stringify({email:email,password:pass})})
  .then(function(d){
    $("authBtn").disabled=false;
    if(d.error){toast(d.error,"error");return}
    saveAuth(d.token,d.user);
    toast(authMode==="login"?"登录成功":"注册成功","success");
    init()
  }).catch(function(){$("authBtn").disabled=false;toast("网络错误","error")})
}

function doGuest(){
  apiCall("/api/guest",{method:"POST",body:JSON.stringify({machine_id:getMachineId()})})
  .then(function(d){
    if(d.error){toast(d.error,"error");return}
    saveAuth(d.token,d.user);
    toast("已进入游客模式","success");
    init()
  }).catch(function(){toast("网络错误","error")})
}

function saveAuth(token,user){
  S.auth={token:token,user:JSON.stringify(user)};
  userInfo=user;
  fetch("/local/auth",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(S.auth)})
}

function doTrial(){
  apiCall("/api/trial",{method:"POST"}).then(function(d){
    if(d.error){toast(d.error,"error");return}
    userInfo=d.user;
    S.auth.user=JSON.stringify(d.user);
    fetch("/local/auth",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(S.auth)});
    toast("试用已激活！有效期1小时","success");
    renderUser();
    refreshNodes()
  }).catch(function(){toast("网络错误","error")})
}

function doLogout(){
  fetch("/local/logout",{method:"POST"});
  S.auth={};userInfo=null;
  init()
}

function refreshNodes(){
  apiCall("/api/nodes").then(function(d){
    if(d.error){toast(d.error,"error");return}
    fetch("/local/sync-nodes",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({nodes:d.nodes})})
    .then(function(){loadStatus()});
    toast("节点已更新","success")
  }).catch(function(){toast("获取节点失败","error")})
}

function refreshUserInfo(){
  apiCall("/api/me").then(function(d){
    if(d.error)return;
    userInfo=d.user;
    S.auth.user=JSON.stringify(d.user);
    fetch("/local/auth",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(S.auth)});
    renderUser()
  })
}

function autoConnect(){
  if(!S.servers||S.servers.length===0){toast("暂无节点","error");return}
  toast("正在测速选择最佳节点...","");
  fetch("/local/test-all",{method:"POST"}).then(function(r){return r.json()}).then(function(d){
    var results=d.results||[];
    var best=-1,bestLat=99999;
    for(var i=0;i<results.length;i++){
      if(results[i].ok){
        lats[results[i].id]=results[i].latency;
        if(results[i].latency<bestLat){bestLat=results[i].latency;best=results[i].id}
      }
    }
    render();
    if(best>=0){
      toast("最佳节点: "+S.servers[best].name+" ("+bestLat+"ms)","success");
      doConnect(best)
    }else{toast("所有节点不可用","error")}
  }).catch(function(){toast("测速失败","error")})
}

function loadStatus(){
  fetch("/local/status").then(function(r){return r.json()}).then(function(d){
    S=d;
    if(S.auth&&S.auth.user){
      try{userInfo=JSON.parse(S.auth.user)}catch(e){}
    }
    render()
  }).catch(function(){})
}

function renderUser(){
  if(!userInfo)return;
  var email=userInfo.email||"游客";
  $("userEmail").textContent=email;

  var now=Math.floor(Date.now()/1000);
  var active=userInfo.expires_at>now;
  var exp=userInfo.expires_at;

  if(exp>0){
    var d=new Date(exp*1000);
    $("userExpire").textContent="到期: "+d.getFullYear()+"-"+(d.getMonth()+1).toString().padStart(2,"0")+"-"+d.getDate().toString().padStart(2,"0")+" "+d.getHours().toString().padStart(2,"0")+":"+d.getMinutes().toString().padStart(2,"0")
  }else{$("userExpire").textContent="未激活"}

  if(active){
    var remaining=exp-now;
    if(remaining<7200){
      $("userBadge").innerHTML='<span class="badge-trial">试用中</span>'
    }else{
      $("userBadge").innerHTML='<span class="badge-active">已激活</span>'
    }
    $("trialBtn").style.display="none";
    $("paywall").style.display="none";
    $("toolbar").style.display="flex"
  }else{
    $("userBadge").innerHTML='<span class="badge-expired">已到期</span>';
    if(!userInfo.trial_used){
      $("trialBtn").style.display="inline-block"
    }else{$("trialBtn").style.display="none"}
    $("paywall").style.display="block";
    $("toolbar").style.display="none"
  }
}

function render(){
  var st=$("status");
  var on=S.active>=0;
  var live=on&&S.connected;
  st.className="status"+(live?" on":on?" reconnecting":"");
  $("statusText").textContent=live?"已连接":on?"重连中...":"未连接";

  var ci=$("connInfo");
  if(on){
    ci.className="conn-info show";
    $("infoSocks").textContent=S.socks_addr;
    $("infoHTTP").textContent=S.http_addr;
    $("infoMixed").textContent=S.mixed_addr;
    $("infoProxy").textContent=S.proxy_on?"已开启":"未开启"
  }else{ci.className="conn-info"}

  document.title=on?"Tunnel - "+S.servers[S.active].name:"Tunnel";
  renderUser();

  var el=$("servers");
  if(!userInfo||!userInfo.active){
    if(userInfo&&userInfo.expires_at<=0&&!userInfo.trial_used){
      el.innerHTML='<div class="empty"><p>点击"免费试用1小时"开始体验</p></div>'
    }else if(userInfo&&!userInfo.active){
      el.innerHTML=''
    }else{el.innerHTML=''}
    $("nodeCount").textContent="";
    return
  }

  $("nodeCount").textContent=S.servers?S.servers.length+" 个节点":"";
  if(!S.servers||S.servers.length===0){
    el.innerHTML='<div class="empty"><p>暂无可用节点</p><p style="font-size:12px;color:#334155">点击"刷新节点"获取</p></div>';
    return
  }

  var h="";
  for(var i=0;i<S.servers.length;i++){
    var s=S.servers[i],act=i===S.active,ld=loading[i],ts=testing[i];
    h+='<div class="server'+(act?" active":"")+'">';
    h+='<div class="server-top"><div class="server-dot"></div>';
    h+='<div class="server-name">'+esc(s.name)+"</div>";
    if(lats[i]!==undefined)h+='<div class="server-latency">'+lats[i]+"ms</div>";
    h+="</div>";
    h+='<div class="server-addr">'+esc(s.addr)+(s.ip?" ("+esc(s.ip)+")":"")+"</div>";
    h+='<div class="server-btns">';
    if(act){
      h+='<button class="btn-disconnect" onclick="doDisconnect()"'+(ld?" disabled":"")+">断开</button>"
    }else{
      h+='<button class="btn-connect" onclick="doConnect('+i+')"'+(ld?" disabled":"")+">"+(ld?"连接中...":"连接")+"</button>"
    }
    h+='<button class="btn-test" onclick="doTest('+i+')"'+(ts?" disabled":"")+">"+(ts?"...":"测速")+"</button>";
    h+="</div></div>"
  }
  el.innerHTML=h
}

function doConnect(id){
  loading[id]=true;render();
  fetch("/local/connect?id="+id,{method:"POST"}).then(function(r){return r.json()}).then(function(d){
    loading[id]=false;
    if(d.error){toast(d.error,"error")}else{toast("已连接","success")}
    loadStatus()
  }).catch(function(){loading[id]=false;toast("连接失败","error");loadStatus()})
}

function doDisconnect(){
  fetch("/local/disconnect",{method:"POST"}).then(function(){toast("已断开","");loadStatus()})
}

function doTest(id){
  testing[id]=true;render();
  fetch("/local/test?id="+id,{method:"POST"}).then(function(r){return r.json()}).then(function(d){
    testing[id]=false;
    if(d.latency!==undefined){lats[id]=d.latency;toast(d.latency+"ms","success")}
    else{toast(d.error||"测速失败","error")}
    render()
  }).catch(function(){testing[id]=false;toast("测速失败","error");render()})
}

var plans=[];
var selPlan=null;

function loadPlans(){
  apiCall("/api/me").then(function(d){
    if(d.plans){plans=d.plans;renderPlans()}
  })
}

function renderPlans(){
  var el=$("plans");
  if(!plans||plans.length===0){el.innerHTML="";$("buyBtn").style.display="none";$("buyMsg").textContent="请联系管理员充值";return}
  var h="";
  plans.forEach(function(p){
    var sel=selPlan&&selPlan.id===p.id;
    h+='<div class="plan-card'+(sel?" sel":"")+'" onclick="pickPlan('+p.id+')">';
    h+='<div class="plan-name">'+p.name+'</div>';
    h+='<div class="plan-price">¥'+p.price+'</div>';
    h+='<div class="plan-unit">'+p.days+'天</div>';
    h+='</div>'
  });
  el.innerHTML=h;
  $("buyBtn").style.display=selPlan?"block":"none";
  $("buyMsg").textContent=selPlan?"":"点击选择套餐"
}

function pickPlan(id){
  selPlan=plans.find(function(p){return p.id===id})||null;
  renderPlans()
}

function doBuy(){
  if(!selPlan)return;
  $("buyBtn").disabled=true;$("buyBtn").textContent="正在创建订单...";
  apiCall("/api/buy",{method:"POST",body:JSON.stringify({plan_id:selPlan.id})})
  .then(function(d){
    $("buyBtn").disabled=false;$("buyBtn").textContent="立即购买";
    if(d.error){toast(d.error,"error");return}
    if(d.pay_url){
      window.open(d.pay_url,"_blank");
      toast("已打开支付页面，支付后自动到账","success");
      pollOrder(d.order_id)
    }else{
      toast(d.message||"请联系管理员","");
    }
  }).catch(function(){$("buyBtn").disabled=false;$("buyBtn").textContent="立即购买";toast("网络错误","error")})
}

function pollOrder(oid){
  var count=0;
  var iv=setInterval(function(){
    count++;
    if(count>60){clearInterval(iv);return}
    apiCall("/api/order?id="+oid).then(function(d){
      if(d.order&&d.order.status==="paid"){
        clearInterval(iv);
        toast("支付成功！套餐已到账","success");
        refreshUserInfo();
        setTimeout(function(){refreshNodes()},1000)
      }
    })
  },3000)
}

function checkUpdate(){
  fetch("/local/version").then(function(r){return r.json()}).then(function(local){
    fetch("/api/version").then(function(r){return r.json()}).then(function(remote){
      if(remote.version&&remote.version!==local.version){
        var b=$("updateBanner");
        b.style.display="block";
        $("updateText").textContent="发现新版本 v"+remote.version+"（当前 v"+local.version+"）— 点击下载";
        $("updateLog").textContent=remote.changelog||"";
        b.dataset.url=remote.download_url||""
      }
    }).catch(function(){})
  }).catch(function(){})
}

function init(){
  loadStatus();
  setTimeout(function(){
    if(S.auth&&S.auth.token){
      $("authScreen").style.display="none";
      $("mainScreen").style.display="block";
      refreshUserInfo();
      loadPlans();
      if(userInfo&&userInfo.active){refreshNodes()}
    }else{
      $("authScreen").style.display="block";
      $("mainScreen").style.display="none"
    }
    checkUpdate()
  },300)
}

function fmtSize(n){
  if(n<1024)return n+" B";
  if(n<1048576)return(n/1024).toFixed(1)+" KB";
  if(n<1073741824)return(n/1048576).toFixed(1)+" MB";
  return(n/1073741824).toFixed(2)+" GB"
}

var prevUp=0,prevDown=0,speedTimer=null;
function startSpeedPoll(){
  if(speedTimer)return;
  prevUp=0;prevDown=0;
  speedTimer=setInterval(function(){
    fetch("/local/speed").then(function(r){return r.json()}).then(function(d){
      var up=d.upload||0,down=d.download||0;
      var sUp=up-prevUp,sDown=down-prevDown;
      if(prevUp===0&&prevDown===0){sUp=0;sDown=0}
      prevUp=up;prevDown=down;
      $("speedUp").textContent=fmtSize(sUp)+"/s";
      $("speedDown").textContent=fmtSize(sDown)+"/s";
      $("totalUp").textContent=fmtSize(up);
      $("totalDown").textContent=fmtSize(down);
      $("speedRow").style.display="";
      $("trafficRow").style.display=""
    }).catch(function(){})
  },1000)
}
function stopSpeedPoll(){
  if(speedTimer){clearInterval(speedTimer);speedTimer=null}
  $("speedRow").style.display="none";
  $("trafficRow").style.display="none"
}

function toggleAutoStart(on){
  fetch("/local/autostart",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({enable:on})})
  .then(function(r){return r.json()}).then(function(d){
    if(d.error){toast(d.error,"error");$("autoStartCb").checked=!on}
    else{toast(on?"已开启开机自启":"已关闭开机自启","success")}
  }).catch(function(){toast("设置失败","error");$("autoStartCb").checked=!on})
}

function loadAutoStart(){
  fetch("/local/autostart").then(function(r){return r.json()}).then(function(d){
    $("autoStartCb").checked=!!d.enabled
  }).catch(function(){})
}

var _origRender=render;
render=function(){
  _origRender();
  if(S.active>=0&&S.connected){startSpeedPoll()}else{stopSpeedPoll()}
};

init();
loadAutoStart();
setInterval(loadStatus,3000);
setInterval(refreshUserInfo,60000);
</script>
</body>
</html>`
