package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestAdminDeleteUserGuardsAndCleansUp(t *testing.T) {
	a, s, admin := commercialSetup(t)
	plan := a.Store.Snapshot().Plans[0]
	create := func(email string) string {
		request(t, s, "admin/users", admin, map[string]string{"email": email, "password": "delete-password-123", "plan_id": plan.ID}, 200)
		for _, u := range a.Store.Snapshot().Users {
			if u.Email == email {
				return u.ID
			}
		}
		t.Fatal("user not created")
		return ""
	}
	del := func(id, email string, status int) map[string]any {
		return request(t, s, "admin/users/"+id+"/delete", admin, map[string]string{"email": email}, status)
	}
	adminID := a.Store.Snapshot().Users[0].ID
	if out := del(adminID, "admin@example.com", 409); out["error"] != errUserDeleteAdmin.Error() {
		t.Fatal("administrator deleted", out)
	}

	uid := create("gone@example.test")
	token := request(t, s, "login", "", map[string]string{"email": "gone@example.test", "password": "delete-password-123"}, 200)["token"].(string)
	request(t, s, "tickets", token, map[string]string{"subject": "help", "body": "details"}, 200)
	if out := del(uid, "other@example.test", 409); out["error"] != errUserDeleteConfirm.Error() {
		t.Fatal("wrong confirmation accepted", out)
	}
	open := Lease{ID: ID(), NodeID: ID(), UserID: uid, DeviceID: ID(), Budget: 1}
	if e := a.Store.Update(func(d *State) error { d.Leases = append(d.Leases, open); return nil }); e != nil {
		t.Fatal(e)
	}
	if out := del(uid, "gone@example.test", 409); out["error"] != errUserDeleteLeases.Error() {
		t.Fatal("account with an open lease deleted", out)
	}
	if e := a.Store.Update(func(d *State) error { d.Leases[len(d.Leases)-1].Closed = true; return nil }); e != nil {
		t.Fatal(e)
	}
	del(uid, " Gone@Example.test ", 200)
	d := a.Store.Snapshot()
	if findUser(&d, uid) != nil || len(d.Entitlements) != 0 || len(d.Tickets) != 0 || len(d.Leases) != 0 {
		t.Fatal("deleted account left owned records behind")
	}
	for _, v := range d.Sessions {
		if v.UserID == uid {
			t.Fatal("session survived deletion")
		}
	}
	request(t, s, "me", token, nil, 401)
	if d.Audit[len(d.Audit)-1].Action != "user_deleted" || d.Audit[len(d.Audit)-1].SubjectName != "gone@example.test" {
		t.Fatal("deletion not audited with a readable subject")
	}

	// Nodes keep reporting counters of deleted accounts; sync must still succeed.
	node := Node{ID: ID(), Enabled: true, AgentKey: Token()}
	if e := a.Store.Update(func(d *State) error { d.Nodes = append(d.Nodes, node); return nil }); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(SyncRequest{BootID: ID(), Version: Version, Counters: []Counter{{UserID: uid, Upload: 10, Download: 20}}})
	req, _ := http.NewRequest("POST", s.URL+"/api/node/sync", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+node.AgentKey)
	req.Header.Set("X-TunnelX-Node", node.ID)
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var sync SyncResponse
	json.NewDecoder(res.Body).Decode(&sync)
	res.Body.Close()
	if res.StatusCode != 200 || len(sync.Acknowledged) != 1 {
		t.Fatal("node sync broke after deleting an account", res.StatusCode)
	}

	paid := create("paid@example.test")
	if e := a.Store.Update(func(d *State) error {
		o := Order{ID: ID(), UserID: paid, Plan: plan, Status: "pending", CreatedAt: time.Now().Unix()}
		d.Orders = append(d.Orders, o)
		d.Payments = append(d.Payments, Payment{ID: ID(), OrderID: o.ID, Provider: "test", Status: "pending"})
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if out := del(paid, "paid@example.test", 409); out["error"] != errUserDeletePaid.Error() {
		t.Fatal("account with payment history deleted", out)
	}
}
