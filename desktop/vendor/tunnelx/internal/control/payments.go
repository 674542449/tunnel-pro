package control

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func adminPathAllowed(role, path, method string) bool {
	return role == "admin"
}
func (a *API) readiness() map[string]any {
	d := a.Store.Snapshot()
	admins := true
	commercialNodes := 0
	for _, u := range d.Users {
		if u.Role == "admin" && !u.Disabled && u.MFASecret == "" {
			admins = false
		}
	}
	for _, n := range d.Nodes {
		if n.Enabled && !n.TestOnly && !n.Pending() && n.LastSeen > time.Now().Unix()-90 && hasCapability(n, "strict-billing") && hasCapability(n, "leases-v1") {
			commercialNodes++
		}
	}
	mode := a.paymentMode(&d)
	configured := mode == "stripe" && a.Config.Commercial.PaymentSecret != "" && a.Config.Commercial.WebhookSecret != "" || mode == "gateways" && len(a.paymentMethods(&d)) > 0
	checks := []map[string]any{{"key": "database", "label": "事务数据库", "ok": a.Store.Backend() == "postgresql" && a.Store.Healthy()}, {"key": "mail", "label": "实际邮件发送配置", "ok": a.Config.Mail.Mode == "smtp"}, {"key": "mfa", "label": "管理员二次验证", "ok": admins && a.Config.Commercial.RequireAdminMFA}, {"key": "payments", "label": "正式支付商户配置", "ok": configured}, {"key": "policies", "label": "服务、隐私及退款政策", "ok": a.Config.Commercial.Terms != "" && a.Config.Commercial.Privacy != "" && a.Config.Commercial.RefundPolicy != ""}, {"key": "nodes", "label": "商业计费节点", "ok": commercialNodes > 0}, {"key": "approval", "label": "确认经营资质并批准正式开售", "ok": a.Config.Commercial.LiveApproved}}
	ready := a.Config.Commercial.Enabled
	var backup struct {
		Time     int64 `json:"time"`
		FailedAt int64 `json:"failed_at"`
	}
	if b, e := os.ReadFile(filepath.Join(filepath.Dir(a.Config.DataFile), "backup-status.json")); e == nil {
		json.Unmarshal(b, &backup)
	}
	checks = append(checks, map[string]any{"key": "backups", "label": "最近两小时内的加密备份", "ok": backup.FailedAt == 0 && backup.Time > time.Now().Unix()-7200 && backup.Time <= time.Now().Unix()}, map[string]any{"key": "alerts", "label": "实际运维告警投递配置", "ok": a.Config.Mail.Mode == "smtp" && a.Config.Mail.AlertsTo != ""})
	for _, v := range checks {
		ready = ready && v["ok"].(bool)
	}
	return map[string]any{"live_ready": ready, "payment_mode": mode, "storage": a.Store.Backend(), "checks": checks}
}
func hasCapability(n Node, value string) bool {
	for _, v := range n.Capabilities {
		if v == value {
			return true
		}
	}
	return false
}
func (a *API) publicCommerce(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "GET" && r.URL.Path == "/api/public/commerce" {
		d := a.Store.Snapshot()
		plans := []Plan{}
		for _, p := range d.Plans {
			if p.Enabled {
				plans = append(plans, p)
			}
		}
		reply(w, 200, map[string]any{"enabled": a.Config.Commercial.Enabled, "payment_mode": a.paymentMode(&d), "payment_methods": a.paymentMethods(&d), "test_mode": a.paymentMode(&d) == "test", "invite_only": a.Config.Commercial.BetaInviteOnly, "plans": plans, "release": d.Release, "terms": a.Config.Commercial.Terms, "privacy": a.Config.Commercial.Privacy, "refund_policy": a.Config.Commercial.RefundPolicy})
		return true
	}
	if r.Method == "POST" && r.URL.Path == "/api/payment/webhook/stripe" {
		a.stripeWebhook(w, r)
		return true
	}
	if r.URL.Path == "/api/payment/webhook/epay" {
		a.epayWebhook(w, r)
		return true
	}
	if r.URL.Path == "/api/payment/webhook/bepusdt" {
		a.bepusdtWebhook(w, r)
		return true
	}
	return false
}
func (a *API) stripe(ctx context.Context, path string, fields url.Values, idempotency string) (map[string]any, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.stripe.com/v1/"+path, strings.NewReader(fields.Encode()))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+a.Config.Commercial.PaymentSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", idempotency)
	client := a.PaymentHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, e := client.Do(req)
	if e != nil {
		return nil, errors.New("payment gateway unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, errors.New("payment gateway rejected request")
	}
	var value map[string]any
	if e = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&value); e != nil {
		return nil, errors.New("invalid payment gateway response")
	}
	return value, nil
}
func (a *API) commerce(w http.ResponseWriter, r *http.Request, u *User, s *Session) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if path == "billing" && r.Method == "GET" {
		d := a.Store.Snapshot()
		reply(w, 200, billingSummary(&d, *u))
		return true
	}
	parts := strings.Split(path, "/")
	checkout := len(parts) == 3 && parts[0] == "orders" && parts[2] == "checkout"
	testConfirm := len(parts) == 3 && parts[0] == "payments" && parts[2] == "test-confirm"
	if !checkout && !testConfirm {
		return false
	}
	if r.Method != "POST" {
		fail(w, 404, "接口不存在")
		return true
	}
	var body struct {
		Method string `json:"method"`
	}
	if !decode(w, r, &body) {
		return true
	}
	if !a.Config.Commercial.Enabled {
		fail(w, 503, "购买功能尚未启用")
		return true
	}
	if u.NeedsEmailVerification && u.EmailVerifiedAt == 0 {
		fail(w, 403, "请先验证邮箱")
		return true
	}
	if testConfirm {
		snapshot := a.Store.Snapshot()
		if a.paymentMode(&snapshot) != "test" || !u.Beta && u.Role != "admin" {
			fail(w, 403, "测试支付仅供获邀账号使用")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			p := findPayment(d, parts[1])
			if p == nil || !p.Test {
				return errors.New("invalid test payment")
			}
			o := findOrder(d, p.OrderID)
			if o == nil || o.UserID != u.ID {
				return errors.New("payment not owned")
			}
			return paidEvent(d, p.ID, "test-paid-"+p.ID, p.AmountCents, p.Currency, true, time.Now().Unix())
		})
		if e != nil {
			failCommit(w, e, 409, "测试付款未完成，请检查订单状态")
			return true
		}
		reply(w, 200, map[string]bool{"test": true, "paid": true, "real_charge": false})
		return true
	}
	snapshot := a.Store.Snapshot()
	mode := a.paymentMode(&snapshot)
	if mode != "test" && mode != "stripe" && mode != "gateways" {
		fail(w, 503, "支付渠道尚未配置")
		return true
	}
	if mode != "test" && !a.readiness()["live_ready"].(bool) {
		fail(w, 503, "正式收款条件尚未满足，请联系管理员")
		return true
	}
	var payment Payment
	var order Order
	e := a.commit(u, s, func(d *State) error {
		if a.paymentMode(d) != mode {
			return errors.New("payment mode changed")
		}
		expireOrders(d, time.Now().Unix())
		o := findOrder(d, parts[1])
		if o == nil || o.UserID != u.ID || o.Status != "pending" {
			return errors.New("order not payable")
		}
		if o.Plan.Currency == "" {
			if e := financialPlan(&o.Plan); e != nil {
				return e
			}
		}
		if o.Test != (mode == "test") {
			return errors.New("order scope differs from payment mode")
		}
		if o.Test && !u.Beta && u.Role != "admin" {
			return errors.New("beta required")
		}
		if e := validatePurchase(d, *o, time.Now().Unix()); e != nil {
			return e
		}
		for _, p := range d.Payments {
			if p.OrderID == o.ID {
				if body.Method != "" && p.Method != "" && p.Method != body.Method {
					return errors.New("此订单已经选定支付方式，请继续原收银台或创建新订单")
				}
				payment = p
				order = *o
				return nil
			}
		}
		payment = Payment{ID: ID(), OrderID: o.ID, Provider: mode, AmountCents: o.Plan.PriceCents, Currency: o.Plan.Currency, Status: "pending", CreatedAt: time.Now().Unix(), Test: o.Test}
		if mode == "gateways" {
			provider, gateway, e := gatewayForMethod(d, body.Method)
			if e != nil {
				return e
			}
			if payment.AmountCents <= 0 {
				return errors.New("正式支付金额需要大于零")
			}
			if provider == "epay" && payment.Currency != "cny" {
				return errors.New("支付宝与微信支付只支持人民币套餐")
			}
			payment.Provider = provider
			payment.Method = body.Method
			payment.Gateway = gateway
		}
		if payment.Test {
			payment.Reference = "test-" + payment.ID
		}
		d.Payments = append(d.Payments, payment)
		record(d, u.ID, "payment_checkout_created", payment.ID)
		order = *o
		return nil
	})
	if e != nil {
		failCommit(w, e, 409, "订单已失效、已处理或保存失败")
		return true
	}
	if payment.Test {
		reply(w, 200, map[string]any{"payment": payment.Public(), "test": true, "real_charge": false})
		return true
	}
	if payment.CheckoutURL != "" {
		reply(w, 200, map[string]any{"payment": payment.Public(), "provider": payment.Provider, "checkout_url": payment.CheckoutURL})
		return true
	}
	if payment.Provider == "epay" || payment.Provider == "bepusdt" {
		a.createGatewayCheckout(w, r, u, s, payment, order)
		return true
	}
	fields := url.Values{"mode": {"payment"}, "client_reference_id": {order.ID}, "metadata[order_id]": {order.ID}, "metadata[payment_id]": {payment.ID}, "line_items[0][quantity]": {"1"}, "line_items[0][price_data][currency]": {payment.Currency}, "line_items[0][price_data][unit_amount]": {strconv.FormatInt(payment.AmountCents, 10)}, "line_items[0][price_data][product_data][name]": {order.Plan.Name}, "success_url": {a.Config.PublicURL + "/#orders"}, "cancel_url": {a.Config.PublicURL + "/#orders"}}
	response, e := a.stripe(r.Context(), "checkout/sessions", fields, "checkout-"+order.ID)
	if e != nil {
		fail(w, 502, "支付服务暂时不可用，订单保留，可稍后重试")
		return true
	}
	ref, ok := response["id"].(string)
	target, _ := response["url"].(string)
	parsed, e := url.Parse(target)
	if !ok || ref == "" || e != nil || parsed.Scheme != "https" || parsed.Hostname() != "checkout.stripe.com" || parsed.User != nil {
		fail(w, 502, "支付渠道响应无效")
		return true
	}
	e = a.commit(u, s, func(d *State) error {
		p := findPayment(d, payment.ID)
		if p == nil {
			return errors.New("missing payment")
		}
		if p.Reference != "" && p.Reference != ref {
			return errors.New("payment reference changed")
		}
		p.Reference = ref
		p.CheckoutURL = target
		return nil
	})
	if e != nil {
		failCommit(w, e, 503, "无法保存收款页面，请重新打开订单核实")
		return true
	}
	reply(w, 200, map[string]any{"payment_id": payment.ID, "checkout_url": target})
	return true
}
func (a *API) adminCommerce(w http.ResponseWriter, r *http.Request, u *User, s *Session, path string, parts []string) bool {
	if a.adminGatewaySettings(w, r, u, s, path, parts) {
		return true
	}
	if r.Method == "GET" {
		d := a.Store.Snapshot()
		switch path {
		case "payments":
			public := make([]Payment, 0, len(d.Payments))
			for _, p := range d.Payments {
				public = append(public, p.Public())
			}
			reply(w, 200, public)
			return true
		case "refunds":
			reply(w, 200, d.Refunds)
			return true
		case "leases":
			reply(w, 200, d.Leases)
			return true
		case "readiness":
			reply(w, 200, a.readiness())
			return true
		case "mail/test":
			if a.Config.Mail.Mode != "test" {
				fail(w, 403, "实际邮件不在后台显示正文")
				return true
			}
			out := []map[string]any{}
			for _, m := range d.Outbox {
				body, e := a.unseal("mail", m.Body)
				if e != nil {
					continue
				}
				out = append(out, map[string]any{"id": m.ID, "to": m.To, "subject": m.Subject, "body": body, "created_at": m.CreatedAt})
			}
			reply(w, 200, out)
			return true
		}
	}
	if r.Method != "POST" {
		return false
	}
	if len(parts) == 3 && parts[0] == "leases" && parts[2] == "reconcile" {
		var b struct {
			Used   int64  `json:"used"`
			Reason string `json:"reason"`
		}
		if !decode(w, r, &b) {
			return true
		}
		if len(strings.TrimSpace(b.Reason)) < 4 || len(b.Reason) > 1000 {
			fail(w, 400, "请填写核实计数的依据")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			for i := range d.Leases {
				l := &d.Leases[i]
				if l.ID != parts[1] {
					continue
				}
				n := findNode(d, l.NodeID)
				if l.Closed {
					return nil
				}
				if l.ExpiresAt > time.Now().Unix() || !l.Uncertain && n != nil && n.Enabled && n.LastSeen > time.Now().Unix()-90 {
					return errors.New("lease still live")
				}
				if e := reconcileLease(d, l, b.Used); e != nil {
					return e
				}
				l.Closed = true
				l.Budget = l.Used
				for i := range l.Allocations {
					l.Allocations[i].Budget = l.Allocations[i].Used
				}
				record(d, u.ID, "lease_reconciled:"+b.Reason, l.ID)
				incident(d, "lease-reconcile:"+l.ID, l.NodeID, "", false, time.Now().Unix())
				return nil
			}
			return errors.New("missing lease")
		})
		if e != nil {
			failCommit(w, e, 409, "不能对账：租约仍有效、计数超出预留范围或存储不可用")
			return true
		}
		reply(w, 200, map[string]bool{"ok": true})
		return true
	}
	if path == "beta/invites" {
		var b struct {
			Days int `json:"days"`
		}
		if !decode(w, r, &b) {
			return true
		}
		if b.Days < 1 || b.Days > 30 {
			fail(w, 400, "邀请有效期需为 1 至 30 天")
			return true
		}
		token := Token()
		invite := BetaInvite{ID: ID(), TokenHash: Hash(token), ExpiresAt: time.Now().Add(time.Duration(b.Days) * 24 * time.Hour).Unix()}
		e := a.commit(u, s, func(d *State) error {
			d.Invites = append(d.Invites, invite)
			record(d, u.ID, "beta_invite_created", invite.ID)
			return nil
		})
		if e != nil {
			failCommit(w, e, 503, "无法生成邀请")
			return true
		}
		reply(w, 200, map[string]any{"invite": token, "url": a.Config.PublicURL + "/#invite=" + token, "expires_at": invite.ExpiresAt})
		return true
	}
	if len(parts) == 3 && parts[0] == "users" && parts[2] == "role" {
		var b struct {
			Role string `json:"role"`
		}
		if !decode(w, r, &b) {
			return true
		}
		if b.Role != "user" && b.Role != "admin" {
			fail(w, 400, "分组只能为管理员或用户")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			v := findUser(d, parts[1])
			if v == nil || v.ID == u.ID {
				return errors.New("cannot change own role")
			}
			if v.Role == "admin" && b.Role != "admin" {
				count := 0
				for _, user := range d.Users {
					if user.Role == "admin" && !user.Disabled {
						count++
					}
				}
				if count <= 1 {
					return errors.New("last admin")
				}
			}
			v.Role = b.Role
			revokeSessions(d, v.ID, "")
			record(d, u.ID, "role_changed", v.ID)
			return nil
		})
		if e != nil {
			failCommit(w, e, 409, "无法修改角色，不能修改自己的角色或移除最后管理员")
			return true
		}
		reply(w, 200, map[string]bool{"ok": true})
		return true
	}
	if len(parts) == 3 && parts[0] == "payments" && parts[2] == "refund" {
		var b struct {
			Reason string `json:"reason"`
		}
		if !decode(w, r, &b) {
			return true
		}
		b.Reason = strings.TrimSpace(b.Reason)
		if b.Reason == "" || len(b.Reason) > 1000 {
			fail(w, 400, "请填写退款原因")
			return true
		}
		d := a.Store.Snapshot()
		p := findPayment(&d, parts[1])
		if p == nil {
			fail(w, 404, "付款不存在")
			return true
		}
		if p.Test {
			e := a.commit(u, s, func(d *State) error {
				return refundPayment(d, parts[1], "test-refund-"+parts[1], b.Reason, time.Now().Unix(), u.ID)
			})
			if e != nil {
				failCommit(w, e, 409, "无法退款，请核实付款状态")
				return true
			}
			reply(w, 200, map[string]bool{"test": true, "refunded": true})
			return true
		}
		if p.Status == "refunded" {
			reply(w, 200, map[string]bool{"refunded": true})
			return true
		}
		if p.Provider != "stripe" {
			fail(w, 409, "此渠道需要先在支付平台或钱包完成退款，再登记退款流水；不会从钱包自动转账")
			return true
		}
		if p.IntentID == "" || p.Status != "paid" {
			fail(w, 409, "付款状态不允许退款")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			for _, r := range d.Refunds {
				if r.PaymentID == p.ID {
					return nil
				}
			}
			d.Refunds = append(d.Refunds, Refund{ID: ID(), PaymentID: p.ID, AmountCents: p.AmountCents, Status: "pending", Reason: b.Reason, CreatedAt: time.Now().Unix()})
			return nil
		})
		if e != nil {
			failCommit(w, e, 503, "无法保存退款请求")
			return true
		}
		fields := url.Values{"payment_intent": {p.IntentID}, "amount": {strconv.FormatInt(p.AmountCents, 10)}}
		result, e := a.stripe(r.Context(), "refunds", fields, "refund-"+p.ID)
		if e != nil {
			fail(w, 502, "退款结果尚未确认，请核实渠道记录后重试")
			return true
		}
		id, _ := result["id"].(string)
		status, _ := result["status"].(string)
		if id == "" {
			fail(w, 502, "退款渠道响应无效")
			return true
		}
		e = a.commit(u, s, func(d *State) error {
			if status == "succeeded" {
				return refundPayment(d, p.ID, id, b.Reason, time.Now().Unix(), u.ID)
			}
			for i := range d.Refunds {
				if d.Refunds[i].PaymentID == p.ID {
					d.Refunds[i].Reference = id
				}
			}
			return nil
		})
		if e != nil {
			failCommit(w, e, 503, "渠道已接收退款，后台保存失败，待回调或对账恢复")
			return true
		}
		reply(w, 200, map[string]string{"status": status})
		return true
	}
	return false
}
func validStripeSignature(raw []byte, header, secret string, now int64) bool {
	if secret == "" || len(header) > 2048 {
		return false
	}
	stamp := ""
	signatures := []string{}
	for _, p := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			continue
		}
		if k == "t" {
			if stamp != "" {
				return false
			}
			stamp = v
		} else if k == "v1" {
			signatures = append(signatures, v)
		}
	}
	timestamp, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || timestamp < now-300 || timestamp > now+300 {
		return false
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stamp + "."))
	h.Write(raw)
	expected := h.Sum(nil)
	for _, v := range signatures {
		b, e := hex.DecodeString(v)
		if e == nil && subtle.ConstantTimeCompare(b, expected) == 1 {
			return true
		}
	}
	return false
}
func (a *API) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if e != nil || !validStripeSignature(raw, r.Header.Get("Stripe-Signature"), a.Config.Commercial.WebhookSecret, time.Now().Unix()) {
		fail(w, 400, "通知签名无效")
		return
	}
	var event struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		LiveMode bool   `json:"livemode"`
		Data     struct {
			Object struct {
				ID                string            `json:"id"`
				PaymentStatus     string            `json:"payment_status"`
				Status            string            `json:"status"`
				AmountTotal       int64             `json:"amount_total"`
				Amount            int64             `json:"amount"`
				Currency          string            `json:"currency"`
				ClientReferenceID string            `json:"client_reference_id"`
				PaymentIntent     string            `json:"payment_intent"`
				Metadata          map[string]string `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if e = json.NewDecoder(bytes.NewReader(raw)).Decode(&event); e != nil || event.ID == "" || len(event.ID) > 128 {
		fail(w, 400, "通知格式无效")
		return
	}
	if !a.Config.Commercial.LiveApproved || !event.LiveMode {
		fail(w, 409, "正式支付通知与环境不匹配")
		return
	}
	object := event.Data.Object
	e = a.Store.Update(func(d *State) error {
		switch event.Type {
		case "checkout.session.completed", "checkout.session.async_payment_succeeded":
			if object.PaymentStatus != "paid" {
				return nil
			}
			var p *Payment
			for i := range d.Payments {
				candidate := &d.Payments[i]
				if candidate.Provider == "stripe" && (candidate.Reference == object.ID || candidate.ID == object.Metadata["payment_id"]) {
					p = candidate
					break
				}
			}
			if p == nil || p.Test || p.OrderID != object.ClientReferenceID || p.Reference != "" && p.Reference != object.ID {
				return errors.New("unmatched payment")
			}
			p.Reference = object.ID
			p.IntentID = object.PaymentIntent
			return paidEvent(d, p.ID, event.ID, object.AmountTotal, object.Currency, false, time.Now().Unix())
		case "refund.updated", "refund.created":
			if object.Status != "succeeded" {
				return nil
			}
			for _, p := range d.Payments {
				if p.Provider == "stripe" && p.IntentID == object.PaymentIntent && object.Amount == p.AmountCents && object.Currency == p.Currency {
					return refundPayment(d, p.ID, event.ID, "支付渠道退款确认", time.Now().Unix())
				}
			}
			return errors.New("unmatched refund")
		default:
			return nil
		}
	})
	if e != nil {
		fail(w, 409, "通知未处理，请核实订单或重试")
		return
	}
	reply(w, 200, map[string]bool{"received": true})
}
