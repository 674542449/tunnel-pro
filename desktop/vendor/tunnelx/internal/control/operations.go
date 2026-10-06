package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"
	"tunnelx/internal/client"
)

func findTicket(d *State, id string) *Ticket {
	for i := range d.Tickets {
		if d.Tickets[i].ID == id {
			return &d.Tickets[i]
		}
	}
	return nil
}
func (a *API) support(w http.ResponseWriter, r *http.Request, u *User, s *Session) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	staff := strings.HasPrefix(path, "admin/")
	if staff {
		path = strings.TrimPrefix(path, "admin/")
	}
	parts := strings.Split(path, "/")
	if parts[0] != "tickets" && parts[0] != "incidents" && path != "operations" {
		return false
	}
	if staff && !adminPathAllowed(u.Role, path, r.Method) {
		fail(w, 403, "需要具备权限并完成二次验证的工作账号")
		return true
	}
	if staff && a.mfaRequired(u, s) {
		a.failMFA(w, u)
		return true
	}
	if !staff && parts[0] != "tickets" {
		return false
	}
	d := a.Store.Snapshot()
	if r.Method == "GET" {
		switch path {
		case "tickets":
			items := []Ticket{}
			for _, t := range d.Tickets {
				if staff || t.UserID == u.ID {
					items = append(items, t)
				}
			}
			reply(w, 200, items)
			return true
		case "incidents":
			reply(w, 200, d.Incidents)
			return true
		case "operations":
			pending := 0
			for _, m := range d.Outbox {
				if m.SentAt == 0 {
					pending++
				}
			}
			reply(w, 200, map[string]any{"storage": a.Store.Backend(), "healthy": a.Store.Healthy(), "mail_mode": a.Config.Mail.Mode, "pending_mail": pending, "incident_count": len(d.Incidents), "checked_at": time.Now().Unix()})
			return true
		}
		fail(w, 404, "接口不存在")
		return true
	}
	if r.Method != "POST" {
		fail(w, 404, "接口不存在")
		return true
	}
	var b struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if !decode(w, r, &b) {
		return true
	}
	b.Subject = strings.TrimSpace(b.Subject)
	b.Body = strings.TrimSpace(b.Body)
	now := time.Now().Unix()
	var ticketID string
	e := a.commit(u, s, func(d *State) error {
		if parts[0] == "incidents" && len(parts) == 3 && parts[2] == "ack" {
			for i := range d.Incidents {
				v := &d.Incidents[i]
				if v.ID == parts[1] {
					v.AcknowledgedAt = now
					record(d, u.ID, "incident_ack", v.ID)
					return nil
				}
			}
			return errors.New("missing incident")
		}
		if path == "tickets" && !staff {
			if b.Subject == "" || len(b.Subject) > 160 || b.Body == "" || len(b.Body) > 8000 {
				return errors.New("invalid ticket")
			}
			open := 0
			for _, t := range d.Tickets {
				if t.UserID == u.ID && t.Status != "closed" {
					open++
				}
			}
			if open >= 10 || len(d.Tickets) >= 10000 {
				return errors.New("ticket limit")
			}
			ticketID = ID()
			d.Tickets = append(d.Tickets, Ticket{ID: ticketID, UserID: u.ID, Subject: b.Subject, Status: "open", CreatedAt: now, UpdatedAt: now, Replies: []TicketReply{{ID: ID(), ActorID: u.ID, Body: b.Body, Time: now}}})
			record(d, u.ID, "ticket_created", ticketID)
			return nil
		}
		if parts[0] != "tickets" || len(parts) != 3 {
			return errors.New("invalid route")
		}
		t := findTicket(d, parts[1])
		if t == nil || !staff && t.UserID != u.ID {
			return errors.New("ticket not owned")
		}
		ticketID = t.ID
		switch parts[2] {
		case "reply":
			if b.Body == "" || len(b.Body) > 8000 || len(t.Replies) >= 200 {
				return errors.New("invalid reply")
			}
			t.Replies = append(t.Replies, TicketReply{ID: ID(), ActorID: u.ID, Body: b.Body, Time: now})
			if staff {
				t.Status = "answered"
			} else {
				t.Status = "open"
			}
		case "close":
			t.Status = "closed"
		default:
			return errors.New("invalid route")
		}
		t.UpdatedAt = now
		record(d, u.ID, "ticket_"+parts[2], t.ID)
		return nil
	})
	if e != nil {
		failCommit(w, e, 409, "提交失败，请检查工单内容、数量和操作权限")
		return true
	}
	reply(w, 200, map[string]any{"ok": true, "ticket_id": ticketID})
	return true
}

func incident(d *State, key, node, message string, problem bool, now int64) {
	for i := range d.Incidents {
		v := &d.Incidents[i]
		if v.Key == key && v.ResolvedAt == 0 {
			if !problem {
				v.ResolvedAt = now
				record(d, "system", "incident_resolved", v.ID)
			}
			return
		}
	}
	if problem {
		v := Incident{ID: ID(), Key: key, NodeID: node, Severity: "warning", Message: message, OpenedAt: now}
		d.Incidents = append(d.Incidents, v)
		record(d, "system", "incident_opened", v.ID)
	}
}

func backupIssue(path string, now int64) string {
	var status struct {
		Time     int64 `json:"time"`
		FailedAt int64 `json:"failed_at"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &status) != nil {
		return "加密备份状态缺失或无法读取，请检查备份服务和备份目录。"
	}
	if status.FailedAt > 0 {
		return "最近一次加密备份失败，请检查备份服务、数据库状态和备份目录。"
	}
	if status.Time > now {
		return "加密备份成功时间晚于当前时间，请检查系统时钟和备份状态文件。"
	}
	if status.Time <= now-7200 || status.Time <= 0 {
		return "加密备份已超过两小时未成功，请检查定时备份服务。"
	}
	return ""
}

func (a *API) maintain(now int64) error {
	return a.Store.Update(func(d *State) error {
		expireOrders(d, now)
		if a.Config.Commercial.Enabled {
			message := backupIssue(filepath.Join(filepath.Dir(a.Config.DataFile), "backup-status.json"), now)
			incident(d, "backup-unavailable", "", message, message != "", now)
		}
		for i := range d.Payments {
			p := &d.Payments[i]
			if p.Provider == "bepusdt" && p.Status == "pending" && p.CheckoutState == "creating" && p.CreatedAt < now-60 {
				p.CheckoutState = "unknown"
				incident(d, "checkout-unknown:"+p.ID, "", "USDT 收银台创建被中断，请在网关核实同一商户订单号，避免重复创建付款。", true, now)
			}
		}
		for _, n := range d.Nodes {
			incident(d, "node-offline:"+n.ID, n.ID, "节点心跳中断超过 90 秒，请检查服务、证书和节点到后台的网络。", n.Enabled && !n.Pending() && (n.LastSeen == 0 || n.LastSeen < now-90), now)
		}
		for _, l := range d.Leases {
			incident(d, "lease-reconcile:"+l.ID, l.NodeID, "节点有未完成对账的流量预留，需要恢复节点或核实最终计数后释放。", !l.Closed && l.Budget > l.Used && l.ExpiresAt < now-90, now)
		}
		pendingFailed := false
		for _, m := range d.Outbox {
			if m.SentAt == 0 && m.Attempts >= 3 {
				pendingFailed = true
			}
		}
		incident(d, "mail-delivery", "", "邮件投递连续失败，验证和找回密码可能延迟。", pendingFailed, now)
		if a.Config.Mail.AlertsTo != "" && (a.Config.Mail.Mode == "smtp" || a.Config.Mail.Mode == "test") {
			for i := range d.Incidents {
				v := &d.Incidents[i]
				recovering := v.ResolvedAt > 0 && v.RecoveryNotifiedAt == 0
				opening := v.NotifiedAt == 0
				if !recovering && !opening {
					continue
				}
				label := "运维告警"
				if recovering {
					label = "服务恢复"
				}
				body, e := a.seal("mail", v.Message+"\n"+a.Config.PublicURL+"/\n告警时间："+time.Unix(v.OpenedAt, 0).UTC().Format(time.RFC3339))
				if e != nil {
					return e
				}
				d.Outbox = append(d.Outbox, MailMessage{ID: ID(), To: a.Config.Mail.AlertsTo, Subject: "tunnelX · " + label, Body: body, CreatedAt: now})
				v.NotifiedAt = now
				if recovering {
					v.RecoveryNotifiedAt = now
				}
			}
		}
		var totalUp, totalDown int64
		for _, u := range d.Users {
			totalUp += u.Upload
			totalDown += u.Download
		}
		lastSnap := int64(0)
		if len(d.TrafficHistory) > 0 {
			lastSnap = d.TrafficHistory[len(d.TrafficHistory)-1].Time
		}
		if now-lastSnap >= 3600 {
			d.TrafficHistory = append(d.TrafficHistory, TrafficSnapshot{Time: now, Upload: totalUp, Download: totalDown})
			if len(d.TrafficHistory) > 168 {
				d.TrafficHistory = d.TrafficHistory[len(d.TrafficHistory)-168:]
			}
		}
		if retention := a.Config.AuditRetentionDays; retention > 0 {
			cutoff := now - int64(retention)*86400
			kept := d.Audit[:0]
			for _, entry := range d.Audit {
				if entry.Time >= cutoff {
					kept = append(kept, entry)
				}
			}
			d.Audit = kept
		}
		return nil
	})
}

// SMTP transport requires authenticated TLS, either implicit TLS on 465 or STARTTLS.
func sendMail(ctx context.Context, c MailConfig, m MailMessage, body string) error {
	if !validEmail(m.To) || strings.ContainsAny(m.Subject+m.To, "\r\n") {
		return errors.New("invalid mail headers")
	}
	address := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	dial := net.Dialer{Timeout: 10 * time.Second}
	connection, e := dial.DialContext(ctx, "tcp", address)
	if e != nil {
		return errors.New("SMTP connection unavailable")
	}
	defer connection.Close()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	connection.SetDeadline(deadline)
	tlsConfig := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var client *smtp.Client
	if c.Port == 465 {
		secured := tls.Client(connection, tlsConfig)
		if e = secured.HandshakeContext(ctx); e != nil {
			return errors.New("SMTP TLS verification failed")
		}
		client, e = smtp.NewClient(secured, c.Host)
	} else {
		client, e = smtp.NewClient(connection, c.Host)
		if e == nil {
			if ok, _ := client.Extension("STARTTLS"); !ok {
				client.Close()
				return errors.New("SMTP requires STARTTLS")
			}
			e = client.StartTLS(tlsConfig)
		}
	}
	if e != nil {
		return errors.New("SMTP TLS setup failed")
	}
	defer client.Close()
	if c.Username != "" {
		if e = client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); e != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if e = client.Mail(c.From); e != nil {
		return errors.New("SMTP sender rejected")
	}
	if e = client.Rcpt(m.To); e != nil {
		return errors.New("SMTP recipient rejected")
	}
	writer, e := client.Data()
	if e != nil {
		return errors.New("SMTP message rejected")
	}
	host := c.Host
	payload := "From: " + c.From + "\r\nTo: " + m.To + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", m.Subject) + "\r\nMessage-ID: <" + m.ID + "@" + host + ">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if _, e = writer.Write([]byte(payload)); e != nil {
		return errors.New("SMTP body transfer failed")
	}
	if e = writer.Close(); e != nil {
		return errors.New("SMTP delivery not confirmed")
	}
	client.Quit()
	return nil
}
func (a *API) deliver(ctx context.Context) {
	if a.Config.Mail.Mode != "smtp" {
		return
	}
	d := a.Store.Snapshot()
	now := time.Now().Unix()
	for _, m := range d.Outbox {
		if ctx.Err() != nil {
			return
		}
		if m.SentAt > 0 || m.NextAttempt > now {
			continue
		}
		body, e := a.unseal("mail", m.Body)
		if e == nil {
			e = sendMail(ctx, a.Config.Mail, m, body)
		}
		a.Store.Update(func(d *State) error {
			for i := range d.Outbox {
				v := &d.Outbox[i]
				if v.ID == m.ID && v.SentAt == 0 {
					v.Attempts++
					if e == nil {
						v.SentAt = time.Now().Unix()
						v.Body = ""
					} else {
						shift := v.Attempts
						if shift > 8 {
							shift = 8
						}
						v.NextAttempt = time.Now().Unix() + int64(1<<shift)*30
					}
					break
				}
			}
			return nil
		})
		break
	}
}
func (a *API) RunMaintenance(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		lastProbe := time.Time{}
		defer ticker.Stop()
		for {
			a.maintain(time.Now().Unix())
			a.deliver(ctx)
			if time.Since(lastProbe) > 5*time.Minute {
				a.probeNodes(ctx)
				lastProbe = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { <-done }
}
func (a *API) probeNodes(ctx context.Context) {
	d := a.Store.Snapshot()
	for _, node := range d.Nodes {
		if ctx.Err() != nil {
			return
		}
		if !node.Enabled || node.Pending() || node.CAPEM == "" {
			continue
		}
		start := time.Now()
		ok := false
		directory, e := os.MkdirTemp(filepath.Dir(a.Config.DataFile), ".network-probe-")
		if e != nil {
			continue
		}
		ca := filepath.Join(directory, "origin-ca.pem")
		e = os.WriteFile(ca, []byte(node.CAPEM), 0600)
		if e == nil {
			cfg := node.Client
			cfg.CAFile = ca
			cfg.Token = Token()
			m, err := client.New(cfg)
			if err == nil {
				c, cancel := context.WithTimeout(ctx, 8*time.Second)
				response, err := m.PublicGET(c)
				if err == nil {
					_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
					response.Body.Close()
					ok = err == nil && response.StatusCode == 200 && response.ProtoMajor == 2 && (cfg.Privacy != "strict" || m.LastECH.Load())
				}
				cancel()
				m.Close()
			}
		}
		os.Remove(ca)
		os.Remove(directory)
		latency := time.Since(start).Milliseconds()
		a.Store.Update(func(d *State) error {
			n := findNode(d, node.ID)
			if n == nil {
				return nil
			}
			n.LastProbe = time.Now().Unix()
			n.ProbeOK = ok
			n.ProbeLatencyMS = latency
			incident(d, "network-probe:"+node.ID, node.ID, "节点 TLS/ECH/HTTP2 外部连接探测失败，请检查公网路径和证书。", !ok, n.LastProbe)
			return nil
		})
	}
}
