package control

import (
	"errors"
	"strings"
	"time"
)

func financialPlan(p *Plan) error {
	if p.Kind == "" {
		p.Kind = "subscription"
	}
	if p.Currency == "" {
		p.Currency = "cny"
	}
	p.Currency = strings.ToLower(p.Currency)
	if p.Kind != "subscription" && p.Kind != "traffic" {
		return errors.New("套餐类型无效")
	}
	if p.Currency != "cny" && p.Currency != "usd" && p.Currency != "eur" {
		return errors.New("币种无效")
	}
	if p.Kind == "traffic" && p.TrafficBytes <= 0 {
		return errors.New("流量包需要正数额度")
	}
	return nil
}
func findOrder(d *State, id string) *Order {
	for i := range d.Orders {
		if d.Orders[i].ID == id {
			return &d.Orders[i]
		}
	}
	return nil
}
func findPayment(d *State, id string) *Payment {
	for i := range d.Payments {
		if d.Payments[i].ID == id {
			return &d.Payments[i]
		}
	}
	return nil
}
func usage(d *State, u User, test bool) int64 {
	if test {
		v := d.TestUsage[u.ID]
		return v.Upload + v.Download
	}
	return u.Upload + u.Download
}

var errTrafficBaseRequired = errors.New("流量加购需要当前有效且节点范围匹配的周期套餐")

func trafficBaseEnd(d *State, o Order, now int64) int64 {
	end := int64(0)
	for _, g := range d.Entitlements {
		if g.UserID != o.UserID || g.Test != o.Test || g.RevokedAt != 0 || g.StartsAt > now || g.EndsAt <= now || g.Kind == "traffic" {
			continue
		}
		matches := len(o.Plan.NodeIDs) == 0 || len(g.NodeIDs) == 0
		for _, node := range o.Plan.NodeIDs {
			for _, allowed := range g.NodeIDs {
				matches = matches || node == allowed
			}
		}
		if matches && g.EndsAt > end {
			end = g.EndsAt
		}
	}
	return end
}

func validatePurchase(d *State, o Order, now int64) error {
	if err := validatePlanNodes(d, o.Plan); err != nil {
		return err
	}
	if o.Plan.Kind == "traffic" && trafficBaseEnd(d, o, now) == 0 {
		return errTrafficBaseRequired
	}
	return nil
}
func grantPurchase(d *State, o *Order, now int64) error {
	for _, e := range d.Entitlements {
		if e.OrderID == o.ID && e.Source == "purchase" {
			return nil
		}
	}
	if err := validatePurchase(d, *o, now); err != nil {
		return err
	}
	u := findUser(d, o.UserID)
	if u == nil {
		return errors.New("missing account")
	}
	p := o.Plan
	if e := financialPlan(&p); e != nil {
		return e
	}
	start := now
	end := now + int64(p.Days)*86400
	if p.Kind == "traffic" {
		end = trafficBaseEnd(d, *o, now)
		if end == 0 {
			return errTrafficBaseRequired
		}
	} else {
		if !o.Test && u.ExpiresAt > start {
			start = u.ExpiresAt
		}
		for _, g := range d.Entitlements {
			// Traffic packs augment an existing cycle; they cannot postpone a new
			// subscription after their original base subscription was refunded.
			if g.UserID == u.ID && g.Test == o.Test && g.RevokedAt == 0 && g.Kind != "traffic" && g.EndsAt > start {
				start = g.EndsAt
			}
		}
		end = start + int64(p.Days)*86400
	}
	devices := p.Devices
	if p.Kind == "traffic" {
		devices = 0
	}
	d.Entitlements = append(d.Entitlements, Entitlement{ID: ID(), UserID: u.ID, OrderID: o.ID, Source: "purchase", StartsAt: start, EndsAt: end, Bytes: p.TrafficBytes, Devices: devices, SpeedLimit: p.SpeedLimit, NodeIDs: p.NodeIDs, Test: o.Test, Kind: p.Kind})
	return nil
}

// Only managed grants for the selected scope and node can authorize commerce users.
func accountForNode(d *State, u User, n Node, now int64) User {
	if u.Role == "admin" {
		return u
	}
	hasGrants := false
	for _, g := range d.Entitlements {
		if g.UserID == u.ID && g.Test == n.TestOnly {
			hasGrants = true
			break
		}
	}
	if !n.TestOnly && (!hasGrants || u.ExpiresAt > now) {
		return u
	}
	v := u
	v.ExpiresAt = 0
	v.Limit = 1
	v.Upload = 0
	v.Download = 0
	v.Devices = 1
	if n.TestOnly && !u.Beta || u.Disabled || u.NeedsEmailVerification && u.EmailVerifiedAt == 0 {
		return v
	}
	if n.ID != "" && (!hasCapability(n, "strict-billing") || !hasCapability(n, "leases-v1")) {
		return v
	}
	base := false
	for _, g := range d.Entitlements {
		if g.UserID == u.ID && g.Test == n.TestOnly && g.RevokedAt == 0 && g.StartsAt <= now && g.EndsAt > now && g.Kind != "traffic" && (n.ID == "" || entitledToNode(g, n)) {
			base = true
			break
		}
	}
	if !base {
		return v
	}
	if n.TestOnly {
		counter := d.TestUsage[u.ID]
		v.Upload = counter.Upload
		v.Download = counter.Download
	} else {
		v.Upload = u.Upload
		v.Download = u.Download
	}
	v.Limit = v.Upload + v.Download
	remaining := int64(0)
	unlimited := false
	// Only subscriptions carry a speed tier; 0 means unlimited and must win.
	v.SpeedLimit = 0
	unlimitedSpeed := false
	for _, g := range d.Entitlements {
		if g.UserID != u.ID || g.Test != n.TestOnly || g.RevokedAt != 0 || g.StartsAt > now || g.EndsAt <= now {
			continue
		}
		allowed := len(g.NodeIDs) == 0 || n.ID == ""
		for _, id := range g.NodeIDs {
			if id == n.ID {
				allowed = true
			}
		}
		if !allowed {
			continue
		}
		if g.EndsAt > v.ExpiresAt {
			v.ExpiresAt = g.EndsAt
		}
		if g.Devices > v.Devices {
			v.Devices = g.Devices
		}
		if g.Kind != "traffic" {
			if g.SpeedLimit == 0 {
				unlimitedSpeed = true
			} else if g.SpeedLimit > v.SpeedLimit {
				v.SpeedLimit = g.SpeedLimit
			}
		}
		if g.Bytes == 0 && g.Kind != "traffic" {
			unlimited = true
		} else if g.Bytes > g.Used {
			remaining += g.Bytes - g.Used
		}
	}
	if unlimitedSpeed {
		v.SpeedLimit = 0
	}
	if unlimited {
		v.Limit = 0
	} else {
		v.Limit += remaining
		if remaining == 0 {
			v.QuotaExhausted = true
		}
	}
	return v
}
func consumeEntitlements(d *State, userID string, test bool, amount, now int64) {
	for i := range d.Entitlements {
		g := &d.Entitlements[i]
		if amount <= 0 {
			break
		}
		if g.UserID != userID || g.Test != test || g.RevokedAt != 0 || g.StartsAt > now || g.EndsAt <= now {
			continue
		}
		if g.Bytes == 0 && g.Kind != "traffic" {
			g.Used += amount
			return
		}
		room := g.Bytes - g.Used
		if room <= 0 {
			continue
		}
		take := amount
		if take > room {
			take = room
		}
		g.Used += take
		amount -= take
	}
}
func paidEvent(d *State, paymentID, eventID string, amount int64, currency string, test bool, now int64) error {
	p := findPayment(d, paymentID)
	if p == nil {
		return errors.New("unknown payment")
	}
	o := findOrder(d, p.OrderID)
	if o == nil {
		return errors.New("unknown order")
	}
	if amount != p.AmountCents || currency != p.Currency || amount != o.Plan.PriceCents || currency != o.Plan.Currency || test != p.Test || test != o.Test {
		return errors.New("payment amount, currency or scope mismatch")
	}
	for _, event := range d.PaymentEvents {
		if event.Provider == p.Provider && event.ExternalID == eventID {
			if event.ObjectID != p.ID || event.Type != "paid" {
				return errors.New("event identity mismatch")
			}
			return nil
		}
	}
	if p.Status == "paid" || p.Status == "refunded" {
		d.PaymentEvents = append(d.PaymentEvents, PaymentEvent{ID: ID(), Provider: p.Provider, ExternalID: eventID, ObjectID: p.ID, Type: "paid", Time: now})
		return nil
	}
	external := p.Provider == "stripe" || p.Provider == "epay" || p.Provider == "bepusdt"
	if (o.Status != "pending" || o.ExpiresAt > 0 && o.ExpiresAt <= now) && !external {
		return errors.New("order is no longer payable")
	}
	if external && (o.Status == "cancelled" || o.Status == "expired" || o.ExpiresAt > 0 && o.ExpiresAt <= now) {
		incident(d, "late-payment:"+p.ID, "", "已收到过期或取消订单的有效付款，请核对订单履约状态并对账。", true, now)
	}
	grantErr := grantPurchase(d, o, now)
	if grantErr != nil && !external {
		return grantErr
	}
	p.Status = "paid"
	p.PaidAt = now
	o.Status = "paid"
	if grantErr != nil {
		// A genuine receipt must remain refundable even if the base subscription
		// expired between checkout and payment. Do not discard or retry the receipt.
		o.Status = "paid_review"
		incident(d, "payment-fulfilment:"+p.ID, "", "已收到付款，但套餐条件发生变化，权益尚未发放。请核实付款记录并安排退款。", true, now)
	}
	o.PaidAt = now
	o.PaymentID = p.ID
	d.PaymentEvents = append(d.PaymentEvents, PaymentEvent{ID: ID(), Provider: p.Provider, ExternalID: eventID, ObjectID: p.ID, Type: "paid", Time: now})
	actor := "system:" + p.Provider
	if p.Test {
		actor = o.UserID
	}
	record(d, actor, "payment_paid", o.ID)
	return nil
}
func refundPayment(d *State, paymentID, eventID, reason string, now int64, actorIDs ...string) error {
	p := findPayment(d, paymentID)
	if p == nil {
		return errors.New("missing payment")
	}
	if p.Status == "refunded" {
		return nil
	}
	if p.Status != "paid" {
		return errors.New("payment not paid")
	}
	for _, event := range d.PaymentEvents {
		if event.Provider == p.Provider && event.ExternalID == eventID {
			return errors.New("event already belongs to another operation")
		}
	}
	o := findOrder(d, p.OrderID)
	if o == nil {
		return errors.New("missing order")
	}
	for i := range d.Entitlements {
		g := &d.Entitlements[i]
		if g.OrderID == o.ID && g.Test == p.Test {
			g.RevokedAt = now
		}
	}
	end := now
	u := findUser(d, o.UserID)
	if u != nil && !p.Test && u.ExpiresAt > end {
		end = u.ExpiresAt
	}
	for i := range d.Entitlements {
		g := &d.Entitlements[i]
		if g.UserID != o.UserID || g.Test != p.Test || g.RevokedAt > 0 || g.Kind == "traffic" || g.EndsAt <= now {
			continue
		}
		if g.StartsAt > now {
			duration := g.EndsAt - g.StartsAt
			g.StartsAt = end
			g.EndsAt = end + duration
		}
		if g.EndsAt > end {
			end = g.EndsAt
		}
	}
	found := false
	for i := range d.Refunds {
		r := &d.Refunds[i]
		if r.PaymentID == p.ID {
			r.Status = "succeeded"
			r.Reference = eventID
			found = true
		}
	}
	if !found {
		d.Refunds = append(d.Refunds, Refund{ID: ID(), PaymentID: p.ID, Reference: eventID, AmountCents: p.AmountCents, Status: "succeeded", Reason: reason, CreatedAt: now, Test: p.Test})
	}
	p.Status = "refunded"
	o.Status = "refunded"
	incident(d, "payment-fulfilment:"+p.ID, "", "", false, now)
	d.PaymentEvents = append(d.PaymentEvents, PaymentEvent{ID: ID(), Provider: p.Provider, ExternalID: eventID, ObjectID: p.ID, Type: "refunded", Time: now})
	actor := "system:" + p.Provider
	if len(actorIDs) > 0 && actorIDs[0] != "" {
		actor = actorIDs[0]
	}
	record(d, actor, "payment_refunded", o.ID)
	return nil
}
func billingSummary(d *State, u User) map[string]any {
	grants := []Entitlement{}
	for _, g := range d.Entitlements {
		if g.UserID == u.ID {
			grants = append(grants, g)
		}
	}
	payments := []Payment{}
	for _, p := range d.Payments {
		if o := findOrder(d, p.OrderID); o != nil && o.UserID == u.ID {
			payments = append(payments, p.Public())
		}
	}
	refunds := []Refund{}
	for _, r := range d.Refunds {
		for _, p := range payments {
			if r.PaymentID == p.ID {
				refunds = append(refunds, r)
			}
		}
	}
	counter := d.TestUsage[u.ID]
	return map[string]any{"entitlements": grants, "payments": payments, "refunds": refunds, "test_usage": counter, "rules": map[string]string{"period": "按套餐天数计算连续周期，提前续费从已有周期末尾开始", "quota": "额度按各周期分别使用，下一周期额度不能提前消费；到期剩余额度不结转", "addon": "流量包即时加入当前有效周期，到期时间沿用当前周期", "refund": "本版支持整笔退款，确认成功后撤销对应订单权益", "meter": "统计隧道业务上下行字节，不等同运营商出口账单"}}
}
func expireOrders(d *State, now int64) {
	for i := range d.Orders {
		o := &d.Orders[i]
		if o.Status == "pending" && o.ExpiresAt > 0 && o.ExpiresAt <= now {
			o.Status = "expired"
		}
	}
}

var billingNow = func() int64 { return time.Now().Unix() }
