package control

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func commercialSetup(t *testing.T) (*API, *httptest.Server, string) {
	a, s, admin := setup(t)
	a.Config.Commercial = CommercialConfig{Enabled: true, PaymentMode: "test", SecurityKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)), BetaInviteOnly: true}
	a.Config.Mail.Mode = "test"
	backup, err := json.Marshal(map[string]int64{"time": time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(a.Config.DataFile), "backup-status.json"), backup, 0600); err != nil {
		t.Fatal(err)
	}
	return a, s, admin
}
func betaAccount(t *testing.T, a *API, s *httptest.Server, admin string) (string, string) {
	v := request(t, s, "admin/beta/invites", admin, map[string]int{"days": 1}, 200)
	reg := request(t, s, "register", "", map[string]string{"email": "beta@example.test", "password": "beta-test-password-strong", "invite": v["invite"].(string)}, 200)
	token := reg["token"].(string)
	uid := reg["user"].(map[string]any)["id"].(string)
	d := a.Store.Snapshot()
	body, e := a.unseal("mail", d.Outbox[len(d.Outbox)-1].Body)
	if e != nil {
		t.Fatal(e)
	}
	link := strings.Split(strings.Split(body, "#verify=")[1], "\n")[0]
	request(t, s, "security/verify", "", map[string]string{"token": link}, 200)
	return uid, token
}
func purchased(t *testing.T, a *API, s *httptest.Server, token string) string {
	d := a.Store.Snapshot()
	o := request(t, s, "orders", token, map[string]string{"plan_id": d.Plans[0].ID}, 201)
	p := request(t, s, "orders/"+o["id"].(string)+"/checkout", token, map[string]any{}, 200)
	payment := p["payment"].(map[string]any)["id"].(string)
	request(t, s, "payments/"+payment+"/test-confirm", token, map[string]any{}, 200)
	return payment
}
func TestCommercialDuplicatePaymentRefundAndRenewal(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, token := betaAccount(t, a, s, admin)
	pid := purchased(t, a, s, token)
	request(t, s, "payments/"+pid+"/test-confirm", token, map[string]any{}, 200)
	d := a.Store.Snapshot()
	if len(d.Entitlements) != 1 || len(d.PaymentEvents) != 1 {
		t.Fatal("duplicate charge granted twice")
	}
	if findUser(&d, uid).ExpiresAt != 0 {
		t.Fatal("test payment leaked to production access")
	}
	pid2 := purchased(t, a, s, token)
	d = a.Store.Snapshot()
	if d.Entitlements[1].StartsAt != d.Entitlements[0].EndsAt {
		t.Fatal("renewal does not queue")
	}
	request(t, s, "admin/payments/"+pid+"/refund", admin, map[string]string{"reason": "test full refund"}, 200)
	request(t, s, "admin/payments/"+pid+"/refund", admin, map[string]string{"reason": "duplicate refund"}, 200)
	d = a.Store.Snapshot()
	if len(d.Refunds) != 1 || d.Entitlements[0].RevokedAt == 0 || d.Entitlements[1].StartsAt > time.Now().Unix()+1 {
		t.Fatal("refund duplicated or queued renewal left a gap")
	}
	request(t, s, "payments/"+pid+"/test-confirm", token, map[string]any{}, 200)
	d = a.Store.Snapshot()
	if d.Payments[0].Status != "refunded" || len(d.Entitlements) != 2 {
		t.Fatal("late duplicate payment resurrected refunded access")
	}
	_ = pid2
	request(t, s, "admin/orders/"+d.Orders[1].ID+"/fulfil", admin, map[string]any{}, 409)
}
func TestCommercialVerificationAndResetSingleUse(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, token := betaAccount(t, a, s, admin)
	request(t, s, "security/forgot", "", map[string]string{"email": "beta@example.test"}, 202)
	d := a.Store.Snapshot()
	body, _ := a.unseal("mail", d.Outbox[len(d.Outbox)-1].Body)
	link := strings.Split(strings.Split(body, "#reset=")[1], "\n")[0]
	old := findUser(&d, uid).TunnelToken
	request(t, s, "security/reset", "", map[string]string{"token": link, "password": "new-test-password-at-least-12"}, 200)
	request(t, s, "me", token, nil, 401)
	request(t, s, "security/reset", "", map[string]string{"token": link, "password": "another-test-password-new"}, 400)
	d = a.Store.Snapshot()
	u := findUser(&d, uid)
	if u.TunnelToken == old || !passwordValid(u.PasswordHash, "new-test-password-at-least-12") {
		t.Fatal("reset failed to rotate credentials")
	}
	raw, _ := json.Marshal(d)
	if bytes.Contains(raw, []byte(link)) {
		t.Fatal("reset link saved in plaintext")
	}
}
func TestCommercialMFAReplayRecoveryAndRoleGate(t *testing.T) {
	a, s, admin := commercialSetup(t)
	a.Config.Commercial.RequireAdminMFA = true
	denied := request(t, s, "admin/summary", admin, nil, 403)
	if denied["mfa_required"] != true || denied["mfa_enabled"] != false || denied["security_url"] != a.Config.PublicURL+"/#security" {
		t.Fatal("MFA gate does not provide a setup path")
	}
	status := request(t, s, "security/status", admin, nil, 200)
	if status["mfa_required"] != true || status["mfa_enforced"] != true {
		t.Fatal("security setup is inaccessible or reports the wrong policy")
	}
	request(t, s, "admin/tickets", admin, nil, 403)
	begin := request(t, s, "security/mfa/begin", admin, map[string]string{"password": a.Config.AdminPassword}, 200)
	code, _ := totp(begin["secret"].(string), time.Now().Unix()/30)
	enroll := request(t, s, "security/mfa/confirm", admin, map[string]string{"challenge_id": begin["challenge_id"].(string), "code": code}, 200)
	request(t, s, "admin/summary", admin, nil, 200)
	status = request(t, s, "security/status", admin, nil, 200)
	if status["mfa_required"] != false || status["mfa_session"] != true || status["mfa_enabled"] != true {
		t.Fatal("confirmed MFA session remains locked")
	}
	d := a.Store.Snapshot()
	u := d.Users[0]
	if a.verifyMFA(&u, code, time.Now().Unix()) {
		t.Fatal("TOTP replay accepted")
	}
	recovery := enroll["recovery_codes"].([]any)[0].(string)
	if !a.verifyMFA(&u, recovery, time.Now().Unix()) || a.verifyMFA(&u, recovery, time.Now().Unix()) {
		t.Fatal("recovery code replay accepted")
	}
	if _, e := a.unseal("mail", d.Users[0].MFASecret); e == nil {
		t.Fatal("ciphertext can cross purposes")
	}
	if adminPathAllowed("support", "payments/x/refund", "POST") || adminPathAllowed("finance", "nodes", "POST") {
		t.Fatal("role crosses permission boundary")
	}
}
func ledger(t *testing.T) (State, Node, User, int64) {
	now := time.Now().Unix()
	u := User{ID: ID(), Role: "user", Beta: true, EmailVerifiedAt: now, Devices: 1}
	n := Node{ID: ID(), Enabled: true, TestOnly: true, Capabilities: []string{"strict-billing", "leases-v1"}}
	d := State{Schema: 1, Users: []User{u}, Entitlements: []Entitlement{{ID: ID(), UserID: u.ID, StartsAt: now - 1, EndsAt: now + 86400, Bytes: 1024, Devices: 1, Test: true, Kind: "subscription"}}}
	normalizeLists(&d)
	return d, n, u, now
}
func TestCommercialMultiNodeQuotaExhaustionAndDevices(t *testing.T) {
	d, n, u, now := ledger(t)
	device := ID()
	one, e := leaseUpdate(&d, n, LeaseRequest{UserID: u.ID, DeviceID: device}, now)
	if e != nil || one.Budget != 1024 {
		t.Fatal("lease allocation", e)
	}
	second := n
	second.ID = ID()
	two, e := leaseUpdate(&d, second, LeaseRequest{UserID: u.ID, DeviceID: device}, now)
	if e != nil || two.Budget != 0 {
		t.Fatal("quota multiplied on second node", e)
	}
	if _, e = leaseUpdate(&d, second, LeaseRequest{UserID: u.ID, DeviceID: ID()}, now); e == nil {
		t.Fatal("global device limit bypassed")
	}
	if _, e = leaseUpdate(&d, n, LeaseRequest{ID: one.ID, UserID: u.ID, DeviceID: device, Used: 1025}, now); e == nil {
		t.Fatal("overspend accepted")
	}
	one, e = leaseUpdate(&d, n, LeaseRequest{ID: one.ID, UserID: u.ID, DeviceID: device, Used: 1024, Close: true}, now)
	if e != nil {
		t.Fatal(e)
	}
	v := accountForNode(&d, u, n, now)
	if v.Active(now) || d.Entitlements[0].Used != 1024 {
		t.Fatal("exhausted quota still authorizes")
	}
	if _, e = leaseUpdate(&d, n, LeaseRequest{UserID: u.ID, DeviceID: device}, now); e == nil {
		t.Fatal("exhausted account got a new lease")
	}
}
func TestCommercialReservationsDoNotReleaseOnNodeLoss(t *testing.T) {
	d, n, u, now := ledger(t)
	one, e := leaseUpdate(&d, n, LeaseRequest{UserID: u.ID, DeviceID: ID()}, now)
	if e != nil {
		t.Fatal(e)
	}
	expireOrders(&d, now+120)
	if reserved(&d, d.Entitlements[0].ID, "") != one.Budget {
		t.Fatal("node loss reissued possibly consumed bytes")
	}
	other := n
	other.ID = ID()
	l, e := leaseUpdate(&d, other, LeaseRequest{UserID: u.ID, DeviceID: ID()}, now+120)
	if e != nil || l.Budget != 0 {
		t.Fatal("unreported quota reissued after timeout", e)
	}
}
func TestCommercialCycleBoundaryAddonAndExpiredOrder(t *testing.T) {
	d, n, u, now := ledger(t)
	o := Order{ID: ID(), UserID: u.ID, Plan: Plan{Kind: "traffic", TrafficBytes: 512, Days: 1, Devices: 1, Currency: "cny"}, Test: true}
	if e := grantPurchase(&d, &o, now); e != nil {
		t.Fatal(e)
	}
	v := accountForNode(&d, u, n, now)
	if v.Limit != 1536 {
		t.Fatal("addon missing")
	}
	v = accountForNode(&d, u, n, now+86400)
	if v.Active(now + 86400) {
		t.Fatal("expired quota carried forward")
	}
	d.Orders = []Order{{ID: ID(), UserID: u.ID, Status: "pending", Plan: Plan{PriceCents: 100, Currency: "cny", Days: 30, Devices: 1}, ExpiresAt: now - 1, Test: true}}
	p := Payment{ID: ID(), OrderID: d.Orders[0].ID, Provider: "test", AmountCents: 100, Currency: "cny", Status: "pending", Test: true}
	d.Payments = []Payment{p}
	if paidEvent(&d, p.ID, ID(), 100, "cny", true, now) == nil {
		t.Fatal("expired test order paid")
	}
}
func stripeSignature(raw []byte, secret string, now int64) string {
	stamp := strconv.FormatInt(now, 10)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stamp + "."))
	h.Write(raw)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(h.Sum(nil))
}
func TestCommercialStripeWebhookSignatureAndIdempotency(t *testing.T) {
	a, s, _ := commercialSetup(t)
	a.Config.Commercial.WebhookSecret = "webhook-test-secret"
	a.Config.Commercial.LiveApproved = true
	now := time.Now().Unix()
	d, n, u, _ := ledger(t)
	n.TestOnly = false
	u.Beta = false
	d.Users = append(d.Users, a.Store.Snapshot().Users[0])
	d.Entitlements = nil
	o := Order{ID: ID(), UserID: u.ID, Status: "pending", Plan: Plan{Currency: "cny", PriceCents: 1234, Days: 30, Devices: 1, TrafficBytes: 2048}, ExpiresAt: now + 300}
	p := Payment{ID: ID(), OrderID: o.ID, Provider: "stripe", Reference: "cs_test_case", AmountCents: 1234, Currency: "cny", Status: "pending"}
	d.Orders = []Order{o}
	d.Payments = []Payment{p}
	if e := a.Store.Update(func(v *State) error { *v = d; return nil }); e != nil {
		t.Fatal(e)
	}
	send := func(eventID string, amount int64, signature bool, status int) {
		raw, _ := json.Marshal(map[string]any{"id": eventID, "type": "checkout.session.completed", "livemode": true, "data": map[string]any{"object": map[string]any{"id": p.Reference, "payment_status": "paid", "payment_intent": "pi_test_case", "amount_total": amount, "currency": "cny", "client_reference_id": o.ID}}})
		r, _ := http.NewRequest("POST", s.URL+"/api/payment/webhook/stripe", bytes.NewReader(raw))
		if signature {
			r.Header.Set("Stripe-Signature", stripeSignature(raw, a.Config.Commercial.WebhookSecret, now))
		}
		res, e := s.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("webhook %d want %d", res.StatusCode, status)
		}
	}
	send("evt_a", 1234, false, 400)
	send("evt_a", 1, true, 409)
	if len(a.Store.Snapshot().Entitlements) > 0 {
		t.Fatal("invalid payment changed ledger")
	}
	send("evt_a", 1234, true, 200)
	send("evt_a", 1234, true, 200)
	send("evt_b", 1234, true, 200)
	if len(a.Store.Snapshot().Entitlements) != 1 {
		t.Fatal("valid duplicate notifications granted twice")
	}
	raw := []byte("{}")
	if validStripeSignature(raw, stripeSignature(raw, a.Config.Commercial.WebhookSecret, now-301), a.Config.Commercial.WebhookSecret, now) {
		t.Fatal("stale signature accepted")
	}
}
func TestCommercialTicketsIsolationAndOfflineRecovery(t *testing.T) {
	a, s, admin := commercialSetup(t)
	_, token := betaAccount(t, a, s, admin)
	created := request(t, s, "tickets", token, map[string]string{"subject": "下载中断", "body": "验收工单"}, 200)
	tid := created["ticket_id"].(string)
	request(t, s, "admin/tickets/"+tid+"/reply", admin, map[string]string{"body": "已记录并检查"}, 200)
	request(t, s, "tickets/"+tid+"/close", token, map[string]any{}, 200)
	a.Store.Update(func(d *State) error {
		d.Nodes = append(d.Nodes, Node{ID: ID(), Enabled: true, LastSeen: time.Now().Unix() - 100})
		return nil
	})
	a.maintain(time.Now().Unix())
	if len(a.Store.Snapshot().Incidents) != 1 {
		t.Fatal("offline not recorded")
	}
	a.Store.Update(func(d *State) error { d.Nodes[0].LastSeen = time.Now().Unix(); return nil })
	a.maintain(time.Now().Unix())
	if a.Store.Snapshot().Incidents[0].ResolvedAt == 0 {
		t.Fatal("recovery not recorded")
	}
	reopened, e := OpenStore(a.Config)
	if e != nil || reopened.Snapshot().Tickets[0].Status != "closed" {
		t.Fatal("restart lost ticket", e)
	}
}
func TestCommercialExportAndFailureRollback(t *testing.T) {
	a, _, _ := commercialSetup(t)
	file := filepath.Join(testTempDir(t), "backup.json")
	if e := a.Store.Export(file); e != nil {
		t.Fatal(e)
	}
	before := a.Store.Snapshot()
	if e := a.Store.Update(func(d *State) error { d.Announcement = "do not commit"; return errors.New("transaction rejected") }); e == nil {
		t.Fatal("rejected update accepted")
	}
	if a.Store.Snapshot().Announcement != before.Announcement {
		t.Fatal("transaction rollback failed")
	}
}
