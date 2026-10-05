package control

import (
	"testing"
	"time"
)

func TestAuditTrafficPurchaseRequiresActiveMatchingSubscription(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, user := betaAccount(t, a, s, admin)
	addon := a.Store.Snapshot().Plans[0]
	addon.Kind, addon.Name, addon.ID = "traffic", "extra traffic", ""
	request(t, s, "admin/plans", admin, addon, 200)
	d := a.Store.Snapshot()
	id := d.Plans[len(d.Plans)-1].ID
	request(t, s, "orders", user, map[string]string{"plan_id": id}, 400)
	purchased(t, a, s, user)
	o := request(t, s, "orders", user, map[string]string{"plan_id": id}, 201)
	if e := a.Store.Update(func(d *State) error {
		for i := range d.Entitlements {
			if d.Entitlements[i].UserID == uid {
				d.Entitlements[i].EndsAt = time.Now().Unix() - 1
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	request(t, s, "orders/"+o["id"].(string)+"/checkout", user, map[string]any{}, 409)
	if len(a.Store.Snapshot().Payments) != 1 {
		t.Fatal("unfulfillable payment created")
	}
	state, _, owner, now := ledger(t)
	state.Entitlements[0].NodeIDs = []string{"node-a"}
	order := Order{UserID: owner.ID, Test: true, Plan: Plan{Kind: "traffic", NodeIDs: []string{"node-b"}}}
	if validatePurchase(&state, order, now) == nil {
		t.Fatal("nonmatching add-on accepted")
	}
}

func TestAuditReceivedMoneyRemainsRefundableAfterBaseExpires(t *testing.T) {
	for _, provider := range []string{"epay", "bepusdt", "stripe"} {
		t.Run(provider, func(t *testing.T) {
			d, _, user, now := ledger(t)
			d.Entitlements = nil
			d.Orders = []Order{{ID: ID(), UserID: user.ID, Status: "pending", Plan: Plan{Name: "addon", Kind: "traffic", PriceCents: 199, Currency: "cny", TrafficBytes: 1024}}}
			d.Payments = []Payment{{ID: ID(), OrderID: d.Orders[0].ID, Provider: provider, AmountCents: 199, Currency: "cny", Status: "pending"}}
			p := d.Payments[0]
			for i := 0; i < 2; i++ {
				if e := paidEvent(&d, p.ID, "real-receipt", 199, "cny", false, now); e != nil {
					t.Fatal(e)
				}
			}
			if d.Payments[0].Status != "paid" || d.Orders[0].Status != "paid_review" || len(d.Entitlements) != 0 || len(d.PaymentEvents) != 1 || len(d.Incidents) != 1 {
				t.Fatal("receipt lost, duplicated or access incorrectly granted")
			}
			if e := refundPayment(&d, p.ID, "refund-receipt", "verified refund", now+1); e != nil {
				t.Fatal(e)
			}
			if d.Orders[0].Status != "refunded" || d.Incidents[0].ResolvedAt == 0 {
				t.Fatal("unfulfilled receipt cannot be refunded and resolved")
			}
		})
	}
}

func TestAuditPlanNodeReferencesValidated(t *testing.T) {
	a, s, admin := commercialSetup(t)
	node := Node{ID: ID(), Name: "scoped node"}
	if e := a.Store.Update(func(d *State) error { d.Nodes = append(d.Nodes, node); return nil }); e != nil {
		t.Fatal(e)
	}
	plan := a.Store.Snapshot().Plans[0]
	plan.NodeIDs = []string{node.ID}
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	for _, ids := range [][]string{{ID()}, {node.ID, node.ID}} {
		plan.NodeIDs = ids
		request(t, s, "admin/plans/"+plan.ID, admin, plan, 409)
	}
	if got := a.Store.Snapshot().Plans[0].NodeIDs; len(got) != 1 || got[0] != node.ID {
		t.Fatal("invalid plan changed saved scope")
	}
}

func TestAuditAdminCannotDisableRequiredMFA(t *testing.T) {
	for _, role := range []string{"admin"} {
		t.Run(role, func(t *testing.T) {
			a, s, token := commercialSetup(t)
			a.Config.Commercial.RequireAdminMFA = true
			begin := request(t, s, "security/mfa/begin", token, map[string]string{"password": a.Config.AdminPassword}, 200)
			code, _ := totp(begin["secret"].(string), time.Now().Unix()/30)
			confirm := request(t, s, "security/mfa/confirm", token, map[string]string{"challenge_id": begin["challenge_id"].(string), "code": code}, 200)
			if e := a.Store.Update(func(d *State) error { d.Users[0].Role = role; return nil }); e != nil {
				t.Fatal(e)
			}
			request(t, s, "security/mfa/disable", token, map[string]string{"password": a.Config.AdminPassword, "code": confirm["recovery_codes"].([]any)[0].(string)}, 403)
			if a.Store.Snapshot().Users[0].MFASecret == "" {
				t.Fatal("required staff MFA disabled")
			}
		})
	}
}
