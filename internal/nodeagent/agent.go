// Package nodeagent connects the H2 node to the management API. No target addresses or payloads are reported.
package nodeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
	"tunnelx/internal/control"
	"tunnelx/internal/server"
)

type Config struct {
	APIURL    string `json:"api_url"`
	NodeID    string `json:"node_id"`
	AgentKey  string `json:"agent_key"`
	StateFile string `json:"state_file"`
}

func (c Config) Validate() error {
	u, e := url.Parse(c.APIURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid node API URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()) {
		return errors.New("node API requires HTTPS")
	}
	if len(c.NodeID) != 32 || len(c.AgentKey) != 43 || c.StateFile == "" {
		return errors.New("invalid node identity or state path")
	}
	return nil
}

type persisted struct {
	Clean          bool                       `json:"clean"`
	RecoveryLeases []control.Lease            `json:"recovery_leases,omitempty"`
	BootID         string                     `json:"boot_id"`
	Counters       map[string]control.Counter `json:"counters"`
	Leases         map[string]control.Lease   `json:"leases,omitempty"`
}
type Agent struct {
	ManagedOnly  bool
	leaseMu      sync.Mutex
	Connections  func() int64
	Config       Config
	mu           sync.Mutex
	syncMu       sync.Mutex
	state        persisted
	grants       map[[32]byte]control.Grant
	acknowledged map[string]control.Counter
	validUntil   time.Time
	permits      map[*access]bool
	limiters     map[string]*accountLimiters
	client       *http.Client
}
type accountLimiters struct {
	rate     int64
	up, down *server.Limiter
}
type access struct {
	agent          *Agent
	ctx            context.Context
	cancel         context.CancelFunc
	userID, device string
	leaseKey       string
	hash           [32]byte
	once           sync.Once
}

func New(c Config) (*Agent, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	a := &Agent{Config: c, grants: map[[32]byte]control.Grant{}, acknowledged: map[string]control.Counter{}, permits: map[*access]bool{}, limiters: map[string]*accountLimiters{}, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	b, e := os.ReadFile(c.StateFile)
	if e == nil {
		if json.Unmarshal(b, &a.state) != nil || len(a.state.BootID) != 32 || a.state.Counters == nil {
			return nil, errors.New("invalid agent counters; refusing to reset accounting")
		}
	} else if os.IsNotExist(e) {
		a.state = persisted{BootID: control.ID(), Counters: map[string]control.Counter{}}
		if e = a.save(); e != nil {
			return nil, e
		}
	} else {
		return nil, e
	}
	if a.state.Leases == nil {
		a.state.Leases = map[string]control.Lease{}
	}
	if !a.state.Clean {
		for key, l := range a.state.Leases {
			if !l.Closed && l.Budget > l.Used {
				a.state.RecoveryLeases = append(a.state.RecoveryLeases, l)
				delete(a.state.Leases, key)
			}
		}
	}
	a.state.Clean = false
	if e = a.save(); e != nil {
		return nil, e
	}
	return a, nil
}
func (a *Agent) save() error {
	b, e := json.Marshal(a.state)
	if e != nil {
		return e
	}
	return control.WriteFile(a.Config.StateFile, b)
}
func (a *Agent) Sync(ctx context.Context) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	a.mu.Lock()
	if e := a.save(); e != nil {
		a.mu.Unlock()
		return e
	}
	b := control.SyncRequest{BootID: a.state.BootID, Version: control.ConsoleVersion, Active: int64(len(a.permits)), Counters: []control.Counter{}, Capabilities: []string{"strict-billing", "leases-v1"}}
	for _, l := range a.state.RecoveryLeases {
		b.RecoveredLeases = append(b.RecoveredLeases, l.ID)
	}
	for _, c := range a.state.Counters {
		b.Counters = append(b.Counters, c)
	}
	a.mu.Unlock()
	if a.Connections != nil {
		b.Active = a.Connections()
	}
	raw, _ := json.Marshal(b)
	req, e := http.NewRequestWithContext(ctx, "POST", a.Config.APIURL+"/api/node/sync", bytes.NewReader(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+a.Config.AgentKey)
	req.Header.Set("X-TunnelX-Node", a.Config.NodeID)
	req.Header.Set("Content-Type", "application/json")
	res, e := a.client.Do(req)
	if e != nil {
		return errors.New("node control network unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("node control rejected snapshot")
	}
	var response control.SyncResponse
	if e = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&response); e != nil {
		return e
	}
	if response.ValidForSeconds < 1 || response.ValidForSeconds > 90 {
		return errors.New("invalid policy lifetime")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	grants := map[[32]byte]control.Grant{}
	for _, g := range response.Grants {
		if len(g.UserID) != 32 || len(g.Token) != 43 || g.Devices < 1 || g.Devices > 32 || !g.Unlimited && g.Remaining < 0 {
			return errors.New("invalid access policy")
		}
		grants[sha256.Sum256([]byte("Bearer "+g.Token))] = g
	}
	ack := map[string]control.Counter{}
	for _, c := range response.Acknowledged {
		ack[c.UserID] = c
	}
	for _, c := range b.Counters {
		if ack[c.UserID] != c {
			return errors.New("snapshot acknowledgement mismatch")
		}
	}
	a.grants = grants
	rates := map[string]int64{}
	for _, g := range grants {
		rates[g.UserID] = g.SpeedLimit
	}
	for user, l := range a.limiters {
		if rates[user] != l.rate {
			delete(a.limiters, user)
		}
	}
	cleared := map[string]bool{}
	for _, id := range response.ClearedRecovery {
		cleared[id] = true
	}
	recovery := a.state.RecoveryLeases[:0]
	for _, l := range a.state.RecoveryLeases {
		if !cleared[l.ID] {
			recovery = append(recovery, l)
		}
	}
	a.state.RecoveryLeases = recovery
	a.acknowledged = ack
	a.validUntil = time.Now().Add(time.Duration(response.ValidForSeconds) * time.Second)
	a.revokeLocked()
	return nil
}
func (a *Agent) allowedLocked(p *access) bool {
	g, ok := a.grants[p.hash]
	if !ok || g.UserID != p.userID || !time.Now().Before(a.validUntil) || g.ExpiresAt <= time.Now().Unix() {
		return false
	}
	if g.RequireLeases {
		l, ok := a.state.Leases[p.leaseKey]
		return ok && !l.Closed && l.ExpiresAt > time.Now().Unix() && l.Budget >= l.Used
	}
	c := a.state.Counters[g.UserID]
	ack := a.acknowledged[g.UserID]
	pending := c.Upload - ack.Upload + c.Download - ack.Download
	return g.Unlimited || pending < g.Remaining
}
func (a *Agent) revokeLocked() {
	for p := range a.permits {
		if !a.allowedLocked(p) {
			p.cancel()
		}
	}
}
func (a *Agent) Authorize(ctx context.Context, headers []string, device string) (server.Permit, bool) {
	b, e := hex.DecodeString(device)
	if e != nil || len(b) != 16 || len(headers) != 1 || len(headers[0]) > 128 {
		return nil, false
	}
	hash := sha256.Sum256([]byte(headers[0]))
	key := ""
	a.mu.Lock()
	initial, exists := a.grants[hash]
	a.mu.Unlock()
	if exists && initial.RequireLeases {
		key = initial.UserID + ":" + device
		if e := a.renewLease(ctx, key, initial.UserID, device, false); e != nil {
			return nil, false
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.grants[hash]
	if !ok {
		return nil, false
	}
	p := &access{agent: a, userID: g.UserID, device: device, hash: hash, leaseKey: key}
	if !a.allowedLocked(p) {
		return nil, false
	}
	devices := map[string]bool{}
	streams := 0
	for other := range a.permits {
		if other.userID == g.UserID && other.ctx.Err() == nil {
			devices[other.device] = true
			streams++
		}
	}
	if streams >= 128 || !devices[device] && len(devices) >= g.Devices {
		return nil, false
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	a.permits[p] = true
	return p, true
}
func (p *access) Context() context.Context { return p.ctx }
func (p *access) Allowed() bool {
	p.agent.mu.Lock()
	defer p.agent.mu.Unlock()
	ok := p.ctx.Err() == nil && p.agent.allowedLocked(p)
	if !ok {
		p.cancel()
	}
	return ok
}
func (p *access) Account(upload bool, n int) {
	if n <= 0 {
		return
	}
	a := p.agent
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.state.Counters[p.userID]
	c.UserID = p.userID
	if upload {
		c.Upload += int64(n)
	} else {
		c.Download += int64(n)
	}
	a.state.Counters[p.userID] = c
	if !a.allowedLocked(p) {
		p.cancel()
	}
}
func (p *access) SpeedLimit() int64 {
	p.agent.mu.Lock()
	defer p.agent.mu.Unlock()
	if g, ok := p.agent.grants[p.hash]; ok {
		return g.SpeedLimit
	}
	return 0
}
// Limiter shares one bucket per account and direction across all its streams.
func (p *access) Limiter(upload bool) *server.Limiter {
	a := p.agent
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.grants[p.hash]
	if !ok || g.SpeedLimit <= 0 {
		return nil
	}
	l := a.limiters[p.userID]
	if l == nil || l.rate != g.SpeedLimit {
		l = &accountLimiters{rate: g.SpeedLimit, up: server.NewLimiter(g.SpeedLimit), down: server.NewLimiter(g.SpeedLimit)}
		a.limiters[p.userID] = l
	}
	if upload {
		return l.up
	}
	return l.down
}
func (p *access) Close() {
	p.once.Do(func() { p.cancel(); p.agent.mu.Lock(); delete(p.agent.permits, p); p.agent.mu.Unlock() })
}
func (a *Agent) Run(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		syncTicker := time.NewTicker(5 * time.Second)
		saveTicker := time.NewTicker(5 * time.Second)
		expiry := time.NewTicker(time.Second)
		defer syncTicker.Stop()
		defer saveTicker.Stop()
		defer expiry.Stop()
		doSync := func() {
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			a.syncLeases(c)
			if e := a.Sync(c); e != nil {
				log.Print("Managed node policy/traffic sync unavailable; retained counters will retry")
			}
		}
		doSync()
		for {
			select {
			case <-ctx.Done():
				a.mu.Lock()
				a.validUntil = time.Time{}
				a.revokeLocked()
				e := a.save()
				a.mu.Unlock()
				if e != nil {
					log.Print("Managed node final accounting save failed")
				}
				final, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				a.syncLeases(final)
				cancel()
				a.mu.Lock()
				clean := true
				for _, l := range a.state.Leases {
					if !l.Closed {
						clean = false
					}
				}
				a.state.Clean = clean
				a.save()
				a.mu.Unlock()
				return
			case <-syncTicker.C:
				doSync()
			case <-expiry.C:
				a.mu.Lock()
				a.revokeLocked()
				a.mu.Unlock()
			case <-saveTicker.C:
				a.mu.Lock()
				e := a.save()
				if e != nil {
					a.validUntil = time.Time{}
					a.revokeLocked()
				}
				a.mu.Unlock()
				if e != nil {
					log.Print("Managed node accounting unavailable; managed access paused")
				}
			}
		}
	}()
	return func() { <-done }
}
