package control

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresAdminAssignmentConcurrentRetryAndRestart(t *testing.T) {
	c := pgConfig(t)
	one, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	u := User{ID: ID(), Email: "assigned@example.test", Role: "user", Beta: true}
	if err = one.Update(func(d *State) error { d.Users = append(d.Users, u); return nil }); err != nil {
		t.Fatal(err)
	}
	d := one.Snapshot()
	actor := d.Users[0].ID
	b := assignmentRequest{PlanID: d.Plans[0].ID, Mode: "immediate", Test: true, Reason: "数据库并发重复提交验证", RequestID: ID()}
	two, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := one
			if i%2 == 1 {
				s = two
			}
			errs <- s.Update(func(d *State) error { return assignPlan(d, actor, u.ID, b, time.Now().Unix()) })
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	d = restarted.Snapshot()
	if len(d.Entitlements) != 1 || d.Entitlements[0].RequestID != b.RequestID || d.Entitlements[0].PlanName == "" {
		t.Fatal("duplicate grant or lost assignment metadata")
	}
	count := 0
	for _, v := range d.Audit {
		if v.Action == "user_plan_assigned" {
			count++
			if len(v.Changes) == 0 {
				t.Fatal("lost audit details")
			}
		}
	}
	if count != 1 {
		t.Fatal("duplicate audit")
	}
}

func TestAdminAssignmentExhaustionExpiryUsageAndIdempotency(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, token := betaAccount(t, a, s, admin)
	d := a.Store.Snapshot()
	p := d.Plans[0]
	b := assignmentRequest{PlanID: p.ID, Mode: "immediate", Test: true, Reason: "客服为用户补充套餐", RequestID: ID()}
	request(t, s, "admin/users/"+uid+"/assign-plan", token, b, 403)
	request(t, s, "admin/users/"+uid+"/assign-plan", admin, b, 200)
	request(t, s, "admin/users/"+uid+"/assign-plan", admin, b, 200)
	d = a.Store.Snapshot()
	if len(d.Entitlements) != 1 || len(d.Payments) != 0 || len(d.Orders) != 0 {
		t.Fatal("assignment duplicated or created payment records")
	}
	g := d.Entitlements[0]
	if g.PlanName != p.Name || g.AssignedBy == "" || g.Bytes != p.TrafficBytes || !g.Test {
		t.Fatal(g)
	}
	audited := false
	for _, v := range d.Audit {
		if v.Action == "user_plan_assigned" {
			audited = true
			if len(v.Changes) == 0 || !strings.Contains(v.SubjectName, p.Name) {
				t.Fatal("assignment audit missing details")
			}
		}
	}
	if !audited {
		t.Fatal("missing assignment audit")
	}
	if err := a.Store.Update(func(d *State) error {
		d.Entitlements[0].Used = g.Bytes
		d.TestUsage[uid] = Counter{UserID: uid, Upload: 99999999999}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	me := request(t, s, "me", token, nil, 200)["user"].(map[string]any)
	traffic := me["traffic"].(map[string]any)
	if me["expires_at"] != float64(g.EndsAt) || me["subscription_status"] != "valid" || me["traffic_exhausted"] != true || me["active"] != false {
		t.Fatal(me)
	}
	if traffic["total_bytes"] != float64(g.Bytes) || traffic["used_bytes"] != float64(g.Bytes) || traffic["remaining_bytes"] != float64(0) {
		t.Fatal("lifetime counters contaminated current quota", traffic)
	}
	d = a.Store.Snapshot()
	u := *findUser(&d, uid)
	v := accountForNode(&d, u, Node{TestOnly: true}, time.Now().Unix())
	if v.Active(time.Now().Unix()) || v.ExpiresAt != g.EndsAt {
		t.Fatal("exhaustion altered expiry or authorized account")
	}
	// A second future period must not refill the exhausted current period.
	b.RequestID, b.Mode = ID(), "renew"
	request(t, s, "admin/users/"+uid+"/assign-plan", admin, b, 200)
	d = a.Store.Snapshot()
	if d.Entitlements[1].StartsAt != g.EndsAt {
		t.Fatal("renewal did not queue")
	}
	me = request(t, s, "me", token, nil, 200)["user"].(map[string]any)
	if me["active"] != false || me["traffic"].(map[string]any)["total_bytes"] != float64(g.Bytes) {
		t.Fatal("future quota used early")
	}
	// Immediate assignments replenish access without erasing used quota.
	b.RequestID, b.Mode = ID(), "immediate"
	request(t, s, "admin/users/"+uid+"/assign-plan", admin, b, 200)
	me = request(t, s, "me", token, nil, 200)["user"].(map[string]any)
	if me["active"] != true || me["traffic"].(map[string]any)["remaining_bytes"] != float64(g.Bytes) {
		t.Fatal("immediate grant failed")
	}
	b.Reason = "更改已完成请求的内容"
	request(t, s, "admin/users/"+uid+"/assign-plan", admin, b, 409)
	// Stored entitlement metadata survives the state serialization used by both stores.
	d = a.Store.Snapshot()
	raw, _ := json.Marshal(d)
	var restored State
	if err := json.Unmarshal(raw, &restored); err != nil || restored.Entitlements[0].PlanName != p.Name {
		t.Fatal("assignment metadata lost")
	}
}

func TestAccountSummaryUnlimitedZeroCountersExpiredAndNodeScope(t *testing.T) {
	now := time.Now().Unix()
	u := User{ID: ID(), Role: "user", Beta: true}
	g := Entitlement{ID: ID(), UserID: u.ID, Test: true, Kind: "subscription", StartsAt: now - 10, EndsAt: now + 3600, Bytes: 100, Used: 100, Devices: 2, NodeIDs: []string{"allowed"}}
	d := State{Users: []User{u}, Entitlements: []Entitlement{g}}
	v := accountForNode(&d, u, Node{TestOnly: true}, now)
	if v.Active(now) || v.ExpiresAt != g.EndsAt {
		t.Fatal("zero counters turned exhaustion into unlimited")
	}
	d.Entitlements[0].Bytes = 0
	pub := accountPublic(&d, u, true, now)
	if pub["active"] != true || pub["traffic_exhausted"] != false || pub["traffic"].(map[string]any)["unlimited"] != true {
		t.Fatal(pub)
	}
	n := Node{ID: "denied", TestOnly: true, Capabilities: []string{"strict-billing", "leases-v1"}}
	if accountForNode(&d, u, n, now).Active(now) {
		t.Fatal("summary removed node scope")
	}
	pub = accountPublic(&d, u, true, g.EndsAt+1)
	if pub["subscription_status"] != "expired" || pub["expires_at"] != g.EndsAt || pub["traffic_exhausted"] != false {
		t.Fatal(pub)
	}
}

func TestAdminAssignmentRejectsInvalidScopeDisabledPlanAndMissingBase(t *testing.T) {
	now := time.Now().Unix()
	u := User{ID: ID(), Role: "user", Beta: true}
	p := Plan{ID: ID(), Name: "流量包", Enabled: true, Kind: "traffic", TrafficBytes: 1024, Days: 30, Devices: 1}
	d := State{Users: []User{u}, Plans: []Plan{p}}
	b := assignmentRequest{PlanID: p.ID, Mode: "immediate", Test: true, Reason: "测试分配校验", RequestID: ID()}
	if assignPlan(&d, ID(), u.ID, b, now) == nil {
		t.Fatal("add-on without base allowed")
	}
	d.Plans[0].Kind = "subscription"
	d.Plans[0].Enabled = false
	if assignPlan(&d, ID(), u.ID, b, now) == nil {
		t.Fatal("disabled plan allowed")
	}
	d.Plans[0].Enabled = true
	d.Users[0].Beta = false
	if assignPlan(&d, ID(), u.ID, b, now) == nil {
		t.Fatal("test rights assigned to non-beta")
	}
	if len(d.Entitlements) != 0 {
		t.Fatal("rejected assignment modified ledger")
	}
}
