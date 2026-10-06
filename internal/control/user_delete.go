package control

import "errors"

var errUserDeleteAdmin = errors.New("管理员账号不能删除，请先把分组改为用户")
var errUserDeletePaid = errors.New("该账号有付款或已开通的订单记录，为保留账务请改用封禁")
var errUserDeleteLeases = errors.New("该账号仍有连接中的流量预留，请先封禁并等待连接断开后再删除")
var errUserDeleteConfirm = errors.New("输入的邮箱与该账号不一致")

// deleteUser removes an account and everything that only exists for it.
// Financial history is never deleted: accounts with payments or fulfilled
// orders must be disabled instead. Audit entries and node traffic reports
// are kept; node sync ignores counters of deleted accounts.
func deleteUser(d *State, id, confirm string) error {
	u := findUser(d, id)
	if u == nil {
		return errors.New("missing user")
	}
	if u.Role == "admin" {
		return errUserDeleteAdmin
	}
	if confirm != u.Email {
		return errUserDeleteConfirm
	}
	orders := map[string]bool{}
	for _, o := range d.Orders {
		if o.UserID != id {
			continue
		}
		if o.Status == "paid" || o.Status == "paid_review" || o.Status == "refunded" {
			return errUserDeletePaid
		}
		orders[o.ID] = true
	}
	for _, p := range d.Payments {
		if orders[p.OrderID] {
			return errUserDeletePaid
		}
	}
	for _, g := range d.Entitlements {
		if g.UserID == id && g.OrderID != "" {
			return errUserDeletePaid
		}
	}
	for _, l := range d.Leases {
		if l.UserID == id && !l.Closed {
			return errUserDeleteLeases
		}
	}
	email := u.Email
	d.Users = keepWhere(d.Users, func(v User) bool { return v.ID != id })
	d.Sessions = keepWhere(d.Sessions, func(v Session) bool { return v.UserID != id })
	d.Challenges = keepWhere(d.Challenges, func(v Challenge) bool { return v.UserID != id && v.Email != email })
	d.Orders = keepWhere(d.Orders, func(v Order) bool { return v.UserID != id })
	d.Entitlements = keepWhere(d.Entitlements, func(v Entitlement) bool { return v.UserID != id })
	d.Leases = keepWhere(d.Leases, func(v Lease) bool { return v.UserID != id })
	d.Tickets = keepWhere(d.Tickets, func(v Ticket) bool { return v.UserID != id })
	d.Outbox = keepWhere(d.Outbox, func(v MailMessage) bool { return v.SentAt != 0 || v.To != email })
	delete(d.TestUsage, id)
	return nil
}

func keepWhere[T any](items []T, ok func(T) bool) []T {
	out := items[:0]
	for _, v := range items {
		if ok(v) {
			out = append(out, v)
		}
	}
	return out
}
