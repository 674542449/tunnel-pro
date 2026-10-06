package control

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// AuditChange contains display strings only. Private configuration and complete
// model values must never be marshalled into this record.
type AuditChange struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type auditValue struct{ label, text, fingerprint string }
type auditObject struct {
	name   string
	fields map[string]auditValue
}
type auditView struct {
	actors  map[string]string
	objects map[string]auditObject
}

func auditText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return s
}

func (o auditObject) put(key, label, value string) {
	o.fields[key] = auditValue{label: label, text: auditText(value), fingerprint: value}
}

// Equality fingerprints are ephemeral and are never part of Audit or its JSON.
func (o auditObject) privatePresence(key, label, value string) {
	text := "未配置"
	if value != "" {
		text = "已配置"
	}
	o.fields[key] = auditValue{label: label, text: text, fingerprint: Hash(value)}
}
func (o auditObject) contentPresence(key, label, value string) {
	text := "未填写"
	if value != "" {
		text = fmt.Sprintf("已填写（%d 字）", utf8.RuneCountInString(value))
	}
	o.fields[key] = auditValue{label: label, text: text, fingerprint: Hash(value)}
}
func auditBool(v bool) string {
	if v {
		return "是"
	}
	return "否"
}
func auditTime(v int64) string {
	if v == 0 {
		return "未设置"
	}
	return time.Unix(v, 0).UTC().Format(time.RFC3339)
}
func auditOrigin(raw string) string {
	if raw == "" {
		return "未配置"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "已配置地址"
	}
	return u.Scheme + "://" + u.Host
}

func (o auditObject) address(key, label, raw string) {
	o.fields[key] = auditValue{label: label, text: auditOrigin(raw), fingerprint: Hash(raw)}
}

func auditAmount(cents int64, currency string) string {
	if currency == "" {
		currency = "cny"
	}
	return money(cents) + " " + strings.ToUpper(currency)
}

// Explicit allowlists are used for each entity; no reflection or JSON encoding
// of a source entity is used to infer audit fields.
func auditSnapshot(d *State) auditView {
	v := auditView{actors: map[string]string{"system": "系统"}, objects: map[string]auditObject{}}
	add := func(kind, id, name string) auditObject {
		o := auditObject{name: auditText(name), fields: map[string]auditValue{}}
		v.objects[kind+":"+id] = o
		return o
	}
	for _, u := range d.Users {
		v.actors[u.ID] = auditText(u.Email)
		o := add("user", u.ID, u.Email)
		o.put("email", "账号", u.Email)
		o.put("role", "分组", u.Role)
		o.put("disabled", "已停用", auditBool(u.Disabled))
		o.put("expires_at", "到期时间", auditTime(u.ExpiresAt))
		o.put("traffic_limit", "历史账号额度（字节）", fmt.Sprint(u.Limit))
		o.put("device_limit", "设备上限", fmt.Sprint(u.Devices))
		o.put("email_verified", "邮箱已验证", auditBool(u.EmailVerifiedAt > 0))
		o.put("mfa_enabled", "二次验证已启用", auditBool(u.MFASecret != ""))
		o.put("beta", "试运营资格", auditBool(u.Beta))
	}
	for _, n := range d.Nodes {
		v.actors[n.ID] = "节点 · " + auditText(n.Name)
		o := add("node", n.ID, n.Name)
		o.put("name", "名称", n.Name)
		o.put("region", "地区", n.Region)
		o.put("enabled", "已启用", auditBool(n.Enabled))
		o.put("test_only", "仅测试节点", auditBool(n.TestOnly))
		o.put("server_ip", "服务器 IP", n.Client.ServerIP)
		o.put("port", "端口", fmt.Sprint(n.Client.Port))
		o.put("server_name", "服务器名称", n.Client.ServerName)
		o.put("pending_install", "安装未完成", auditBool(n.Pending()))
		o.put("certificate_configured", "节点证书已配置", auditBool(n.CAPEM != ""))
		if n.Setup != nil {
			o.put("public_domain", "外层证书名称", n.Setup.Domain)
			mode := "公开证书"
			if n.Setup.CertificateMode == "private" {
				mode = "自建证书"
			}
			o.put("certificate_mode", "证书模式", mode)
			o.put("install_expires_at", "安装命令有效期", auditTime(n.Setup.ExpiresAt))
		}
	}
	nodeNames := func(ids []string) string {
		if len(ids) == 0 {
			return "全部符合账号范围的节点"
		}
		names := append([]string{}, ids...)
		sort.Strings(names)
		for i, id := range names {
			if n := findNode(d, id); n != nil {
				names[i] = n.Name + "（" + id + "）"
			}
		}
		return strings.Join(names, "、")
	}
	planFields := func(o auditObject, p Plan) {
		o.put("name", "套餐名称", p.Name)
		o.put("kind", "套餐类型", p.Kind)
		o.put("days", "周期天数", fmt.Sprint(p.Days))
		o.put("price", "价格", auditAmount(p.PriceCents, p.Currency))
		o.put("traffic_bytes", "流量额度（字节；0 为不限）", fmt.Sprint(p.TrafficBytes))
		o.put("devices", "设备上限", fmt.Sprint(p.Devices))
		o.put("node_scope", "节点范围", nodeNames(p.NodeIDs))
	}
	for _, p := range d.Plans {
		o := add("plan", p.ID, p.Name)
		planFields(o, p)
		o.put("enabled", "已上架", auditBool(p.Enabled))
	}
	for _, order := range d.Orders {
		o := add("order", order.ID, order.Plan.Name+" · "+order.ID)
		planFields(o, order.Plan)
		o.put("status", "订单状态", order.Status)
		o.put("test", "测试订单", auditBool(order.Test))
		o.put("customer", "购买账号", v.actors[order.UserID])
		o.put("payment_id", "付款 ID", order.PaymentID)
	}
	for _, p := range d.Payments {
		o := add("payment", p.ID, p.Provider+" · "+p.ID)
		o.put("order_id", "订单 ID", p.OrderID)
		o.put("provider", "支付渠道", p.Provider)
		o.put("method", "支付方式", p.Method)
		o.put("amount", "金额", auditAmount(p.AmountCents, p.Currency))
		o.put("status", "付款状态", p.Status)
		o.put("checkout_state", "收银台状态", p.CheckoutState)
		o.put("test", "测试付款", auditBool(p.Test))
	}
	for _, t := range d.Tickets {
		o := add("ticket", t.ID, t.Subject)
		o.put("subject", "工单标题", t.Subject)
		o.put("status", "工单状态", t.Status)
		o.put("reply_count", "消息数量", fmt.Sprint(len(t.Replies)))
	}
	for _, item := range d.Incidents {
		o := add("incident", item.ID, item.Message)
		o.put("kind", "告警类型", strings.SplitN(item.Key, ":", 2)[0])
		o.put("acknowledged", "已确认", auditBool(item.AcknowledgedAt > 0))
		o.put("resolved", "已恢复", auditBool(item.ResolvedAt > 0))
	}
	for _, invite := range d.Invites {
		o := add("invite", invite.ID, "试运营邀请 · "+invite.ID)
		o.put("expires_at", "有效期", auditTime(invite.ExpiresAt))
		o.put("used", "已使用", auditBool(invite.UsedAt > 0))
	}
	for _, lease := range d.Leases {
		o := add("lease", lease.ID, "流量租约 · "+lease.ID)
		o.put("used", "确认已用（字节）", fmt.Sprint(lease.Used))
		o.put("budget", "累计预留（字节）", fmt.Sprint(lease.Budget))
		o.put("closed", "已关闭", auditBool(lease.Closed))
		o.put("test", "测试范围", auditBool(lease.Test))
	}
	for _, g := range d.Entitlements {
		if g.Source != "admin_assignment" {
			continue
		}
		o := add("entitlement", g.ID, g.PlanName+" · "+v.actors[g.UserID])
		o.put("customer", "分配账号", v.actors[g.UserID])
		o.put("plan", "套餐", g.PlanName)
		o.put("starts_at", "生效时间", auditTime(g.StartsAt))
		o.put("ends_at", "到期时间", auditTime(g.EndsAt))
		o.put("quota", "额度（字节；0 为不限）", fmt.Sprint(g.Bytes))
		o.put("devices", "设备上限", fmt.Sprint(g.Devices))
		o.put("node_scope", "节点范围", nodeNames(g.NodeIDs))
		o.put("test", "测试范围", auditBool(g.Test))
		o.put("reason", "分配原因", g.Reason)
	}
	settings := add("settings", "", "网站设置")
	settings.contentPresence("announcement", "公告内容", d.Announcement)
	settings.put("release_version", "客户端发布版本", d.Release.Version)
	settings.address("release_host", "下载站点", d.Release.URL)
	settings.put("release_sha256", "安装包 SHA256", d.Release.SHA256)
	settings.contentPresence("release_notes", "发布说明", d.Release.Notes)
	if d.Access != nil {
		settings.put("registration", "开放注册", auditBool(d.Access.Registration))
		settings.put("trial_hours", "试用小时数", fmt.Sprint(d.Access.TrialHours))
	}
	gateways := add("payment_settings", "", "支付配置")
	if d.PaymentConfig != nil {
		gateways.put("mode", "付款模式", d.PaymentConfig.Mode)
		for _, entry := range []struct {
			name   string
			config GatewayConfig
		}{{"epay", d.PaymentConfig.Epay}, {"bepusdt", d.PaymentConfig.Bepusdt}} {
			g, prefix := entry.config, entry.name+"."
			gateways.put(prefix+"enabled", entry.name+" 已启用", auditBool(g.Enabled))
			gateways.address(prefix+"host", entry.name+" 网关站点", g.URL)
			gateways.privatePresence(prefix+"key", entry.name+" 商户密钥", g.Secret)
			if entry.name == "epay" {
				gateways.put(prefix+"merchant_id", "易支付商户 ID", g.MerchantID)
				gateways.put(prefix+"alipay", "支付宝已启用", auditBool(g.Alipay))
				gateways.put(prefix+"wechat", "微信支付已启用", auditBool(g.Wechat))
			} else {
				gateways.put(prefix+"trade_types", "USDT 网络", strings.Join(usdtTradeTypes(g), "、"))
			}
		}
	}
	return v
}

func auditAction(action string) (kind, summary string) {
	switch action {
	case "settings_saved":
		return "settings", "保存网站设置"
	case "desktop_release_published":
		return "settings", "发布已验收的客户端更新"
	case "payment_settings_saved":
		return "payment_settings", "保存支付渠道配置"
	case "register":
		return "user", "注册账号"
	case "login":
		return "user", "登录账号"
	case "trial":
		return "user", "领取试用权益"
	case "password_reset":
		return "user", "管理员重置账号密码"
	case "password_changed":
		return "user", "修改账号密码"
	case "security_reset":
		return "user", "通过找回链接重置密码"
	case "security_verify":
		return "user", "完成邮箱验证"
	case "mfa_enabled":
		return "user", "启用二次验证"
	case "mfa_disabled":
		return "user", "关闭二次验证"
	case "role_changed":
		return "user", "变更账号分组"
	case "role_migrated":
		return "user", "旧账号分组迁移为用户"
	case "user_status":
		return "user", "变更账号启用状态"
	case "user_renewed":
		return "user", "管理员调整历史账号权益"
	case "user_token_rotated":
		return "user", "重设凭证"
	case "user_plan_assigned":
		return "entitlement", "管理员分配套餐"
	case "node_saved":
		return "node", "保存节点配置"
	case "node_status":
		return "node", "变更节点启用状态"
	case "node_scope_changed":
		return "node", "调整节点测试 / 正式范围"
	case "node_deleted":
		return "node", "删除节点"
	case "node_install_command":
		return "node", "生成或更新节点安装命令"
	case "node_installed":
		return "node", "节点完成安装登记"
	case "plan_saved":
		return "plan", "保存套餐"
	case "plan_deleted":
		return "plan", "删除套餐"
	case "order_created":
		return "order", "创建订单"
	case "order_cancelled":
		return "order", "取消订单"
	case "order_fulfilled":
		return "order", "管理员开通历史订单"
	case "payment_paid":
		return "order", "确认付款到账并处理权益"
	case "payment_refunded":
		return "order", "确认整笔退款并撤销对应权益"
	case "payment_checkout_created":
		return "payment", "创建付款记录并准备收银台"
	case "gateway_payment_expired":
		return "payment", "支付网关确认付款过期"
	case "external_refund_confirmed":
		return "payment", "管理员登记已完成的外部退款"
	case "ticket_created":
		return "ticket", "提交工单"
	case "ticket_reply":
		return "ticket", "回复工单"
	case "ticket_close":
		return "ticket", "关闭工单"
	case "incident_opened":
		return "incident", "记录运维告警"
	case "incident_ack":
		return "incident", "确认运维告警"
	case "incident_resolved":
		return "incident", "记录故障恢复"
	case "beta_invite_created":
		return "invite", "生成试运营邀请"
	case "lease_reconciled":
		return "lease", "核实并关闭流量预留租约"
	default:
		return "", "记录系统操作"
	}
}

func enrichAudit(before auditView, after *State, start int) {
	if start < 0 || start >= len(after.Audit) {
		return
	}
	current := auditSnapshot(after)
	for i := start; i < len(after.Audit); i++ {
		a := &after.Audit[i]
		a.ActorName = current.actors[a.Actor]
		if a.ActorName == "" {
			a.ActorName = before.actors[a.Actor]
		}
		if strings.HasPrefix(a.Actor, "system:") {
			provider := strings.TrimPrefix(a.Actor, "system:")
			name := map[string]string{"epay": "易支付", "bepusdt": "USDT 网关", "stripe": "Stripe", "test": "模拟支付"}[provider]
			if name == "" {
				name = "支付渠道"
			}
			a.ActorName = "系统 · " + name
		}
		if a.ActorName == "" {
			a.ActorName = auditText(a.Actor)
		}
		kind, summary := auditAction(a.Action)
		a.Summary = summary
		prior, oldExists := before.objects[kind+":"+a.Subject]
		next, newExists := current.objects[kind+":"+a.Subject]
		a.SubjectName = next.name
		if !newExists {
			a.SubjectName = prior.name
		}
		if a.SubjectName == "" {
			a.SubjectName = auditText(a.Subject)
		}
		keys := map[string]bool{}
		for key := range prior.fields {
			keys[key] = true
		}
		for key := range next.fields {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			old, oldOK := prior.fields[key]
			value, newOK := next.fields[key]
			if oldOK && newOK && old.fingerprint == value.fingerprint {
				continue
			}
			label, from, to := value.label, old.text, value.text
			if label == "" {
				label = old.label
			}
			if !oldExists || !oldOK {
				from = "未设置"
			}
			if !newExists || !newOK {
				to = "已移除"
			}
			if oldOK && newOK && from == to {
				to += "（已更新）"
			}
			a.Changes = append(a.Changes, AuditChange{Field: key, Label: label, Before: from, After: to})
		}
	}
}
