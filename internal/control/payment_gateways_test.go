package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const epayTestKey = "test-merchant-secret"
const bepTestKey = "test-bepusdt-secret"

func TestGatewayProtocolVectorsAndExactMoney(t *testing.T) {
	ep := map[string]string{"pid": "1001", "type": "alipay", "money": "10.01", "out_trade_no": "A1", "empty": "", "sign_type": "MD5"}
	if gatewaySign(ep, epayTestKey, "sign", "sign_type") != "fd0d7aa574ccdc4dbd90044119764ef1" {
		t.Fatal("epay signing vector differs")
	}
	be, err := bepusdtFields(map[string]any{"amount": json.Number("19.99"), "order_id": "A1", "status": 2, "signature": "ignored"})
	if err != nil || gatewaySign(be, bepTestKey, "signature") != "e3354901b99414cd8e9fe1b3d65dc57f" {
		t.Fatal("bepusdt signing vector differs")
	}
	zero, _ := bepusdtFields(map[string]any{"amount": json.Number("0"), "flag": false})
	if zero["amount"] != "0" || zero["flag"] != "false" {
		t.Fatal("zero or false omitted from native signature")
	}
	for _, raw := range []string{"19.999", "1e2", "NaN", "-1", " 19.99", "19.99 ", "1000000000000"} {
		if _, e := moneyCents(raw); e == nil {
			t.Fatal("inexact amount accepted", raw)
		}
	}
	if cents, e := moneyCents("19.99"); e != nil || cents != 1999 {
		t.Fatal("exact cent parsing")
	}
}

func TestGatewaySettingsEncryptionPreservationAndMFA(t *testing.T) {
	a, s, admin := commercialSetup(t)
	input := map[string]any{"mode": "test", "epay": gatewayInput{Enabled: true, URL: "https://pay.example.test/", MerchantID: "1001", Key: epayTestKey, Alipay: true, Wechat: true}, "bepusdt": gatewayInput{Enabled: true, URL: "https://usdt.example.test", Key: bepTestKey, TradeType: "usdt.trc20"}}
	a.Config.Commercial.RequireAdminMFA = true
	request(t, s, "admin/payment-settings", admin, input, 403)
	begin := request(t, s, "security/mfa/begin", admin, map[string]string{"password": a.Config.AdminPassword}, 200)
	code, _ := totp(begin["secret"].(string), time.Now().Unix()/30)
	request(t, s, "security/mfa/confirm", admin, map[string]string{"challenge_id": begin["challenge_id"].(string), "code": code}, 200)
	result := request(t, s, "admin/payment-settings", admin, input, 200)
	raw, _ := json.Marshal(result)
	if bytes.Contains(raw, []byte(epayTestKey)) || bytes.Contains(raw, []byte(bepTestKey)) {
		t.Fatal("key returned to client")
	}
	d := a.Store.Snapshot()
	before := d.PaymentConfig.Epay.Secret
	raw, _ = json.Marshal(d)
	if bytes.Contains(raw, []byte(epayTestKey)) || bytes.Contains(raw, []byte(bepTestKey)) {
		t.Fatal("key persisted in plaintext")
	}
	if v, e := a.unseal("payment-gateway", before); e != nil || v != epayTestKey {
		t.Fatal("gateway key not recoverable")
	}
	in := input["epay"].(gatewayInput)
	in.Key = ""
	input["epay"] = in
	in = input["bepusdt"].(gatewayInput)
	in.Key = ""
	input["bepusdt"] = in
	request(t, s, "admin/payment-settings", admin, input, 200)
	if a.Store.Snapshot().PaymentConfig.Epay.Secret != before {
		t.Fatal("blank key erased existing key")
	}
	in = input["epay"].(gatewayInput)
	in.URL = "http://public.example.test"
	input["epay"] = in
	request(t, s, "admin/payment-settings", admin, input, 400)
	if a.Store.Snapshot().PaymentConfig.Epay.Secret != before {
		t.Fatal("failed save changed configuration")
	}
	// A production origin never accepts plaintext or literal private gateways.
	a.Config.PublicURL = "https://console.example.test/control"
	for _, value := range []string{"http://pay.example.test", "https://127.0.0.1", "https://10.1.2.3", "https://user:secret@pay.example.test", "https://pay.example.test/?key=x", "https://pay.example.test/submit.php"} {
		if _, e := a.gatewayURL(value); e == nil {
			t.Fatal("unsafe gateway accepted", value)
		}
	}
}

func TestGatewayReadinessCannotBeBypassed(t *testing.T) {
	a, s, admin := commercialSetup(t)
	request(t, s, "admin/payment-settings", admin, map[string]any{"mode": "gateways", "epay": gatewayInput{Enabled: true, URL: "https://pay.example.test", MerchantID: "1001", Key: epayTestKey, Alipay: true}}, 200)
	o := request(t, s, "orders", admin, map[string]string{"plan_id": a.Store.Snapshot().Plans[0].ID}, 201)
	request(t, s, "orders/"+o["id"].(string)+"/checkout", admin, map[string]string{"method": "alipay"}, 503)
	if len(a.Store.Snapshot().Payments) != 0 {
		t.Fatal("payment created before readiness")
	}
}

func TestGatewayUSDTNetworkSelectionAndLegacyPayments(t *testing.T) {
	a, s, admin := commercialSetup(t)
	input := map[string]any{"mode": "gateways", "bepusdt": gatewayInput{Enabled: true, URL: "https://usdt.example.test", Key: bepTestKey}}
	request(t, s, "admin/payment-settings", admin, input, 200)
	public := request(t, s, "public/commerce", "", nil, 200)
	methods := public["payment_methods"].([]any)
	if len(methods) != 3 {
		t.Fatal("three USDT networks not offered")
	}
	expected := map[string]string{"usdt_trc20": "usdt.trc20", "usdt_bep20": "usdt.bep20", "usdt_polygon": "usdt.polygon"}
	d := a.Store.Snapshot()
	for _, method := range methods {
		m := method.(map[string]any)
		provider, gateway, err := gatewayForMethod(&d, m["id"].(string))
		if err != nil || provider != "bepusdt" || gateway.TradeType != expected[m["id"].(string)] || m["trade_type"] != gateway.TradeType {
			t.Fatal("selected network does not match gateway snapshot")
		}
	}
	_, pending, err := gatewayForMethod(&d, "usdt_bep20")
	if err != nil {
		t.Fatal(err)
	}
	selected := gatewayInput{Enabled: true, URL: "https://usdt.example.test", TradeTypes: []string{"usdt.polygon"}}
	input["bepusdt"] = selected
	request(t, s, "admin/payment-settings", admin, input, 200)
	d = a.Store.Snapshot()
	for _, method := range []string{"usdt_trc20", "usdt_bep20", "usdt_erc20", "usdt.pol", "usdt.ebp20"} {
		if _, _, err := gatewayForMethod(&d, method); err == nil {
			t.Fatal("disabled or invalid network accepted", method)
		}
	}
	_, legacy, err := gatewayForMethod(&d, "usdt")
	if err != nil || legacy.TradeType != "usdt.polygon" {
		t.Fatal("legacy checkout alias does not use configured default")
	}
	if p := (Payment{Method: "usdt", Gateway: pending}).Public(); p.Gateway != nil || p.TradeType != "usdt.bep20" {
		t.Fatal("old pending payment network changed or private snapshot exposed")
	}
	for _, invalid := range [][]string{{}, {"usdt.erc20"}, {"usdt.pol"}, {"usdt.ebp20"}, {"usdt.bep20", "usdt.polygon", "usdt.trc20", "usdt.trc20"}} {
		selected.TradeTypes = invalid
		input["bepusdt"] = selected
		request(t, s, "admin/payment-settings", admin, input, 400)
	}
	if types := a.Store.Snapshot().PaymentConfig.Bepusdt.TradeTypes; len(types) != 1 || types[0] != "usdt.polygon" {
		t.Fatal("invalid configuration changed saved networks")
	}
}

// Runs against an isolated PostgreSQL schema, real authenticated HTTP endpoints,
// and an independent local gateway. No real financial or SMTP request is sent.
func TestGatewayPostgresHTTPCheckoutCallbacksAndRestart(t *testing.T) {
	c := pgConfig(t)
	c.Registration = true
	c.Commercial = CommercialConfig{Enabled: true, PaymentMode: "gateways", SecurityKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{73}, 32)), RequireAdminMFA: true, BetaInviteOnly: true, LiveApproved: true, Terms: "test terms", Privacy: "test privacy", RefundPolicy: "test policy"}
	c.Mail = MailConfig{Mode: "smtp", Host: "smtp.example.test", Port: 465, From: "service@example.test", AlertsTo: "alerts@example.test"}
	a, e := NewAPI(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Store.Close() })
	s := httptest.NewServer(a)
	t.Cleanup(s.Close)
	login := request(t, s, "login", "", map[string]string{"email": c.AdminEmail, "password": c.AdminPassword}, 200)
	admin := login["token"].(string)
	begin := request(t, s, "security/mfa/begin", admin, map[string]string{"password": c.AdminPassword}, 200)
	code, _ := totp(begin["secret"].(string), time.Now().Unix()/30)
	request(t, s, "security/mfa/confirm", admin, map[string]string{"challenge_id": begin["challenge_id"].(string), "code": code}, 200)
	if e = os.MkdirAll(filepath.Dir(c.DataFile), 0700); e != nil {
		t.Fatal(e)
	}
	backup, _ := json.Marshal(map[string]int64{"time": time.Now().Unix()})
	if e = os.WriteFile(filepath.Join(filepath.Dir(c.DataFile), "backup-status.json"), backup, 0600); e != nil {
		t.Fatal(e)
	}
	var creations atomic.Int32
	var failCreate atomic.Bool
	var gateway *httptest.Server
	gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/order/create-transaction" {
			http.NotFound(w, r)
			return
		}
		creations.Add(1)
		if failCreate.Load() {
			w.WriteHeader(502)
			return
		}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		var body map[string]any
		if decoder.Decode(&body) != nil {
			t.Error("invalid gateway request")
			w.WriteHeader(400)
			return
		}
		values, _ := bepusdtFields(body)
		if !signatureMatches(values["signature"], gatewaySign(values, bepTestKey, "signature")) {
			t.Error("invalid native request signature")
			w.WriteHeader(400)
			return
		}
		paymentID := body["order_id"].(string)
		p := findPaymentPtr(a.Store.Snapshot(), paymentID)
		expected := map[string]string{"usdt": "usdt.trc20", "usdt_trc20": "usdt.trc20", "usdt_bep20": "usdt.bep20", "usdt_polygon": "usdt.polygon"}
		if p == nil || body["amount"].(json.Number).String() != "19.99" || body["fiat"] != "CNY" || body["trade_type"] != expected[p.Method] {
			t.Error("gateway request amount or network differs")
		}
		reply(w, 200, map[string]any{"status_code": 200, "data": map[string]any{"trade_id": "trade-" + paymentID, "order_id": paymentID, "amount": "19.99", "fiat": "CNY", "payment_url": gateway.URL + "/pay/" + paymentID}})
	}))
	t.Cleanup(gateway.Close)
	settings := map[string]any{"mode": "gateways", "epay": gatewayInput{Enabled: true, URL: "https://pay.example.test", MerchantID: "1001", Key: epayTestKey, Alipay: true, Wechat: true}, "bepusdt": gatewayInput{Enabled: true, URL: gateway.URL, Key: bepTestKey, TradeType: "usdt.trc20"}}
	request(t, s, "admin/payment-settings", admin, settings, 200)
	e = a.Store.Update(func(d *State) error {
		d.Plans[0].PriceCents = 1999
		d.Plans[0].Currency = "cny"
		d.Nodes = append(d.Nodes, Node{ID: ID(), Enabled: true, LastSeen: time.Now().Unix(), Capabilities: []string{"strict-billing", "leases-v1"}})
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_, user := betaAccount(t, a, s, admin)
	if !a.readiness()["live_ready"].(bool) {
		t.Fatal("isolated commercial fixture not ready")
	}
	callback := func(endpoint string, body any, status int) string {
		method := "POST"
		var data io.Reader
		target := s.URL + "/api/payment/webhook/" + endpoint
		if fields, ok := body.(map[string]string); ok {
			v := url.Values{}
			for k, value := range fields {
				v.Set(k, value)
			}
			method = "GET"
			target += "?" + v.Encode()
		} else {
			raw, _ := json.Marshal(body)
			data = bytes.NewReader(raw)
		}
		r, _ := http.NewRequest(method, target, data)
		r.Header.Set("Content-Type", "application/json")
		res, e := s.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("%s callback %d want %d", endpoint, res.StatusCode, status)
		}
		return string(raw)
	}
	for _, method := range []string{"alipay", "wxpay", "usdt_trc20", "usdt_bep20", "usdt_polygon", "usdt"} {
		t.Run(method, func(t *testing.T) {
			o := request(t, s, "orders", user, map[string]string{"plan_id": a.Store.Snapshot().Plans[0].ID}, 201)
			orderID := o["id"].(string)
			out := request(t, s, "orders/"+orderID+"/checkout", user, map[string]string{"method": method}, 200)
			d := a.Store.Snapshot()
			var p Payment
			for _, value := range d.Payments {
				if value.OrderID == orderID {
					p = value
				}
			}
			if p.ID == "" || p.Test || p.Gateway == nil {
				t.Fatal("missing real-scope gateway payment")
			}
			if out["checkout_url"] == "" {
				t.Fatal("no checkout link")
			}
			// Repeated clicks reuse the existing checkout, never rebuild a BE order.
			reused := request(t, s, "orders/"+orderID+"/checkout", user, map[string]string{"method": method}, 200)
			var notice any
			endpoint := "epay"
			if p.Provider == "bepusdt" {
				endpoint = "bepusdt"
				if reused["payment"].(map[string]any)["trade_type"] != p.Gateway.TradeType {
					t.Fatal("public payment network absent")
				}
				// A different selection cannot replace an existing invoice's network.
				other := "usdt_polygon"
				if method == other {
					other = "usdt_trc20"
				}
				request(t, s, "orders/"+orderID+"/checkout", user, map[string]string{"method": other}, 409)
				be := map[string]any{"trade_id": p.Reference, "order_id": p.ID, "amount": json.Number("19.99"), "actual_amount": "3.50", "token": "wallet-address-test", "status": 1, "block_transaction_id": ""}
				values, _ := bepusdtFields(be)
				be["signature"] = gatewaySign(values, bepTestKey, "signature")
				if callback(endpoint, be, 200) != "ok" {
					t.Fatal("native acknowledgement differs")
				}
				if findPaymentPtr(a.Store.Snapshot(), p.ID).Status != "pending" {
					t.Fatal("waiting notice granted access")
				}
				be["status"] = 2
				be["block_transaction_id"] = "chain-transaction-" + p.ID
				values, _ = bepusdtFields(be)
				be["signature"] = gatewaySign(values, bepTestKey, "signature")
				notice = be
				be["amount"] = json.Number("1.00")
				values, _ = bepusdtFields(be)
				be["signature"] = gatewaySign(values, bepTestKey, "signature")
				callback(endpoint, be, 400)
				be["amount"] = json.Number("19.99")
				values, _ = bepusdtFields(be)
				be["signature"] = gatewaySign(values, bepTestKey, "signature")
			} else {
				u, e := url.Parse(out["checkout_url"].(string))
				if e != nil {
					t.Fatal(e)
				}
				values := map[string]string{}
				for k, v := range u.Query() {
					values[k] = v[0]
				}
				if values["type"] != method || values["money"] != "19.99" || !signatureMatches(values["sign"], gatewaySign(values, epayTestKey, "sign", "sign_type")) {
					t.Fatal("invalid epay checkout")
				}
				ep := map[string]string{"pid": "1001", "type": method, "out_trade_no": p.ID, "trade_no": "receipt-" + p.ID, "money": "19.99", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
				ep["sign"] = gatewaySign(ep, epayTestKey, "sign", "sign_type")
				notice = ep
				ep["pid"] = "another-merchant"
				ep["sign"] = gatewaySign(ep, epayTestKey, "sign", "sign_type")
				callback(endpoint, ep, 400)
				ep["pid"] = "1001"
				ep["sign"] = gatewaySign(ep, epayTestKey, "sign", "sign_type")
				// Browser return does not confirm payment.
				request(t, s, "orders", user, nil, 200)
			}
			before := len(a.Store.Snapshot().Entitlements)
			if method == "wxpay" {
				if e := a.Store.Update(func(d *State) error { order := findOrder(d, orderID); order.Status = "expired"; return nil }); e != nil {
					t.Fatal(e)
				}
				rotated := settings["epay"].(gatewayInput)
				rotated.Key = "rotated-epay-test-key"
				settings["epay"] = rotated
				request(t, s, "admin/payment-settings", admin, settings, 200)
			}
			callback(endpoint, notice, 200)
			callback(endpoint, notice, 200)
			d = a.Store.Snapshot()
			if len(d.Entitlements) != before+1 || findPayment(&d, p.ID).Status != "paid" {
				t.Fatal("duplicate notice granted twice or payment absent")
			}
			request(t, s, "admin/payments/"+p.ID+"/refund", admin, map[string]string{"reason": "external test refund"}, 409)
			refund := map[string]string{"reason": "verified external refund", "reference": "refund-receipt-" + p.ID}
			request(t, s, "admin/payments/"+p.ID+"/manual-refund", admin, refund, 200)
			request(t, s, "admin/payments/"+p.ID+"/manual-refund", admin, refund, 200)
			callback(endpoint, notice, 200)
			d = a.Store.Snapshot()
			if findPayment(&d, p.ID).Status != "refunded" || len(d.Refunds) == 0 {
				t.Fatal("refund undone by late payment notification")
			}
			public := request(t, s, "billing", user, nil, 200)
			raw, _ := json.Marshal(public)
			if bytes.Contains(raw, []byte("\"gateway\"")) || bytes.Contains(raw, []byte(p.Gateway.Secret)) {
				t.Fatal("private payment snapshot exposed")
			}
		})
	}
	if creations.Load() != 4 {
		t.Fatal("native checkout recreated on retry")
	}
	// Ambiguous downlink failure persists across requests and does not recreate.
	failCreate.Store(true)
	o := request(t, s, "orders", user, map[string]string{"plan_id": a.Store.Snapshot().Plans[0].ID}, 201)
	request(t, s, "orders/"+o["id"].(string)+"/checkout", user, map[string]string{"method": "usdt"}, 502)
	request(t, s, "orders/"+o["id"].(string)+"/checkout", user, map[string]string{"method": "usdt"}, 409)
	if creations.Load() != 5 {
		t.Fatal("ambiguous order recreated")
	}
	restarted, e := NewAPI(c)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Store.Close()
	d := restarted.Store.Snapshot()
	if d.PaymentConfig == nil || d.PaymentConfig.Mode != "gateways" {
		t.Fatal("gateway settings lost after restart")
	}
	if len(d.PaymentConfig.Bepusdt.TradeTypes) != 3 {
		t.Fatal("network settings lost after restart")
	}
	if key, e := restarted.unseal("payment-gateway", d.PaymentConfig.Bepusdt.Secret); e != nil || key != bepTestKey {
		t.Fatal("encrypted key lost after restart")
	}
	if !strings.Contains(string(mustJSON(d)), "checkout-unknown:") {
		t.Fatal("uncertain creation not recorded")
	}
	// A valid late receipt settles an ambiguous checkout and resolves its alert.
	unknown := d.Payments[len(d.Payments)-1]
	be := map[string]any{"trade_id": "late-" + unknown.ID, "order_id": unknown.ID, "amount": json.Number("19.99"), "actual_amount": "3.50", "token": "test-wallet", "status": 2, "block_transaction_id": "late-chain-" + unknown.ID}
	values, _ := bepusdtFields(be)
	be["signature"] = gatewaySign(values, bepTestKey, "signature")
	callback("bepusdt", be, 200)
	d = a.Store.Snapshot()
	if p := findPayment(&d, unknown.ID); p.Status != "paid" || p.CheckoutState != "confirmed" {
		t.Fatal("late receipt did not settle checkout")
	}
	for _, incident := range d.Incidents {
		if incident.Key == "checkout-unknown:"+unknown.ID && incident.ResolvedAt == 0 {
			t.Fatal("checkout alert did not resolve")
		}
	}
}

func findPaymentPtr(d State, id string) *Payment { return findPayment(&d, id) }
func mustJSON(value any) []byte                  { raw, _ := json.Marshal(value); return raw }
