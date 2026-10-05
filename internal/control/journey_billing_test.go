package control

import (
	"net/http/httptest"
	"testing"
	"time"
)

func journeyBuyPlan(t *testing.T, s *httptest.Server, token, planID string) string {
	t.Helper()
	o := request(t, s, "orders", token, map[string]string{"plan_id": planID}, 201)
	v := request(t, s, "orders/"+o["id"].(string)+"/checkout", token, map[string]any{}, 200)
	id := v["payment"].(map[string]any)["id"].(string)
	request(t, s, "payments/"+id+"/test-confirm", token, map[string]any{}, 200)
	return id
}

func journeyGrant(t *testing.T, d *State, paymentID string) Entitlement {
	t.Helper()
	p := findPayment(d, paymentID)
	if p == nil {
		t.Fatal("missing payment")
	}
	for _, g := range d.Entitlements {
		if g.OrderID == p.OrderID {
			return g
		}
	}
	t.Fatal("missing purchase grant")
	return Entitlement{}
}

func TestJourneyRefundedCycleAddonDoesNotDelayNewSubscription(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, user := betaAccount(t, a, s, admin)
	plan := a.Store.Snapshot().Plans[0]
	addon := plan
	addon.ID, addon.Name, addon.Kind = "", "current cycle traffic", "traffic"
	addon.TrafficBytes = 512
	request(t, s, "admin/plans", admin, addon, 200)
	d := a.Store.Snapshot()
	addonID := d.Plans[len(d.Plans)-1].ID
	basePayment := journeyBuyPlan(t, s, user, plan.ID)
	addonPayment := journeyBuyPlan(t, s, user, addonID)
	d = a.Store.Snapshot()
	base, extra := journeyGrant(t, &d, basePayment), journeyGrant(t, &d, addonPayment)
	if extra.EndsAt != base.EndsAt {
		t.Fatal("add-on did not follow the active cycle")
	}
	request(t, s, "admin/payments/"+basePayment+"/refund", admin, map[string]string{"reason": "refund current subscription"}, 200)
	d = a.Store.Snapshot()
	u := *findUser(&d, uid)
	node := Node{TestOnly: true}
	if accountForNode(&d, u, node, time.Now().Unix()).Active(time.Now().Unix()) {
		t.Fatal("orphan add-on grants service without a subscription")
	}
	before := time.Now().Unix()
	replacement := journeyBuyPlan(t, s, user, plan.ID)
	d = a.Store.Snapshot()
	current := journeyGrant(t, &d, replacement)
	if current.StartsAt < before || current.StartsAt > time.Now().Unix() || !accountForNode(&d, u, node, time.Now().Unix()).Active(time.Now().Unix()) {
		t.Fatal("new subscription was delayed by the refunded cycle's remaining add-on")
	}
	if current.EndsAt-current.StartsAt != int64(plan.Days)*86400 {
		t.Fatal("new purchase lost part of its paid duration")
	}
	reopened, err := OpenStore(a.Config)
	if err != nil {
		t.Fatal(err)
	}
	restarted := reopened.Snapshot()
	if got := journeyGrant(t, &restarted, replacement); got.StartsAt != current.StartsAt || got.EndsAt != current.EndsAt {
		t.Fatal("restart changed the replacement cycle")
	}
}

func TestJourneyQueuedRefundQuotaAddonAndRestart(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, user := betaAccount(t, a, s, admin)
	node := Node{ID: ID(), Enabled: true, TestOnly: true, Capabilities: []string{"strict-billing", "leases-v1"}}
	other := node
	other.ID = ID()
	if err := a.Store.Update(func(d *State) error {
		d.Nodes = append(d.Nodes, node, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plan := a.Store.Snapshot().Plans[0]
	plan.NodeIDs, plan.TrafficBytes, plan.Days = []string{node.ID}, 1024, 1
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	first := journeyBuyPlan(t, s, user, plan.ID)
	middle := journeyBuyPlan(t, s, user, plan.ID)
	last := journeyBuyPlan(t, s, user, plan.ID)
	d := a.Store.Snapshot()
	current, queued, tail := journeyGrant(t, &d, first), journeyGrant(t, &d, middle), journeyGrant(t, &d, last)
	if queued.StartsAt != current.EndsAt || tail.StartsAt != queued.EndsAt {
		t.Fatal("advance renewals overlap or leave a service gap")
	}
	request(t, s, "admin/payments/"+middle+"/refund", admin, map[string]string{"reason": "refund queued renewal"}, 200)
	d = a.Store.Snapshot()
	if got := journeyGrant(t, &d, last); got.StartsAt != current.EndsAt || got.EndsAt-got.StartsAt != 86400 {
		t.Fatal("queued refund left a gap or changed the following paid duration")
	}
	request(t, s, "admin/payments/"+first+"/refund", admin, map[string]string{"reason": "refund active subscription"}, 200)
	d = a.Store.Snapshot()
	now := time.Now().Unix()
	if got := journeyGrant(t, &d, last); got.StartsAt > now || got.EndsAt-got.StartsAt != 86400 {
		t.Fatal("current refund did not activate the queued renewal")
	}
	u := *findUser(&d, uid)
	if accountForNode(&d, u, other, now).Active(now) {
		t.Fatal("restricted purchase authorizes an unrelated node")
	}
	production := node
	production.TestOnly = false
	if accountForNode(&d, u, production, now).Active(now) {
		t.Fatal("test purchase authorizes a production node")
	}
	device := ID()
	if err := a.Store.Update(func(d *State) error {
		lease, err := leaseUpdate(d, node, LeaseRequest{UserID: uid, DeviceID: device}, now)
		if err != nil {
			return err
		}
		if lease.Budget != 1024 {
			t.Fatalf("paid quota = %d, want 1024", lease.Budget)
		}
		_, err = leaseUpdate(d, node, LeaseRequest{ID: lease.ID, UserID: uid, DeviceID: device, Used: lease.Budget, Close: true}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d = a.Store.Snapshot()
	if accountForNode(&d, u, node, now).Active(now) {
		t.Fatal("exhausted quota still grants access")
	}
	addon := plan
	addon.ID, addon.Name, addon.Kind, addon.TrafficBytes = "", "restore exhausted quota", "traffic", 512
	request(t, s, "admin/plans", admin, addon, 200)
	d = a.Store.Snapshot()
	addonPayment := journeyBuyPlan(t, s, user, d.Plans[len(d.Plans)-1].ID)
	d = a.Store.Snapshot()
	now = time.Now().Unix()
	if !accountForNode(&d, u, node, now).Active(now) || journeyGrant(t, &d, addonPayment).EndsAt != journeyGrant(t, &d, last).EndsAt {
		t.Fatal("add-on did not restore the exhausted active cycle")
	}
	if err := a.Store.Update(func(d *State) error {
		lease, err := leaseUpdate(d, node, LeaseRequest{UserID: uid, DeviceID: device}, now)
		if err == nil && lease.Budget != 512 {
			t.Fatalf("add-on quota = %d, want 512", lease.Budget)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(a.Config)
	if err != nil {
		t.Fatal(err)
	}
	restarted := reopened.Snapshot()
	extra := journeyGrant(t, &restarted, addonPayment)
	if journeyGrant(t, &restarted, last).Used != 1024 || reserved(&restarted, extra.ID, "") != 512 {
		t.Fatal("restart lost consumed quota or reissued an outstanding reservation")
	}
	request(t, s, "admin/payments/"+addonPayment+"/refund", admin, map[string]string{"reason": "refund unused add-on"}, 200)
	d = a.Store.Snapshot()
	if accountForNode(&d, u, node, now).Active(now) {
		t.Fatal("refunded add-on resurrected exhausted base quota")
	}
}

func TestJourneyMissingSnapshotNodeBlocksNewCheckout(t *testing.T) {
	a, s, admin := commercialSetup(t)
	_, user := betaAccount(t, a, s, admin)
	node := Node{ID: ID(), TestOnly: true}
	if err := a.Store.Update(func(d *State) error {
		d.Nodes = append(d.Nodes, node)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plan := a.Store.Snapshot().Plans[0]
	plan.NodeIDs = []string{node.ID}
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	order := request(t, s, "orders", user, map[string]string{"plan_id": plan.ID}, 201)
	// Reproduce a stale order from a prior release or restored backup whose node
	// no longer exists. New admin deletion guards separately prevent this state.
	if err := a.Store.Update(func(d *State) error {
		d.Nodes = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request(t, s, "orders/"+order["id"].(string)+"/checkout", user, map[string]any{}, 409)
	request(t, s, "orders", user, map[string]string{"plan_id": plan.ID}, 400)
	if len(a.Store.Snapshot().Payments) != 0 {
		t.Fatal("unfulfillable node snapshot reached payment creation")
	}
}

func TestJourneyDeletedNodeLateReceiptRemainsRefundable(t *testing.T) {
	for _, provider := range []string{"epay", "bepusdt", "stripe"} {
		t.Run(provider, func(t *testing.T) {
			d, _, user, now := ledger(t)
			d.Entitlements = nil
			d.Orders = []Order{{ID: ID(), UserID: user.ID, Status: "cancelled", ExpiresAt: now - 1, Plan: Plan{Name: "retired node subscription", Kind: "subscription", PriceCents: 199, Currency: "cny", Days: 30, TrafficBytes: 1024, NodeIDs: []string{ID()}}}}
			d.Payments = []Payment{{ID: ID(), OrderID: d.Orders[0].ID, Provider: provider, AmountCents: 199, Currency: "cny", Status: "pending"}}
			payment := d.Payments[0].ID
			for i := 0; i < 2; i++ {
				if err := paidEvent(&d, payment, "late-real-receipt", 199, "cny", false, now); err != nil {
					t.Fatal(err)
				}
			}
			if d.Payments[0].Status != "paid" || d.Orders[0].Status != "paid_review" || len(d.Entitlements) != 0 || len(d.PaymentEvents) != 1 {
				t.Fatal("real money was lost or unusable node access was granted")
			}
			if err := refundPayment(&d, payment, "verified-external-refund", "node no longer available", now+1); err != nil {
				t.Fatal(err)
			}
			if d.Orders[0].Status != "refunded" || len(d.Refunds) != 1 {
				t.Fatal("unfulfilled late receipt cannot be refunded")
			}
			for _, incident := range d.Incidents {
				if incident.Key == "payment-fulfilment:"+payment && incident.ResolvedAt == 0 {
					t.Fatal("refund did not resolve the fulfilment incident")
				}
			}
		})
	}
}

func TestJourneyExistingCheckoutRevalidatesAddonBase(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, user := betaAccount(t, a, s, admin)
	plan := a.Store.Snapshot().Plans[0]
	journeyBuyPlan(t, s, user, plan.ID)
	addon := plan
	addon.ID, addon.Kind, addon.Name = "", "traffic", "pending checkout addon"
	request(t, s, "admin/plans", admin, addon, 200)
	d := a.Store.Snapshot()
	o := request(t, s, "orders", user, map[string]string{"plan_id": d.Plans[len(d.Plans)-1].ID}, 201)
	path := "orders/" + o["id"].(string) + "/checkout"
	request(t, s, path, user, map[string]any{}, 200)
	if err := a.Store.Update(func(d *State) error {
		for i := range d.Entitlements {
			if d.Entitlements[i].UserID == uid {
				d.Entitlements[i].EndsAt = time.Now().Unix() - 1
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request(t, s, path, user, map[string]any{}, 409)
	if got := len(a.Store.Snapshot().Payments); got != 2 {
		t.Fatalf("invalid resumed checkout changed payment count: %d", got)
	}
}
