package control

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"tunnelx/internal/config"
	"tunnelx/internal/pki"
)

func TestNodeScopeHonoursChoiceAndGuardsChanges(t *testing.T) {
	a, s, admin := commercialSetup(t)
	ca, _, _, err := pki.Certificate("edge.tunnelx.invalid")
	if err != nil {
		t.Fatal(err)
	}
	_, ech, err := pki.ECH("proxy.test")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(Node{Name: "scope-node", CAPEM: string(ca), Client: config.Client{ServerName: "edge.tunnelx.invalid", ServerIP: "203.0.113.20", Port: 8443, ECHConfig: base64.StdEncoding.EncodeToString(ech)}})
	var body map[string]any
	json.Unmarshal(raw, &body)
	body["test_only"] = false
	request(t, s, "admin/nodes", admin, body, 200)
	n := a.Store.Snapshot().Nodes[0]
	if n.TestOnly {
		t.Fatal("explicit production choice was overridden in simulated-payment mode")
	}
	scope := func(test bool, status int) map[string]any {
		return request(t, s, "admin/nodes/"+n.ID+"/scope", admin, map[string]bool{"test_only": test}, status)
	}
	scope(true, 200)
	if !a.Store.Snapshot().Nodes[0].TestOnly {
		t.Fatal("scope change was not saved")
	}
	plan := a.Store.Snapshot().Plans[0]
	plan.NodeIDs = []string{n.ID}
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	if out := scope(false, 409); out["error"] != errNodeScopeReferenced.Error() {
		t.Fatal("pinned node changed scope", out)
	}
	plan.NodeIDs = nil
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	lease := Lease{ID: ID(), NodeID: n.ID, UserID: ID(), DeviceID: ID(), Budget: 1 << 20, Test: true}
	if e := a.Store.Update(func(d *State) error { d.Leases = append(d.Leases, lease); return nil }); e != nil {
		t.Fatal(e)
	}
	if out := scope(false, 409); out["error"] != errNodeScopeLeases.Error() {
		t.Fatal("node with an open lease changed scope", out)
	}
	if e := a.Store.Update(func(d *State) error { d.Leases[0].Closed = true; return nil }); e != nil {
		t.Fatal(e)
	}
	scope(false, 200)
	d := a.Store.Snapshot()
	if d.Nodes[0].TestOnly || d.Audit[len(d.Audit)-1].Action != "node_scope_changed" {
		t.Fatal("scope change was not saved or audited")
	}
}
