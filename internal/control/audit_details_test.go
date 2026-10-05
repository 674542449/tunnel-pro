package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func findAuditChange(t *testing.T, event Audit, field string) AuditChange {
	t.Helper()
	for _, change := range event.Changes {
		if change.Field == field {
			return change
		}
	}
	t.Fatalf("audit action %s lacks change %s", event.Action, field)
	return AuditChange{}
}

func TestAuditDetailsAPIChangesPersistAndRefundActor(t *testing.T) {
	a, s, admin := commercialSetup(t)
	plan := a.Store.Snapshot().Plans[0]
	plan.Name, plan.PriceCents, plan.Devices = "Audit subscription", 1999, 7
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	d := a.Store.Snapshot()
	change := d.Audit[len(d.Audit)-1]
	if change.ActorName != a.Config.AdminEmail || change.SubjectName != plan.Name || change.Summary != "保存套餐" {
		t.Fatal("plan audit has no readable actor, subject or summary")
	}
	if price := findAuditChange(t, change, "price"); price.Before != "0.00 CNY" || price.After != "19.99 CNY" {
		t.Fatalf("wrong price diff: %+v", price)
	}
	if devices := findAuditChange(t, change, "devices"); devices.Before != "3" || devices.After != "7" {
		t.Fatal("wrong device limit diff")
	}
	uid, user := betaAccount(t, a, s, admin)
	paid := journeyBuyPlan(t, s, user, plan.ID)
	request(t, s, "admin/payments/"+paid+"/refund", admin, map[string]string{"reason": "audit actor verification"}, 200)
	d = a.Store.Snapshot()
	var found bool
	for _, event := range d.Audit {
		if event.Action != "payment_refunded" {
			continue
		}
		found = true
		if event.Actor == uid || event.Actor != d.Users[0].ID || event.ActorName != a.Config.AdminEmail {
			t.Fatal("refund was attributed to the buyer instead of the administrator")
		}
		if status := findAuditChange(t, event, "status"); status.Before != "paid" || status.After != "refunded" {
			t.Fatal("refund did not record the order state transition")
		}
	}
	if !found {
		t.Fatal("missing refund audit")
	}
	reopened, err := OpenStore(a.Config)
	if err != nil {
		t.Fatal(err)
	}
	restarted := reopened.Snapshot()
	before, _ := json.Marshal(d.Audit)
	after, _ := json.Marshal(restarted.Audit)
	if !bytes.Equal(before, after) {
		t.Fatal("restart lost detailed audit fields")
	}
}

func TestAuditWhitelistRedactsSecretsAndBodies(t *testing.T) {
	a, _, _ := commercialSetup(t)
	markers := []string{"password-raw-marker", "password-hash-marker", "tunnel-token-marker", "mfa-secret-marker", "recovery-hash-marker", "gateway-secret-marker", "agent-key-marker", "ca-pem-marker", "mail-body-marker", "ticket-body-marker", "url-user-marker", "url-pass-marker", "url-query-marker", "url-fragment-marker", "private-path-marker", "announcement-marker", "release-notes-marker", "lease-reason-marker"}
	err := a.Store.Update(func(d *State) error {
		actor := d.Users[0].ID
		u := &d.Users[0]
		u.PasswordHash, u.TunnelToken, u.MFASecret, u.RecoveryHashes = markers[1], markers[2], markers[3], markers[4]
		record(d, actor, "password_changed", actor)
		url := "https://url-user-marker:url-pass-marker@pay.example.test/private-path-marker?key=url-query-marker#url-fragment-marker"
		d.PaymentConfig = &GatewaySettings{Mode: "gateways", Epay: GatewayConfig{URL: url, Secret: markers[5], MerchantID: "merchant-visible", Enabled: true, Alipay: true}}
		record(d, actor, "payment_settings_saved", "")
		d.Announcement, d.Release.Notes, d.Release.URL = markers[15], markers[16], url
		record(d, actor, "settings_saved", "")
		node := Node{ID: ID(), Name: "Readable node", AgentKey: markers[6], CAPEM: markers[7]}
		d.Nodes = append(d.Nodes, node)
		record(d, actor, "node_saved", node.ID)
		ticket := Ticket{ID: ID(), Subject: "Readable ticket", Status: "open", Replies: []TicketReply{{Body: markers[9]}}}
		d.Tickets = append(d.Tickets, ticket)
		d.Outbox = append(d.Outbox, MailMessage{ID: ID(), Body: markers[8]})
		record(d, actor, "ticket_created", ticket.ID)
		record(d, actor, "lease_reconciled:"+markers[17], ID())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d := a.Store.Snapshot()
	raw, _ := json.Marshal(d.Audit)
	for _, marker := range markers {
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("audit leaked protected field %q", marker)
		}
	}
	if !bytes.Contains(raw, []byte("https://pay.example.test")) || !bytes.Contains(raw, []byte("merchant-visible")) || !bytes.Contains(raw, []byte("Readable ticket")) {
		t.Fatal("safe audit context was unnecessarily discarded")
	}
	if err = a.Store.Update(func(d *State) error {
		d.PaymentConfig.Epay.Secret = "rotated-private-gateway-marker"
		d.PaymentConfig.Epay.URL = "https://pay.example.test/second-private-path-marker?secret=second-query-marker"
		record(d, d.Users[0].ID, "payment_settings_saved", "")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d = a.Store.Snapshot()
	last := d.Audit[len(d.Audit)-1]
	if key := findAuditChange(t, last, "epay.key"); key.Before != "已配置" || key.After != "已配置（已更新）" {
		t.Fatal("key rotation was not recorded safely")
	}
	if host := findAuditChange(t, last, "epay.host"); host.After != "https://pay.example.test（已更新）" {
		t.Fatal("same-host gateway URL update disappeared")
	}
	raw, _ = json.Marshal(last)
	for _, marker := range []string{"rotated-private-gateway-marker", "second-private-path-marker", "second-query-marker"} {
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatal("audit leaked changed gateway secret or URL")
		}
	}
}

func TestAuditLegacyHistoryDeletionAndRollback(t *testing.T) {
	a, _, _ := commercialSetup(t)
	if err := a.Store.Update(func(d *State) error {
		// Imported legacy rows have only the original four fields. Existing rows
		// must not be retrospectively enriched during later unrelated writes.
		d.Audit[0] = Audit{Time: 1, Actor: d.Users[0].ID, Action: "login", Subject: d.Users[0].ID}
		d.Nodes = append(d.Nodes, Node{ID: "audit-deleted-node", Name: "Historical node"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := a.Store.Snapshot()
	if err := a.Store.Update(func(d *State) error {
		d.Nodes = nil
		record(d, d.Users[0].ID, "node_deleted", "audit-deleted-node")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d := a.Store.Snapshot()
	old, _ := json.Marshal(before.Audit[0])
	preserved, _ := json.Marshal(d.Audit[0])
	if !bytes.Equal(old, preserved) {
		t.Fatal("legacy history was rewritten")
	}
	deleted := d.Audit[len(d.Audit)-1]
	if deleted.SubjectName != "Historical node" || findAuditChange(t, deleted, "name").After != "已移除" {
		t.Fatal("deletion lost the old object details")
	}
	count := len(d.Audit)
	if err := a.Store.Update(func(d *State) error {
		d.Announcement = "rejected write"
		record(d, d.Users[0].ID, "settings_saved", "")
		return errors.New("reject transaction")
	}); err == nil {
		t.Fatal("failed mutation succeeded")
	}
	if len(a.Store.Snapshot().Audit) != count {
		t.Fatal("failed write left a success audit row")
	}
}

func TestAuditGatewayEventsUseSystemActor(t *testing.T) {
	a, _, _ := commercialSetup(t)
	now := time.Now().Unix()
	err := a.Store.Update(func(d *State) error {
		u := User{ID: ID(), Role: "user", Email: "purchaser@example.test"}
		d.Users = append(d.Users, u)
		o := Order{ID: ID(), UserID: u.ID, Status: "pending", Plan: Plan{Name: "Gateway purchase", Days: 30, Currency: "cny", PriceCents: 100, Devices: 1}}
		p := Payment{ID: ID(), OrderID: o.ID, Provider: "stripe", Status: "pending", AmountCents: 100, Currency: "cny"}
		d.Orders, d.Payments = append(d.Orders, o), append(d.Payments, p)
		if err := paidEvent(d, p.ID, "provider-receipt", 100, "cny", false, now); err != nil {
			return err
		}
		return refundPayment(d, p.ID, "provider-refund", "channel confirmed", now+1)
	})
	if err != nil {
		t.Fatal(err)
	}
	d := a.Store.Snapshot()
	for _, event := range d.Audit {
		if event.Action == "payment_paid" || event.Action == "payment_refunded" {
			if event.Actor != "system:stripe" || event.ActorName != "系统 · Stripe" {
				t.Fatal("provider action impersonated the buyer")
			}
		}
	}
}

func TestPostgresAuditDetailsPersist(t *testing.T) {
	c := pgConfig(t)
	s, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Update(func(d *State) error {
		d.PaymentConfig = &GatewaySettings{Mode: "test", Epay: GatewayConfig{Secret: "private-postgres-audit-key"}}
		record(d, d.Users[0].ID, "payment_settings_saved", "")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	d := second.Snapshot()
	if len(d.Audit) != 1 || d.Audit[0].Summary != "保存支付渠道配置" || d.Audit[0].ActorName != c.AdminEmail || len(d.Audit[0].Changes) == 0 {
		t.Fatal("PostgreSQL did not persist detailed audit")
	}
	raw, _ := json.Marshal(d.Audit)
	if strings.Contains(string(raw), "private-postgres-audit-key") {
		t.Fatal("PostgreSQL audit leaked a key")
	}
}
