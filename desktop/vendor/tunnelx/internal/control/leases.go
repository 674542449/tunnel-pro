package control

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"time"
)

func entitledToNode(g Entitlement, n Node) bool {
	if g.Test != n.TestOnly {
		return false
	}
	if len(g.NodeIDs) == 0 {
		return true
	}
	for _, id := range g.NodeIDs {
		if id == n.ID {
			return true
		}
	}
	return false
}
func entitled(d *State, user string, test bool) bool {
	for _, g := range d.Entitlements {
		if g.UserID == user && g.Test == test {
			return true
		}
	}
	return false
}

// Outstanding reservations remain unavailable until the node reports final usage.
// A lost node cannot reuse the same quota on another node. This favors a safe stop
// over overspending; the operations page highlights reservations needing reconciliation.
func reserved(d *State, entitlementID, except string) int64 {
	total := int64(0)
	for _, l := range d.Leases {
		if l.ID == except || l.Closed {
			continue
		}
		for _, allocation := range l.Allocations {
			if allocation.EntitlementID == entitlementID {
				total += allocation.Budget - allocation.Used
			}
		}
	}
	return total
}
func reconcileLease(d *State, l *Lease, used int64) error {
	if used < l.Used || used > l.Budget {
		return errors.New("lease usage regression or overspend")
	}
	delta := used - l.Used
	for i := range l.Allocations {
		v := &l.Allocations[i]
		take := delta
		if room := v.Budget - v.Used; take > room {
			take = room
		}
		if take <= 0 {
			continue
		}
		found := false
		for j := range d.Entitlements {
			g := &d.Entitlements[j]
			if g.ID == v.EntitlementID {
				g.Used += take
				found = true
				break
			}
		}
		if !found {
			return errors.New("missing entitlement ledger")
		}
		v.Used += take
		delta -= take
	}
	if delta != 0 {
		return errors.New("lease allocation mismatch")
	}
	l.Used = used
	return nil
}
func leaseUpdate(d *State, n Node, b LeaseRequest, now int64) (Lease, error) {
	if !hasCapability(n, "leases-v1") || !n.Enabled || n.Pending() {
		return Lease{}, errors.New("node cannot lease")
	}
	device, e := hex.DecodeString(b.DeviceID)
	if e != nil || len(device) != 16 || len(b.UserID) != 32 || b.Used < 0 {
		return Lease{}, errors.New("invalid lease identity")
	}
	var l *Lease
	if b.ID != "" {
		for i := range d.Leases {
			if d.Leases[i].ID == b.ID {
				l = &d.Leases[i]
				break
			}
		}
		if l == nil || l.NodeID != n.ID || l.UserID != b.UserID || l.DeviceID != b.DeviceID {
			return Lease{}, errors.New("lease not owned")
		}
		if e = reconcileLease(d, l, b.Used); e != nil {
			return Lease{}, e
		}
		if b.Close {
			l.Closed = true
			l.ExpiresAt = now
			for i := range l.Allocations {
				l.Allocations[i].Budget = l.Allocations[i].Used
			}
			l.Budget = l.Used
			return *l, nil
		}
		if l.Closed {
			return Lease{}, errors.New("lease closed")
		}
	}
	u := findUser(d, b.UserID)
	if u == nil {
		return Lease{}, errors.New("missing user")
	}
	v := accountForNode(d, *u, n, now)
	if !v.Active(now) || v.NeedsEmailVerification && v.EmailVerifiedAt == 0 || !entitled(d, u.ID, n.TestOnly) {
		if l != nil {
			l.Closed = true
			l.ExpiresAt = now
			for i := range l.Allocations {
				l.Allocations[i].Budget = l.Allocations[i].Used
			}
			l.Budget = l.Used
			return *l, nil
		}
		return Lease{}, errors.New("account has no commercial grant")
	}
	devices := map[string]bool{}
	for _, old := range d.Leases {
		if old.UserID == u.ID && old.Test == n.TestOnly && !old.Closed && old.ExpiresAt > now {
			devices[old.DeviceID] = true
		}
	}
	if !devices[b.DeviceID] && len(devices) >= v.Devices {
		return Lease{}, errors.New("global device limit")
	}
	if l == nil {
		if b.Used != 0 || b.Close {
			return Lease{}, errors.New("new lease must start at zero")
		}
		d.Leases = append(d.Leases, Lease{ID: ID(), NodeID: n.ID, UserID: u.ID, DeviceID: b.DeviceID, Test: n.TestOnly})
		l = &d.Leases[len(d.Leases)-1]
	}
	// Each renewal reconciles actual usage, releases its old unconsumed reservation,
	// then reserves a bounded block. Expiry never releases unreported bytes.
	for i := range l.Allocations {
		l.Allocations[i].Budget = l.Allocations[i].Used
	}
	l.Budget = l.Used
	l.ExpiresAt = now + 20
	remaining := int64(64 << 20)
	for _, g := range d.Entitlements {
		if remaining <= 0 {
			break
		}
		if g.UserID != u.ID || g.RevokedAt > 0 || g.StartsAt > now || g.EndsAt <= now || !entitledToNode(g, n) {
			continue
		}
		available := g.Bytes - g.Used - reserved(d, g.ID, l.ID)
		if g.Bytes == 0 && g.Kind != "traffic" {
			available = remaining
		}
		if available <= 0 {
			continue
		}
		take := remaining
		if take > available {
			take = available
		}
		matched := false
		for i := range l.Allocations {
			allocation := &l.Allocations[i]
			if allocation.EntitlementID == g.ID {
				allocation.Budget += take
				matched = true
				break
			}
		}
		if !matched {
			l.Allocations = append(l.Allocations, LeaseAllocation{EntitlementID: g.ID, Budget: take})
		}
		l.Budget += take
		remaining -= take
		if g.EndsAt < l.ExpiresAt {
			l.ExpiresAt = g.EndsAt
		}
	}
	if l.Budget == l.Used {
		l.ExpiresAt = now
	}
	return *l, nil
}
func (a *API) nodeLease(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-TunnelX-Node")
	heads := r.Header.Values("Authorization")
	d := a.Store.Snapshot()
	n := findNode(&d, id)
	if n == nil || len(heads) != 1 || subtle.ConstantTimeCompare([]byte(heads[0]), []byte("Bearer "+n.AgentKey)) != 1 {
		fail(w, 403, "节点认证失败")
		return
	}
	var b LeaseRequest
	if !decode(w, r, &b) {
		return
	}
	var l Lease
	e := a.Store.Update(func(d *State) error {
		node := findNode(d, id)
		if node == nil || subtle.ConstantTimeCompare([]byte(heads[0]), []byte("Bearer "+node.AgentKey)) != 1 {
			return errors.New("node revoked")
		}
		var err error
		l, err = leaseUpdate(d, *node, b, time.Now().Unix())
		return err
	})
	if e != nil {
		fail(w, 409, "额度、设备或租约校验未通过")
		return
	}
	reply(w, 200, l)
}
