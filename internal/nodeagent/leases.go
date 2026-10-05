package nodeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"tunnelx/internal/control"
)

func (a *Agent) renewLease(ctx context.Context, key, user, device string, closeLease bool) error {
	a.leaseMu.Lock()
	defer a.leaseMu.Unlock()
	a.mu.Lock()
	old := a.state.Leases[key]
	if closeLease {
		for p := range a.permits {
			if p.leaseKey == key && p.ctx.Err() == nil {
				a.mu.Unlock()
				return nil
			}
		}
	}
	if !closeLease && old.ExpiresAt > time.Now().Unix()+8 && old.Budget-old.Used > 8<<20 {
		a.mu.Unlock()
		return nil
	}
	if old.Closed {
		old = control.Lease{}
	}
	if closeLease && old.ID == "" {
		a.mu.Unlock()
		return nil
	}
	if e := a.save(); e != nil {
		a.mu.Unlock()
		return e
	}
	a.mu.Unlock()
	request := control.LeaseRequest{ID: old.ID, UserID: user, DeviceID: device, Used: old.Used, Close: closeLease}
	var renewed control.Lease
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, _ := json.Marshal(request)
	req, e := http.NewRequestWithContext(c, "POST", a.Config.APIURL+"/api/node/lease", bytes.NewReader(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+a.Config.AgentKey)
	req.Header.Set("X-TunnelX-Node", a.Config.NodeID)
	req.Header.Set("Content-Type", "application/json")
	res, e := a.client.Do(req)
	if e != nil {
		return errors.New("lease control unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("lease control rejected")
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&renewed); e != nil {
		return e
	}
	if renewed.UserID != user || renewed.DeviceID != device || renewed.NodeID != a.Config.NodeID || renewed.Budget < renewed.Used || renewed.ExpiresAt > time.Now().Unix()+25 {
		return errors.New("invalid lease response")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.state.Leases[key]
	if current.ID == old.ID && current.Used > old.Used {
		renewed.Used += current.Used - old.Used
	}
	a.state.Leases[key] = renewed
	return a.save()
}
func (a *Agent) syncLeases(ctx context.Context) {
	a.mu.Lock()
	items := map[string]control.Lease{}
	for key, l := range a.state.Leases {
		if !l.Closed {
			items[key] = l
		}
	}
	a.mu.Unlock()
	for key, l := range items {
		a.mu.Lock()
		active := false
		for p := range a.permits {
			if p.leaseKey == key && p.ctx.Err() == nil {
				active = true
				break
			}
		}
		a.mu.Unlock()
		a.renewLease(ctx, key, l.UserID, l.DeviceID, !active)
	}
}

// Charge clips the last TCP read to the remaining quota before forwarding it.
func (p *access) Charge(upload bool, n int) int {
	if n <= 0 {
		return 0
	}
	a := p.agent
	if p.leaseKey != "" {
		a.mu.Lock()
		l := a.state.Leases[p.leaseKey]
		room := l.Budget - l.Used
		expired := l.ExpiresAt <= time.Now().Unix()
		a.mu.Unlock()
		if room < int64(n) || expired {
			if e := a.renewLease(p.ctx, p.leaseKey, p.userID, p.device, false); e != nil {
				p.cancel()
				return 0
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p.ctx.Err() != nil || !a.allowedLocked(p) {
		return 0
	}
	g := a.grants[p.hash]
	allowed := int64(n)
	if g.RequireLeases {
		l := a.state.Leases[p.leaseKey]
		if allowed > l.Budget-l.Used {
			allowed = l.Budget - l.Used
		}
		l.Used += allowed
		a.state.Leases[p.leaseKey] = l
	} else if !g.Unlimited {
		c := a.state.Counters[p.userID]
		ack := a.acknowledged[p.userID]
		left := g.Remaining - (c.Upload - ack.Upload + c.Download - ack.Download)
		if allowed > left {
			allowed = left
		}
	}
	if allowed <= 0 {
		return 0
	}
	c := a.state.Counters[p.userID]
	c.UserID = p.userID
	if upload {
		c.Upload += allowed
	} else {
		c.Download += allowed
	}
	a.state.Counters[p.userID] = c
	return int(allowed)
}
