package desktop

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
	"tunnelx/internal/control"
	"tunnelx/internal/diagnostics"
	"tunnelx/internal/routing"
	"tunnelx/internal/tunmode"
)

func (e *Engine) CancelConnect() error {
	e.mu.Lock()
	e.stopRecoveryLocked()
	cancel := e.connectCancel
	if cancel != nil {
		e.stage = "cancelling"
		// Cancellation and committing a live connection share this lock.
		// Otherwise a scheduled-away caller could cancel after commit.
		cancel()
	}
	e.mu.Unlock()
	return nil
}
func (e *Engine) connectionStage(ctx context.Context, attempt uint64, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.connectID != attempt || !e.connecting {
		return context.Canceled
	}
	if e.stage != "cancelling" {
		e.stage = stage
	}
	return ctx.Err()
}
func (e *Engine) RecoverProxy() error {
	if !e.lifecycle.TryLock() {
		return errors.New("请先断开或取消节点连接")
	}
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	active := e.mux != nil || e.connecting || e.recovering
	e.mu.Unlock()
	if active {
		return errors.New("请先断开节点再恢复系统代理")
	}
	return e.recoverProxy()
}
func (e *Engine) recoverProxy() error {
	err := errors.Join(e.system.RestoreOrphan(), tunmode.Recover(filepath.Join(e.root, "state", "tun-state.json")))
	e.setProxyError(err)
	return err
}
func (e *Engine) setProxyError(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proxyRecoveryError = ""
	if err != nil {
		e.proxyRecoveryError = err.Error()
	}
}

type nodeProfile struct {
	Node struct {
		Name string `json:"name"`
	} `json:"node"`
	Client config.Client `json:"client"`
	CAPEM  string        `json:"ca_pem"`
}

func (e *Engine) Connect(id string, systemProxy bool) (result error) {
	e.mu.Lock()
	e.stopRecoveryLocked()
	e.mu.Unlock()
	return e.connect(id, systemProxy, 0)
}
func (e *Engine) connect(id string, systemProxy bool, recovery uint64) (result error) {
	if !validNodeID(id, false) {
		return errors.New("无效节点")
	}
	if !e.lifecycle.TryLock() {
		return errors.New("正在处理连接，请稍后重试")
	}
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	if recovery != 0 && (!e.recovering || e.recoveryEpoch != recovery || e.recoveryContext.Err() != nil) {
		e.mu.Unlock()
		return context.Canceled
	}
	if e.mux != nil || e.connecting || e.stopping > 0 {
		e.mu.Unlock()
		return errors.New("请先断开当前节点")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if recovery != 0 {
		stop := context.AfterFunc(e.recoveryContext, cancel)
		defer stop()
	}
	e.connectID++
	attempt := e.connectID
	e.connectCancel = cancel
	e.connecting = true
	e.stage = "restoring_proxy"
	e.health = Health{}
	e.nodeID = id
	e.nodeName = ""
	settings, device := e.settings, e.login.DeviceID
	mode, rules, exceptions := e.preferences.ProxyMode, e.rules, strings.Join(e.preferences.DirectDomains, "\n")
	e.mu.Unlock()
	var m *client.Mux
	var p *client.Proxy
	var l *diagnostics.Logger
	var tun *tunmode.Manager
	enabled, committed := false, false
	defer func() {
		cancel()
		if !committed {
			if tun != nil {
				result = errors.Join(result, tun.Close())
			}
			if enabled {
				err := e.system.Restore()
				e.setProxyError(err)
				result = errors.Join(result, err)
			}
			if p != nil {
				p.Close()
			} else if m != nil {
				m.Close()
			}
			if l != nil {
				l.Close()
			}
		}
		e.mu.Lock()
		if e.connectID == attempt {
			e.connecting = false
			e.connectCancel = nil
			if !committed {
				e.stage = "disconnected"
				e.nodeID = ""
				e.nodeName = ""
			}
		}
		e.mu.Unlock()
	}()
	if err := e.recoverProxy(); err != nil {
		return err
	}
	if mode == routing.TUN {
		if !tunmode.Elevated() {
			return errors.New("TUN 需要管理员权限，请退出客户端后右键选择“以管理员身份运行”。")
		}
		if err := tunmode.CheckAssets(tunmode.RuntimeDir()); err != nil {
			return err
		}
		systemProxy = false
	}
	if err := e.connectionStage(ctx, attempt, "fetching_profile"); err != nil {
		return err
	}
	var profile nodeProfile
	if err := e.request(ctx, "/api/nodes/"+id+"/profile", nil, &profile); err != nil {
		return err
	}
	if err := e.connectionStage(ctx, attempt, "verifying_tls"); err != nil {
		return err
	}
	c := profile.Client
	c.SOCKSListen = settings.SOCKS
	c.HTTPListen = settings.HTTP
	c.WebListen = settings.Web
	c.DeviceID = device
	c.Transport = "h2"
	c.Privacy = "strict"
	ca := filepath.Join(e.root, "state", "origin-ca.pem")
	if err := control.WriteFile(ca, []byte(profile.CAPEM)); err != nil {
		return err
	}
	c.CAFile = ca
	var err error
	m, err = client.New(c)
	if err != nil {
		return err
	}
	probeCtx, stopProbe := context.WithTimeout(ctx, 15*time.Second)
	res, probeErr := m.PublicGET(probeCtx)
	status := 0
	if res != nil {
		status = res.StatusCode
		if probeErr == nil {
			_, probeErr = io.Copy(io.Discard, io.LimitReader(res.Body, 8192))
		}
		res.Body.Close()
	}
	stopProbe()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if probeErr != nil || status != 200 || !m.LastECH.Load() {
		return errors.New("节点 TLS/ECH 检测失败")
	}
	tlsChecked := time.Now().Unix()
	if err = e.connectionStage(ctx, attempt, "authenticating"); err != nil {
		return err
	}
	admitCtx, stopAdmit := context.WithTimeout(ctx, 10*time.Second)
	defer stopAdmit()
	for {
		stream, openErr := m.OpenTCP(admitCtx, net.JoinHostPort(c.ServerIP, strconv.Itoa(c.Port)))
		if openErr == nil {
			stream.Close()
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var response *client.RequestError
		if !errors.As(openErr, &response) || response.Status != 405 || admitCtx.Err() != nil {
			return errors.New("节点账号认证失败，请刷新账号或检查节点状态")
		}
		select {
		case <-admitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("等待节点账号同步超时")
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err = e.connectionStage(ctx, attempt, "starting_proxy"); err != nil {
		return err
	}
	p = client.NewProxy(m)
	if mode == routing.TUN {
		if err = e.connectionStage(ctx, attempt, "checking_tun_network"); err != nil {
			return err
		}
		p.TUN = true
		p.TUNIPv6.Store(m.ProbeTUNIPv6(ctx))
		if err = ctx.Err(); err != nil {
			return err
		}
	}
	if mode == routing.Bypass {
		p.Router, err = routing.New(rules, exceptions)
		if err != nil {
			return err
		}
	}
	if err = p.Start(); err != nil {
		return fmt.Errorf("本地代理端口被占用，请先退出旧版 tunnelX：%w", err)
	}
	l, err = diagnostics.New(filepath.Join(e.root, "logs"), diagnostics.Options{})
	if err != nil {
		return err
	}
	m.SetDiagnostics(l)
	if mode == routing.TUN {
		l.Record("tun_network_policy", map[string]any{"scope": "client", "ipv6_available": p.TUNIPv6.Load(), "dns_transport": "h2_tcp", "dns_ipv4_fallback": !p.TUNIPv6.Load()})
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if systemProxy {
		if err = e.system.Enable(); err != nil {
			return err
		}
		enabled = true
	}
	if mode == routing.TUN {
		if err = e.connectionStage(ctx, attempt, "starting_tun"); err != nil {
			return err
		}
		exempt := []string{c.ServerIP}
		base, _ := url.Parse(settings.APIURL)
		resolveCtx, done := context.WithTimeout(ctx, 8*time.Second)
		addresses, resolveErr := net.DefaultResolver.LookupHost(resolveCtx, base.Hostname())
		done()
		if resolveErr != nil {
			return errors.New("TUN 启动前无法解析管理服务地址，请检查本地 DNS")
		}
		exempt = append(exempt, addresses...)
		tun = &tunmode.Manager{}
		if err = tun.Start(ctx, filepath.Join(e.root, "state", "tun-state.json"), settings.SOCKS, exempt); err != nil {
			return err
		}
	}
	e.mu.Lock()
	if ctx.Err() != nil || e.stopping > 0 {
		e.mu.Unlock()
		return context.Canceled
	}
	live, stop := context.WithCancel(context.Background())
	e.cancel = stop
	e.finish = m.RunDiagnostics(live, 30*time.Second)
	e.logger = l
	e.mux = m
	e.proxy = p
	e.tun = tun
	e.nodeID = id
	e.nodeName = profile.Node.Name
	e.connectedAt = time.Now()
	e.priorTime = time.Now()
	e.priorUp = 0
	e.priorDown = 0
	e.upRate = 0
	e.downRate = 0
	e.stage = "connected"
	e.health = Health{CheckedAt: tlsChecked, LastSuccess: tlsChecked}
	e.healthDone = e.startHealth(live, m)
	e.connecting = false
	e.connectCancel = nil
	committed = true
	e.mu.Unlock()
	if tun != nil {
		go func() {
			<-tun.Done()
			if tun.NetworkChanged() {
				e.recoverTUN(m)
			} else {
				_ = e.disconnectSession(m)
			}
		}()
	}
	l.Record("client_started", map[string]any{"scope": "client", "client_version": DesktopVersion, "mode": "h2", "proxy_mode": mode, "privacy": "strict", "pid": os.Getpid(), "server": c.ServerIP, "server_port": c.Port, "health_interval_seconds": 30, "h2_connections": c.H2Connections})
	e.recordConn("connect", id, profile.Node.Name, mode)
	return nil
}
func (e *Engine) Disconnect() error {
	e.beginStop()
	e.lifecycle.Lock()
	defer e.endStop()
	return e.disconnect()
}
func (e *Engine) beginStop() {
	e.mu.Lock()
	e.stopRecoveryLocked()
	e.stopping++
	cancel := e.connectCancel
	if cancel != nil {
		e.stage = "cancelling"
		cancel()
	}
	e.mu.Unlock()
}
func (e *Engine) endStop() { e.mu.Lock(); e.stopping--; e.mu.Unlock(); e.lifecycle.Unlock() }

// A delayed authorization failure may only tear down its original connection.
// It must not cancel a new setup or connection that replaced that session.
func (e *Engine) disconnectSession(expected *client.Mux) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	current := e.mux == expected
	e.mu.Unlock()
	if !current {
		return nil
	}
	return e.disconnect()
}

// disconnect is called with lifecycle held, never while mu is held. Health
// callbacks can finish without waiting for the same lock as teardown.
func (e *Engine) disconnect() error {
	e.mu.Lock()
	m, p, l, cancel, finish, healthDone := e.mux, e.proxy, e.logger, e.cancel, e.finish, e.healthDone
	tun := e.tun
	e.tun = nil
	e.mux = nil
	e.proxy = nil
	e.logger = nil
	e.cancel = nil
	e.finish = nil
	e.healthDone = nil
	e.nodeID = ""
	e.nodeName = ""
	e.stage = "disconnected"
	e.mu.Unlock()
	if m == nil {
		return e.recoverProxy()
	}
	e.recordConn("disconnect", "", "", "")
	var tunErr error
	if tun != nil {
		tunErr = tun.Close()
	}
	err := errors.Join(tunErr, e.system.Restore())
	e.setProxyError(err)
	if cancel != nil {
		cancel()
	}
	if finish != nil {
		finish()
	}
	if healthDone != nil {
		<-healthDone
	}
	if p != nil {
		p.Close()
	} else {
		m.Close()
	}
	m.FinishDiagnostics()
	if l != nil {
		l.Record("client_stopped", map[string]any{"scope": "client", "reason": "requested_shutdown"})
		l.Close()
	}
	return err
}
func (e *Engine) Probe(id string) (int64, error) {
	if !validNodeID(id, false) {
		return 0, errors.New("无效节点")
	}
	var profile nodeProfile
	if err := e.request(context.Background(), "/api/nodes/"+id+"/profile", nil, &profile); err != nil {
		return 0, err
	}
	ca := filepath.Join(e.root, "state", "probe-"+rand.Text()+".pem")
	if err := control.WriteFile(ca, []byte(profile.CAPEM)); err != nil {
		return 0, err
	}
	defer os.Remove(ca)
	profile.Client.CAFile = ca
	m, err := client.New(profile.Client)
	if err != nil {
		return 0, err
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	r, err := m.PublicGET(ctx)
	if err != nil {
		return 0, errors.New("节点检测失败")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 || !m.LastECH.Load() {
		return 0, errors.New("节点检测失败")
	}
	if _, err = io.Copy(io.Discard, io.LimitReader(r.Body, 8192)); err != nil {
		return 0, errors.New("节点检测失败")
	}
	return time.Since(started).Milliseconds(), nil
}

type ProbeResult struct {
	ID    string `json:"id"`
	Ms    int64  `json:"ms"`
	Error string `json:"error,omitempty"`
}

func (e *Engine) ProbeAll() []ProbeResult {
	e.mu.Lock()
	base := e.settings.APIURL
	token := e.login.Token
	e.mu.Unlock()
	if token == "" {
		return nil
	}
	var nodes []struct {
		ID string `json:"id"`
	}
	if err := e.request(context.Background(), "/api/nodes", nil, &nodes); err != nil || len(nodes) == 0 {
		return nil
	}
	results := make([]ProbeResult, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, n := range nodes {
		results[i].ID = n.ID
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ms, err := e.Probe(id)
			if err != nil {
				results[idx].Error = err.Error()
				results[idx].Ms = -1
			} else {
				results[idx].Ms = ms
			}
		}(i, n.ID)
	}
	wg.Wait()
	_ = base
	return results
}
