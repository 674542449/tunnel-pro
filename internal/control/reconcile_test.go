package control

import (
	"testing"
	"time"
)

func TestCommercialManualReconciliationRejectsLiveLease(t *testing.T) {
	a, s, admin := commercialSetup(t)
	d, n, u, now := ledger(t)
	lease, e := leaseUpdate(&d, n, LeaseRequest{UserID: u.ID, DeviceID: ID()}, now)
	if e != nil {
		t.Fatal(e)
	}
	a.Store.Update(func(v *State) error {
		v.Users = append(v.Users, u)
		n.LastSeen = now
		v.Nodes = append(v.Nodes, n)
		v.Entitlements = d.Entitlements
		v.Leases = d.Leases
		return nil
	})
	request(t, s, "admin/leases/"+lease.ID+"/reconcile", admin, map[string]any{"used": 100, "reason": "verified node checkpoint"}, 409)
	a.Store.Update(func(v *State) error {
		v.Leases[0].Uncertain = true
		v.Leases[0].ExpiresAt = time.Now().Unix() - 1
		return nil
	})
	request(t, s, "admin/leases/"+lease.ID+"/reconcile", admin, map[string]any{"used": 100, "reason": "verified node checkpoint"}, 200)
	request(t, s, "admin/leases/"+lease.ID+"/reconcile", admin, map[string]any{"used": 100, "reason": "duplicate reconciliation"}, 200)
	after := a.Store.Snapshot()
	if after.Entitlements[0].Used != 100 || !after.Leases[0].Closed || reserved(&after, after.Entitlements[0].ID, "") != 0 {
		t.Fatal("manual reconciliation duplicated usage or failed to free unused quota")
	}
}
