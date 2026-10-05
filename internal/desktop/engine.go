// Package desktop exposes the H2 client to the Windows desktop shell.
package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/control"
	"tunnelx/internal/diagnostics"
	"tunnelx/internal/routing"
	"tunnelx/internal/systemproxy"
	"tunnelx/internal/tunmode"
)

type Settings struct {
	APIURL string `json:"api_url"`
	SOCKS  string `json:"socks_listen"`
	HTTP   string `json:"http_listen"`
	Web    string `json:"web_listen"`
}

const DesktopVersion = "v0.6.17"

type Preferences struct {
	SelectedNodeID string   `json:"selected_node_id"`
	SystemProxy    bool     `json:"system_proxy"`
	ProxyMode      string   `json:"proxy_mode"`
	DirectDomains  []string `json:"direct_domains"`
}
type Health struct {
	CheckedAt           int64       `json:"checked_at"`
	LastSuccess         int64       `json:"last_success"`
	ConsecutiveFailures int         `json:"consecutive_failures"`
	ErrorKind           string      `json:"error_kind"`
	Gateway             HealthLayer `json:"gateway"`
	DNS                 HealthLayer `json:"dns"`
	Egress              HealthLayer `json:"egress"`
}
type HealthLayer struct {
	CheckedAt   int64  `json:"checked_at"`
	LastSuccess int64  `json:"last_success"`
	Failures    int    `json:"consecutive_failures"`
	ErrorKind   string `json:"error_kind"`
}
type loginState struct {
	APIURL   string `json:"api_url"`
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
	Email    string `json:"email"`
}
type Engine struct {
	// lifecycle serializes changes to the live proxy. Network waits never hold
	// mu, so status and cancellation remain available throughout connection.
	lifecycle                            sync.Mutex
	mu                                   sync.Mutex
	root                                 string
	settings                             Settings
	preferences                          Preferences
	login                                loginState
	http                                 *http.Client
	managementMu                         sync.Mutex
	managementEvents                     []managementEvent
	managementLogWriteFailed             bool
	mux                                  *client.Mux
	proxy                                *client.Proxy
	system                               *systemproxy.Manager
	cancel                               context.CancelFunc
	finish                               func()
	healthDone                           <-chan struct{}
	logger                               *diagnostics.Logger
	connectCancel                        context.CancelFunc
	connectID                            uint64
	connecting                           bool
	stopping                             int
	stage                                string
	health                               Health
	healthInterval                       time.Duration
	healthTimeout                        time.Duration
	nodeID, nodeName                     string
	connectedAt, priorTime               time.Time
	priorUp, priorDown, upRate, downRate int64
	proxyRecoveryError, startupWarning   string
	corruptCredentials                   []byte
	tun                                  *tunmode.Manager
	rules                                *routing.Rules
	ruleInfo                             map[string]any
	tunInfo                              map[string]any
	rulesUpdate                          sync.Mutex
	recoveryCancel                       context.CancelFunc
	recoveryContext                      context.Context
	recoveryEpoch                        uint64
	recovering                           bool
	recoveryAttempt                      int
	connLogs                             []ConnLog
}

func New(root string, settings Settings) (*Engine, error) {
	if settings.SOCKS == "" {
		settings.SOCKS = "127.0.0.1:1080"
	}
	if settings.HTTP == "" {
		settings.HTTP = "127.0.0.1:8088"
	}
	if settings.Web == "" {
		settings.Web = "127.0.0.1:9080"
	}
	base, err := NormalizeURL(settings.APIURL)
	if err != nil {
		return nil, err
	}
	settings.APIURL = base
	e := &Engine{root: root, settings: settings, preferences: Preferences{SystemProxy: true, ProxyMode: routing.Global}, http: newManagementClient(), stage: "disconnected", healthInterval: 30 * time.Second, healthTimeout: 5 * time.Second}
	e.rules, e.ruleInfo, err = routing.Load(filepath.Join(root, "state", "routing-rules.json"))
	if err != nil {
		return nil, err
	}
	e.tunInfo = tunmode.Availability()
	authPath := filepath.Join(root, "state", "auth.dpapi")
	if b, err := os.ReadFile(authPath); err == nil {
		plain, openErr := unprotect(b)
		if openErr != nil || json.Unmarshal(plain, &e.login) != nil {
			e.login = loginState{}
			e.startupWarning = "本机登录凭据已损坏，已保留原文件；请重新登录。"
			// New runs before the desktop shell acquires its single-instance
			// lock. Defer mutations until an explicit credential operation.
			e.corruptCredentials = append([]byte{}, b...)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if loginBase, err := NormalizeURL(e.login.APIURL); err == nil {
		e.login.APIURL = loginBase
	}
	if e.login.APIURL != settings.APIURL {
		e.login.Token, e.login.Email = "", ""
		e.login.APIURL = settings.APIURL
	}
	if e.login.DeviceID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		e.login.DeviceID = hex.EncodeToString(b)
	}
	if b, err := os.ReadFile(filepath.Join(root, "state", "preferences.json")); err == nil {
		p := e.preferences
		if json.Unmarshal(b, &p) == nil && validNodeID(p.SelectedNodeID, true) && routing.ValidMode(p.ProxyMode) {
			e.preferences = p
		} else {
			e.startupWarning += " 本机偏好无法读取，已使用默认设置。"
		}
	} else if !os.IsNotExist(err) {
		e.startupWarning += " 本机偏好无法读取，已使用默认设置。"
	}
	e.system = &systemproxy.Manager{Path: filepath.Join(root, "state", "system-proxy.json"), Proxy: settings.HTTP}
	return e, nil
}
func validNodeID(id string, empty bool) bool {
	if id == "" {
		return empty
	}
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16
}

// saveLogin is called with mu held, serializing credential replacement.
func (e *Engine) saveLogin() error {
	if e.corruptCredentials != nil {
		path := filepath.Join(e.root, "state", "auth.dpapi")
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, e.corruptCredentials) {
			return errors.New("本机登录凭据已被其他实例更新，请重新启动后登录")
		}
		backup := filepath.Join(e.root, "state", "auth-corrupt-"+time.Now().UTC().Format("20060102T150405")+"-"+rand.Text()+".dpapi")
		if err = os.Rename(path, backup); err != nil {
			return errors.New("原登录凭据尚未安全备份，请检查目录写入权限后重启")
		}
		e.corruptCredentials = nil
	}
	b, err := json.Marshal(e.login)
	if err != nil {
		return err
	}
	b, err = protect(b)
	if err != nil {
		return err
	}
	return control.WriteFile(filepath.Join(e.root, "state", "auth.dpapi"), b)
}
func (e *Engine) SavePreferences(nodeID string, proxy bool) error {
	if !validNodeID(nodeID, true) {
		return errors.New("无效节点偏好")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.preferences
	p.SelectedNodeID, p.SystemProxy = nodeID, proxy
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := control.WriteFile(filepath.Join(e.root, "state", "preferences.json"), b); err != nil {
		return err
	}
	e.preferences = p
	return nil
}
func (e *Engine) Settings() Settings { e.mu.Lock(); defer e.mu.Unlock(); return e.settings }
func (e *Engine) SetAPI(base string) error {
	base, err := NormalizeURL(base)
	if err != nil {
		return err
	}
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.settings.APIURL == base {
		return nil
	}
	if e.mux != nil || e.connecting || e.recovering {
		return errors.New("请先断开或取消节点连接")
	}
	next := e.settings
	next.APIURL = base
	b, _ := json.MarshalIndent(next, "", "  ")
	if err := control.WriteFile(filepath.Join(e.root, "settings.json"), b); err != nil {
		return err
	}
	e.settings = next
	e.login.Token = ""
	e.login.Email = ""
	e.login.APIURL = base
	e.preferences.SelectedNodeID = ""
	b, _ = json.MarshalIndent(e.preferences, "", "  ")
	return errors.Join(e.saveLogin(), control.WriteFile(filepath.Join(e.root, "state", "preferences.json"), b))
}
func (e *Engine) Login(email, password string, register bool) error {
	return e.LoginSecure(email, password, "", "", register)
}
func (e *Engine) LoginSecure(email, password, code, invite string, register bool) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	if e.mux != nil || e.connecting {
		e.mu.Unlock()
		return errors.New("请先断开或取消节点连接")
	}
	base := e.settings.APIURL
	e.mu.Unlock()
	path := "/api/login"
	if register {
		path = "/api/register"
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := e.doRequest(context.Background(), base, path, "", map[string]string{"email": email, "password": password, "code": code, "invite": invite}, &response); err != nil {
		return err
	}
	if response.Token == "" {
		return errors.New("登录服务未返回有效会话")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	previous := e.login
	e.login.Token = response.Token
	e.login.APIURL = base
	e.login.Email = email
	if err := e.saveLogin(); err != nil {
		e.login = previous
		return err
	}
	e.startupWarning = ""
	return nil
}
func (e *Engine) PortalURL() string { e.mu.Lock(); defer e.mu.Unlock(); return e.settings.APIURL + "/" }
func (e *Engine) Logout() error {
	e.beginStop()
	e.lifecycle.Lock()
	defer e.endStop()
	if err := e.disconnect(); err != nil {
		return err
	}
	e.mu.Lock()
	base, token := e.settings.APIURL, e.login.Token
	previous := e.login
	e.login.Token = ""
	e.login.Email = ""
	err := e.saveLogin()
	if err != nil {
		e.login = previous
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	_ = e.doRequest(context.Background(), base, "/api/logout", token, map[string]any{}, nil)
	return nil
}
func (e *Engine) Status() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := "disconnected"
	if e.connecting || e.recovering {
		state = "connecting"
	} else if e.mux != nil {
		state = "connected"
		if e.health.ConsecutiveFailures > 0 {
			state = "degraded"
		}
	}
	s := map[string]any{"version": DesktopVersion, "logged_in": e.login.Token != "", "email": e.login.Email, "connected": e.mux != nil, "connection_state": state, "connection_stage": e.stage, "health": e.health, "preferences": e.preferences, "startup_warning": e.startupWarning, "node_name": e.nodeName, "node_id": e.nodeID, "transport": "h2", "api_url": e.settings.APIURL, "system_proxy": e.system.Enabled(), "socks": e.settings.SOCKS, "http": e.settings.HTTP, "logs": filepath.Join(e.root, "logs"), "proxy_recovery_error": e.proxyRecoveryError}
	s["routing_rules"] = e.ruleInfo
	s["recovering"] = e.recovering
	s["recovery_attempt"] = e.recoveryAttempt
	s["tun"] = e.tunInfo
	if e.proxy != nil && e.proxy.TUN {
		info := map[string]any{}
		for k, v := range e.tunInfo {
			info[k] = v
		}
		info["ipv6_available"] = e.proxy.TUNIPv6.Load()
		info["dns_transport"] = "h2_tcp"
		info["dns_upstream"] = e.proxy.DNSRoute()
		s["tun"] = info
	}
	if e.mux != nil {
		up, down := e.mux.Stats.Uploaded.Load(), e.mux.Stats.Downloaded.Load()
		seconds := time.Since(e.priorTime).Seconds()
		if seconds >= .8 {
			e.upRate = int64(float64(up-e.priorUp) / seconds)
			e.downRate = int64(float64(down-e.priorDown) / seconds)
			e.priorUp = up
			e.priorDown = down
			e.priorTime = time.Now()
		}
		s["mode"] = "h2"
		s["privacy"] = "strict"
		s["tcp_carrier"] = e.mux.LastTCP.Load()
		s["h2_streams"] = e.mux.Stats.H2Streams.Load()
		s["upload"] = up
		s["download"] = down
		s["upload_rate"] = e.upRate
		s["download_rate"] = e.downRate
		s["active"] = e.mux.Stats.Active.Load()
		s["connected_at"] = e.connectedAt.Unix()
		s["ech"] = e.mux.LastECH.Load()
		s["logging"] = e.mux.LoggingStatus()
	}
	return s
}
type ConnLog struct {
	Time     string `json:"time"`
	Event    string `json:"event"`
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Detail   string `json:"detail,omitempty"`
}

func (e *Engine) recordConn(event, nodeID, nodeName, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.connLogs = append(e.connLogs, ConnLog{Time: time.Now().UTC().Format(time.RFC3339), Event: event, NodeID: nodeID, NodeName: nodeName, Detail: detail})
	if len(e.connLogs) > 200 {
		e.connLogs = append([]ConnLog(nil), e.connLogs[len(e.connLogs)-200:]...)
	}
}

func (e *Engine) ConnLogs() []ConnLog {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ConnLog(nil), e.connLogs...)
}

func (e *Engine) Close() error {
	e.http.CloseIdleConnections()
	return e.Disconnect()
}
