package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"tunnel/internal/mux"
	"tunnel/internal/proto"
	"tunnel/internal/relay"
	"tunnel/internal/socks5"

	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"
	wailsRT "github.com/wailsapp/wails/v2/pkg/runtime"
)

const apiBase = "https://cloud.xiaguamail.com"
const clientVersion = "1.0.0"

var uaPool = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
}

var flagMap = map[string]string{
	"US": "\U0001F1FA\U0001F1F8", "JP": "\U0001F1EF\U0001F1F5", "DE": "\U0001F1E9\U0001F1EA",
	"SG": "\U0001F1F8\U0001F1EC", "KR": "\U0001F1F0\U0001F1F7", "HK": "\U0001F1ED\U0001F1F0",
	"TW": "\U0001F1F9\U0001F1FC", "GB": "\U0001F1EC\U0001F1E7", "FR": "\U0001F1EB\U0001F1F7",
	"CA": "\U0001F1E8\U0001F1E6", "AU": "\U0001F1E6\U0001F1FA", "NL": "\U0001F1F3\U0001F1F1",
	"IN": "\U0001F1EE\U0001F1F3", "RU": "\U0001F1F7\U0001F1FA", "BR": "\U0001F1E7\U0001F1F7",
	"TR": "\U0001F1F9\U0001F1F7",
}

type NodeInfo struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Region  string `json:"region"`
	Flag    string `json:"flag"`
	Latency int    `json:"latency"`
}

type SpeedInfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

type StatusInfo struct {
	Connected bool   `json:"connected"`
	NodeName  string `json:"nodeName"`
	NodeID    int64  `json:"nodeId"`
}

type ProfileInfo struct {
	ID        int64   `json:"id"`
	Email     string  `json:"email"`
	Role      string  `json:"role"`
	Active    bool    `json:"active"`
	ExpiresAt int64   `json:"expires_at"`
	CreatedAt int64   `json:"created_at"`
	TrialUsed bool    `json:"trial_used"`
	Upload    int64   `json:"upload"`
	Download  int64   `json:"download"`
	Plans     []PlanInfo `json:"plans"`
}

type PlanInfo struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Days  int     `json:"days"`
	Price float64 `json:"price"`
}

type OrderInfo struct {
	ID        int64   `json:"id"`
	Plan      string  `json:"plan"`
	Days      int     `json:"days"`
	Amount    float64 `json:"amount"`
	Status    string  `json:"status"`
	CreatedAt int64   `json:"created_at"`
	PaidAt    int64   `json:"paid_at"`
}

type AuthState struct {
	Token      string          `json:"token,omitempty"`
	User       json.RawMessage `json:"user,omitempty"`
	LastNodeID int64           `json:"last_node_id,omitempty"`
}

type serverConfig struct {
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	IP     string `json:"ip,omitempty"`
	PSK    string `json:"psk"`
	ID     int64  `json:"id"`
	Region string `json:"region,omitempty"`
}

type connPool struct {
	mu   sync.Mutex
	conn *mux.Mux
	dial func() (*mux.Mux, error)
}

func (p *connPool) Get() (*mux.Mux, error) {
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

type UpdateInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	URL       string `json:"url"`
}

type App struct {
	ctx            context.Context
	auth           AuthState
	authPath       string
	nodes          []serverConfig
	mixedAddr      string
	trayDisconnect interface{ Enable(); Disable() }

	mu        sync.RWMutex
	pool      *connPool
	activeID  int64
	proxyOn   bool
	upBytes   atomic.Int64
	downBytes atomic.Int64
}

func NewApp() *App {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	return &App{
		authPath:  filepath.Join(dir, "auth.json"),
		mixedAddr: "127.0.0.1:7890",
		activeID:  -1,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadAuth()
	a.cleanStaleProxy()
	go a.startMixed()
	go a.healthCheck()
	go a.startTray()
	go a.checkUpdateOnStart()
}

func (a *App) cleanStaleProxy() {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable").Output()
	if err != nil {
		return
	}
	if !strings.Contains(string(out), "0x1") {
		return
	}
	out, err = exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer").Output()
	if err != nil {
		return
	}
	if strings.Contains(string(out), a.mixedAddr) {
		clearSystemProxy()
		log.Println("[startup] cleared stale proxy from previous session")
	}
}

func (a *App) GetLastNodeID() int64 {
	return a.auth.LastNodeID
}

func (a *App) shutdown(ctx context.Context) {
	a.Disconnect()
}

func (a *App) loadAuth() {
	data, err := os.ReadFile(a.authPath)
	if err == nil {
		json.Unmarshal(data, &a.auth)
	}
}

func (a *App) saveAuth() {
	data, _ := json.MarshalIndent(a.auth, "", "  ")
	os.WriteFile(a.authPath, data, 0644)
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

func getMachineID() string {
	out, err := exec.Command("wmic", "csproduct", "get", "UUID").Output()
	if err != nil {
		return fmt.Sprintf("win-%d", time.Now().UnixNano())
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "UUID" {
			return line
		}
	}
	return fmt.Sprintf("win-%d", time.Now().UnixNano())
}

// ---------- Wails bindings ----------

func (a *App) Login(email, pass string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": pass})
	resp, err := http.Post(apiBase+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Token string          `json:"token"`
		User  json.RawMessage `json:"user"`
		Error string          `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	a.auth = AuthState{Token: result.Token, User: result.User}
	a.saveAuth()
	return nil
}

func (a *App) Register(email, pass string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": pass})
	resp, err := http.Post(apiBase+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Token string          `json:"token"`
		User  json.RawMessage `json:"user"`
		Error string          `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	a.auth = AuthState{Token: result.Token, User: result.User}
	a.saveAuth()
	return nil
}

func (a *App) GuestLogin() error {
	machineID := getMachineID()
	body, _ := json.Marshal(map[string]string{"machine_id": machineID})
	resp, err := http.Post(apiBase+"/api/guest", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Token string          `json:"token"`
		User  json.RawMessage `json:"user"`
		Error string          `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	a.auth = AuthState{Token: result.Token, User: result.User}
	a.saveAuth()
	return nil
}

func (a *App) ActivateTrial() error {
	req, _ := http.NewRequest("POST", apiBase+"/api/trial", nil)
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (a *App) IsLoggedIn() bool {
	return a.auth.Token != ""
}

func (a *App) GetUser() string {
	return string(a.auth.User)
}

func (a *App) Logout() {
	a.Disconnect()
	a.auth = AuthState{}
	a.saveAuth()
}

func (a *App) HideWindow() {
	wailsRT.WindowHide(a.ctx)
}

func (a *App) ShowWindow() {
	wailsRT.WindowShow(a.ctx)
}

func (a *App) CheckUpdate() UpdateInfo {
	resp, err := http.Get("https://api.github.com/repos/674542449/tunnel-pro/releases/latest")
	if err != nil {
		return UpdateInfo{}
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	json.NewDecoder(resp.Body).Decode(&release)
	ver := strings.TrimPrefix(release.TagName, "v")
	if ver != "" && ver != clientVersion {
		dlURL := ""
		for _, asset := range release.Assets {
			if strings.HasSuffix(asset.Name, ".exe") {
				dlURL = asset.BrowserDownloadURL
				break
			}
		}
		return UpdateInfo{Available: true, Version: ver, URL: dlURL}
	}
	return UpdateInfo{}
}

func (a *App) OpenURL(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func (a *App) checkUpdateOnStart() {
	time.Sleep(2 * time.Second)
	info := a.CheckUpdate()
	if info.Available {
		wailsRT.EventsEmit(a.ctx, "update-available", info)
	}
}

func (a *App) apiGet(path string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", apiBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	return http.DefaultClient.Do(req)
}

func (a *App) GetProfile() (*ProfileInfo, error) {
	resp, err := a.apiGet("/api/me")
	if err != nil {
		return nil, fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		User struct {
			ID        int64  `json:"id"`
			Email     string `json:"email"`
			Role      string `json:"role"`
			Active    bool   `json:"active"`
			ExpiresAt int64  `json:"expires_at"`
			CreatedAt int64  `json:"created_at"`
			TrialUsed bool   `json:"trial_used"`
		} `json:"user"`
		Plans []PlanInfo `json:"plans"`
		Error string     `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}

	tResp, err := a.apiGet("/api/my-traffic")
	var upload, download int64
	if err == nil {
		defer tResp.Body.Close()
		var tr struct {
			Upload   int64 `json:"upload"`
			Download int64 `json:"download"`
		}
		json.NewDecoder(tResp.Body).Decode(&tr)
		upload = tr.Upload
		download = tr.Download
	}

	return &ProfileInfo{
		ID:        result.User.ID,
		Email:     result.User.Email,
		Role:      result.User.Role,
		Active:    result.User.Active,
		ExpiresAt: result.User.ExpiresAt,
		CreatedAt: result.User.CreatedAt,
		TrialUsed: result.User.TrialUsed,
		Upload:    upload,
		Download:  download,
		Plans:     result.Plans,
	}, nil
}

func (a *App) GetOrders() []OrderInfo {
	resp, err := a.apiGet("/api/orders")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Orders []OrderInfo `json:"orders"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Orders
}

func (a *App) ChangePassword(oldPass, newPass string) error {
	body, _ := json.Marshal(map[string]string{"old_password": oldPass, "new_password": newPass})
	req, _ := http.NewRequest("POST", apiBase+"/api/change-password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (a *App) GetNodes() []NodeInfo {
	req, _ := http.NewRequest("GET", apiBase+"/api/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Nodes []struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			Addr   string `json:"addr"`
			IP     string `json:"ip"`
			PSK    string `json:"psk"`
			Region string `json:"region"`
		} `json:"nodes"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	a.nodes = nil
	var out []NodeInfo
	for _, n := range result.Nodes {
		a.nodes = append(a.nodes, serverConfig{
			Name: n.Name, Addr: n.Addr, IP: n.IP, PSK: n.PSK, ID: n.ID, Region: n.Region,
		})
		flag := "\U0001F310"
		region := strings.ToUpper(strings.TrimSpace(n.Region))
		for code, emoji := range flagMap {
			if strings.Contains(region, code) || strings.EqualFold(region, code) {
				flag = emoji
				break
			}
		}
		regionNames := map[string]string{
			"JP": "Japan", "US": "United States", "DE": "Germany", "SG": "Singapore",
			"KR": "Korea", "HK": "Hong Kong", "TW": "Taiwan", "GB": "United Kingdom",
			"FR": "France", "CA": "Canada", "AU": "Australia",
		}
		for code, name := range regionNames {
			if strings.Contains(region, name) || strings.Contains(strings.ToUpper(n.Name), code) {
				if f, ok := flagMap[code]; ok {
					flag = f
				}
				break
			}
		}
		out = append(out, NodeInfo{ID: n.ID, Name: n.Name, Region: n.Region, Flag: flag})
	}
	return out
}

func (a *App) Connect(nodeID int64) error {
	var cfg *serverConfig
	for i := range a.nodes {
		if a.nodes[i].ID == nodeID {
			cfg = &a.nodes[i]
			break
		}
	}
	if cfg == nil {
		return errors.New("node not found")
	}

	a.Disconnect()

	dialFn := func() (*mux.Mux, error) {
		ws, err := dialWS(*cfg)
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
	if uid := parseJWTUID(a.auth.Token); uid > 0 {
		mx.SendUserID(strconv.FormatInt(uid, 10))
	}

	a.mu.Lock()
	a.pool = &connPool{dial: dialFn, conn: mx}
	a.activeID = nodeID
	a.mu.Unlock()

	setSystemProxy(a.mixedAddr)
	a.proxyOn = true

	a.auth.LastNodeID = nodeID
	a.saveAuth()

	a.updateTrayTooltip(true, cfg.Name)
	wailsRT.EventsEmit(a.ctx, "connection-changed", StatusInfo{Connected: true, NodeName: cfg.Name, NodeID: nodeID})
	return nil
}

func (a *App) Disconnect() {
	a.mu.Lock()
	if a.pool != nil {
		if a.pool.conn != nil {
			a.pool.conn.Close()
		}
		a.pool = nil
	}
	a.activeID = -1
	a.mu.Unlock()

	if a.proxyOn {
		clearSystemProxy()
		a.proxyOn = false
	}

	a.updateTrayTooltip(false, "")
	if a.ctx != nil {
		wailsRT.EventsEmit(a.ctx, "connection-changed", StatusInfo{Connected: false})
	}
}

func (a *App) GetStatus() StatusInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pool == nil || a.activeID < 0 {
		return StatusInfo{Connected: false}
	}
	name := ""
	for _, n := range a.nodes {
		if n.ID == a.activeID {
			name = n.Name
			break
		}
	}
	return StatusInfo{Connected: true, NodeName: name, NodeID: a.activeID}
}

func (a *App) GetSpeed() SpeedInfo {
	return SpeedInfo{
		Upload:   a.upBytes.Swap(0),
		Download: a.downBytes.Swap(0),
	}
}

func (a *App) TestLatency(nodeID int64) int {
	var cfg *serverConfig
	for i := range a.nodes {
		if a.nodes[i].ID == nodeID {
			cfg = &a.nodes[i]
			break
		}
	}
	if cfg == nil {
		return -1
	}
	addr := cfg.Addr + ":443"
	if cfg.IP != "" {
		addr = cfg.IP + ":443"
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return -1
	}
	conn.Close()
	return int(time.Since(start).Milliseconds())
}

// ---------- proxy ----------

func (a *App) openStream(host string, port uint16) (*mux.Stream, error) {
	a.mu.RLock()
	p := a.pool
	a.mu.RUnlock()
	if p == nil {
		return nil, errors.New("not connected")
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
	return nil, errors.New("tunnel unavailable")
}

func (a *App) startMixed() {
	ln, err := net.Listen("tcp", a.mixedAddr)
	if err != nil {
		log.Printf("[mixed] listen error: %v", err)
		return
	}
	log.Printf("[mixed] listening on %s", a.mixedAddr)
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go a.handleMixed(c)
	}
}

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

func (a *App) handleMixed(conn net.Conn) {
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
			stream, err := a.openStream(host, port)
			if err != nil {
				socks5.ReplyFailure(c)
				return
			}
			defer stream.Close()
			socks5.ReplySuccess(c)
			relay.CountingRelay(stream, c, &a.downBytes, &a.upBytes)
		})
	} else {
		a.handleHTTP(wrapped)
	}
}

func (a *App) handleHTTP(conn net.Conn) {
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
		stream, err := a.openStream(host, uint16(port))
		if err != nil {
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer stream.Close()
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		relay.CountingRelay(stream, conn, &a.downBytes, &a.upBytes)
		return
	}
	host := req.URL.Hostname()
	port := uint16(80)
	if req.URL.Port() != "" {
		p, _ := strconv.Atoi(req.URL.Port())
		port = uint16(p)
	}
	stream, err := a.openStream(host, port)
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
	relay.CountingRelay(stream, conn, &a.downBytes, &a.upBytes)
}

func (a *App) healthCheck() {
	for {
		time.Sleep(5 * time.Second)
		a.mu.RLock()
		p := a.pool
		id := a.activeID
		a.mu.RUnlock()
		if p == nil || id < 0 {
			continue
		}
		p.mu.Lock()
		conn := p.conn
		p.mu.Unlock()
		if conn == nil || !conn.IsClosed() {
			continue
		}
		log.Println("[health] connection lost, reconnecting...")
		if a.ctx != nil {
			wailsRT.EventsEmit(a.ctx, "reconnecting", true)
		}
		ok := false
		for retry := 0; retry < 5; retry++ {
			if retry > 0 {
				time.Sleep(time.Duration(2<<retry) * time.Second)
			}
			mx, err := p.dial()
			if err != nil {
				log.Printf("[health] retry %d failed: %v", retry+1, err)
				continue
			}
			if uid := parseJWTUID(a.auth.Token); uid > 0 {
				mx.SendUserID(strconv.FormatInt(uid, 10))
			}
			p.mu.Lock()
			p.conn = mx
			p.mu.Unlock()
			ok = true
			log.Println("[health] reconnected")
			break
		}
		if a.ctx != nil {
			wailsRT.EventsEmit(a.ctx, "reconnecting", false)
		}
		if !ok {
			a.Disconnect()
		}
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

func dialWS(cfg serverConfig) (*websocket.Conn, error) {
	wsPath := proto.DynamicPath(cfg.PSK, 0)
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
