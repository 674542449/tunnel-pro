package control

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"tunnelx/internal/config"
)

type bucket struct {
	start time.Time
	count int
}
type API struct {
	Config      Config
	Store       *Store
	mu          sync.Mutex
	limits      map[string]bucket
	passwords   chan struct{}
	dummyHash   string
	origin      string
	basePath    string
	ArtifactDir string
	PaymentHTTP *http.Client
}

func NewAPI(c Config) (*API, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	s, e := OpenStore(c)
	if e != nil {
		return nil, e
	}
	dummy, e := passwordHash(Token())
	if e != nil {
		return nil, e
	}
	u, _ := url.Parse(c.PublicURL)
	executable, e := os.Executable()
	if e != nil {
		// Minimal chroots may omit /proc; management still works without artifacts.
		executable, e = filepath.Abs(os.Args[0])
		if e != nil {
			return nil, e
		}
	}
	return &API{Config: c, Store: s, limits: map[string]bucket{}, passwords: make(chan struct{}, 4), dummyHash: dummy, origin: u.Scheme + "://" + u.Host, basePath: strings.TrimRight(u.Path, "/"), ArtifactDir: filepath.Join(filepath.Dir(executable), "node-artifacts")}, nil
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}
func (a *API) cookiePath() string {
	u, _ := url.Parse(a.Config.PublicURL)
	if u.Path == "" || u.Path == "/" {
		return "/"
	}
	return strings.TrimRight(u.Path, "/") + "/"
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "请求格式无效")
		return false
	}
	return true
}
func (a *API) throttle(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsLoopback() {
		if f := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(f) != nil {
			ip = f
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.limits {
		if now.Sub(v.start) > 2*time.Minute {
			delete(a.limits, k)
		}
	}
	if len(a.limits) >= 10000 {
		return false
	}
	b := a.limits[ip]
	if now.Sub(b.start) > time.Minute {
		b = bucket{start: now}
	}
	b.count++
	a.limits[ip] = b
	return b.count <= 12
}
func (a *API) auth(r *http.Request) (*User, *Session, bool) {
	token := ""
	cookie := false
	heads := r.Header.Values("Authorization")
	if len(heads) == 1 && strings.HasPrefix(heads[0], "Bearer ") {
		token = strings.TrimPrefix(heads[0], "Bearer ")
	} else if c, e := r.Cookie("tunnelx_session"); e == nil {
		token = c.Value
		cookie = true
	}
	if len(token) != 43 {
		return nil, nil, false
	}
	d := a.Store.Snapshot()
	h := Hash(token)
	for _, s := range d.Sessions {
		if s.Hash == h && s.ExpiresAt > time.Now().Unix() {
			u := findUser(&d, s.UserID)
			if u == nil || u.Disabled {
				return nil, nil, false
			}
			if cookie && r.Method != "GET" && (r.Header.Get("Origin") != a.origin || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.CSRF)) != 1) {
				return nil, nil, false
			}
			return u, &s, true
		}
	}
	return nil, nil, false
}
func (a *API) login(w http.ResponseWriter, r *http.Request, register bool) {
	if !a.throttle(r) {
		fail(w, 429, "请求过于频繁，请稍后再试")
		return
	}
	select {
	case a.passwords <- struct{}{}:
		defer func() { <-a.passwords }()
	default:
		fail(w, 429, "服务繁忙")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
		Invite   string `json:"invite"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(body.Email) || len(body.Password) < 12 || len(body.Password) > 256 {
		fail(w, 400, "邮箱无效，密码需要 12 至 256 字节")
		return
	}
	d := a.Store.Snapshot()
	var user *User
	for _, u := range d.Users {
		if u.Email == body.Email {
			v := u
			user = &v
			break
		}
	}
	if register {
		if !a.accessSettings(&d).Registration {
			fail(w, 403, "注册已关闭")
			return
		}
		if user != nil {
			fail(w, 409, "邮箱已存在")
			return
		}
		h, e := passwordHash(body.Password)
		if e != nil {
			fail(w, 500, "密码处理失败")
			return
		}
		v := User{ID: ID(), Email: body.Email, PasswordHash: h, TunnelToken: Token(), Role: "user", Devices: 3, CreatedAt: time.Now().Unix()}
		v.NeedsEmailVerification = a.Config.Commercial.Enabled
		user = &v
		if e = a.Store.Update(func(d *State) error {
			if a.Config.Commercial.Enabled {
				if a.Config.Mail.Mode != "smtp" && a.Config.Mail.Mode != "test" {
					return errors.New("mail unavailable")
				}
				if a.Config.Commercial.BetaInviteOnly {
					found := false
					for i := range d.Invites {
						v := &d.Invites[i]
						if v.UsedAt == 0 && v.ExpiresAt > time.Now().Unix() && v.TokenHash == Hash(body.Invite) {
							v.UsedAt = time.Now().Unix()
							v.UserID = user.ID
							user.Beta = true
							found = true
							break
						}
					}
					if !found {
						return errBetaInvite
					}
				}
				if e := a.challengeEmail(d, *user, "verify", time.Now().Unix()); e != nil {
					return e
				}
			}
			if !a.accessSettings(d).Registration {
				return errors.New("registration closed")
			}
			if len(d.Users) >= 10000 {
				return errors.New("user limit")
			}
			for _, u := range d.Users {
				if u.Email == v.Email {
					return errors.New("duplicate user")
				}
			}
			d.Users = append(d.Users, *user)
			record(d, v.ID, "register", v.ID)
			return nil
		}); e != nil {
			if errors.Is(e, errBetaInvite) {
				fail(w, 403, "试运营邀请码无效、已过期或已被使用")
				return
			}
			fail(w, 409, "注册未完成")
			return
		}
	} else {
		h := a.dummyHash
		if user != nil {
			h = user.PasswordHash
		}
		valid := passwordValid(h, body.Password)
		if !valid || user == nil || user.Disabled {
			fail(w, 401, "账号或密码无效")
			return
		}
		if user.MFASecret != "" && body.Code == "" {
			reply(w, 401, map[string]any{"error": "请输入验证器验证码或恢复码", "mfa_required": true})
			return
		}
	}
	token := Token()
	session := Session{Hash: Hash(token), UserID: user.ID, CSRF: Token(), ExpiresAt: time.Now().Add(24 * time.Hour).Unix()}
	if e := a.Store.Update(func(d *State) error {
		now := time.Now().Unix()
		keep := d.Sessions[:0]
		for _, s := range d.Sessions {
			if s.ExpiresAt > now {
				keep = append(keep, s)
			}
		}
		d.Sessions = keep
		if len(d.Sessions) >= 20000 {
			return errors.New("session limit")
		}
		current := findUser(d, user.ID)
		if current == nil || current.Disabled || current.PasswordHash != user.PasswordHash {
			return errors.New("disabled")
		}
		if current.MFASecret != "" {
			if !a.verifyMFA(current, body.Code, now) {
				return errMFAInvalid
			}
			session.MFA = true
		}
		d.Sessions = append(d.Sessions, session)
		record(d, user.ID, "login", user.ID)
		return nil
	}); e != nil {
		if errors.Is(e, errMFAInvalid) {
			reply(w, 401, map[string]any{"error": "验证码无效或已使用，请等待下一组验证码或使用恢复码", "mfa_required": true})
			return
		}
		fail(w, 503, "无法保存会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "tunnelx_session", Value: token, Path: a.cookiePath(), HttpOnly: true, Secure: strings.HasPrefix(a.Config.PublicURL, "https:"), SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	reply(w, 200, map[string]any{"token": token, "csrf": session.CSRF, "user": user.Public(), "version": ConsoleVersion})
}
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.basePath != "" {
		if r.URL.Path == a.basePath {
			http.Redirect(w, r, a.basePath+"/", http.StatusMovedPermanently)
			return
		}
		if strings.HasPrefix(r.URL.Path, a.basePath+"/") {
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, a.basePath)
			r = r2
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if o := r.Header.Get("Origin"); o != "" && o != a.origin {
		fail(w, 403, "来源不被允许")
		return
	}
	if r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/admin" || r.URL.Path == "/app.js" || r.URL.Path == "/commerce.js" || r.URL.Path == "/style.css" || r.URL.Path == "/icons.svg" || r.URL.Path == "/network.svg" || r.URL.Path == "/mark.svg") {
		a.web(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/health" {
		a.Store.Snapshot()
		status := 200
		if !a.Store.Healthy() {
			status = 503
		}
		reply(w, status, map[string]any{"ready": a.Store.Healthy(), "version": ConsoleVersion, "transport": "h2"})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/install-node.sh" {
		a.installScript(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/node/setup") {
		a.nodeSetup(w, r)
		return
	}
	if a.publicSecurity(w, r) || a.publicCommerce(w, r) {
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/public/settings" {
		d := a.Store.Snapshot()
		reply(w, 200, a.accessSettings(&d))
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/login" {
		a.login(w, r, false)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/register" {
		a.login(w, r, true)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/node/sync" {
		a.syncNode(w, r)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/node/lease" {
		a.nodeLease(w, r)
		return
	}
	u, session, ok := a.auth(r)
	if !ok {
		fail(w, 401, "请登录")
		return
	}
	if a.security(w, r, u, session) || a.commerce(w, r, u, session) || a.support(w, r, u, session) {
		return
	}
	d := a.Store.Snapshot()
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		if !adminPathAllowed(u.Role, strings.TrimPrefix(r.URL.Path, "/api/admin/"), r.Method) {
			fail(w, 403, "需要管理员权限")
			return
		}
		if a.mfaRequired(u, session) {
			a.failMFA(w, u)
			return
		}
		a.admin(w, r, u, session)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/me":
		v := accountPublic(&d, *u, u.Beta && a.paymentMode(&d) == "test", time.Now().Unix())
		reply(w, 200, map[string]any{"user": v, "csrf": session.CSRF, "announcement": d.Announcement, "release": d.Release, "trial_hours": a.accessSettings(&d).TrialHours, "commerce": a.Config.Commercial.Enabled, "security": map[string]bool{"admin_mfa_required": a.Config.Commercial.RequireAdminMFA, "mfa_session": session.MFA, "mfa_enabled": u.MFASecret != ""}})
	case r.Method == "GET" && r.URL.Path == "/api/release":
		reply(w, 200, d.Release)
	case r.Method == "POST" && r.URL.Path == "/api/logout":
		e := a.commit(u, session, func(d *State) error {
			keep := d.Sessions[:0]
			for _, s := range d.Sessions {
				if s.Hash != session.Hash {
					keep = append(keep, s)
				}
			}
			d.Sessions = keep
			return nil
		})
		if e != nil {
			failCommit(w, e, 503, "无法保存退出状态")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "tunnelx_session", Path: a.cookiePath(), MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(a.Config.PublicURL, "https:"), SameSite: http.SameSiteStrictMode})
		if a.cookiePath() != "/" {
			http.SetCookie(w, &http.Cookie{Name: "tunnelx_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(a.Config.PublicURL, "https:"), SameSite: http.SameSiteStrictMode})
		}
		reply(w, 200, map[string]bool{"ok": true})
	case r.Method == "GET" && r.URL.Path == "/api/plans":
		plans := []Plan{}
		for _, p := range d.Plans {
			if p.Enabled {
				plans = append(plans, p)
			}
		}
		reply(w, 200, plans)
	case r.Method == "GET" && r.URL.Path == "/api/nodes":
		nodes := []map[string]any{}
		for _, n := range d.Nodes {
			if n.Enabled && !n.Pending() {
				nodes = append(nodes, userNode(&d, *u, n, time.Now().Unix()))
			}
		}
		reply(w, 200, nodes)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/nodes/") && (strings.HasSuffix(r.URL.Path, "/profile") || strings.HasSuffix(r.URL.Path, "/profile.zip")):
		suffix := "/profile"
		bundle := strings.HasSuffix(r.URL.Path, "/profile.zip")
		if bundle {
			suffix = "/profile.zip"
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), suffix)
		a.profile(w, r, u, findNode(&d, id), bundle)
	case r.Method == "POST" && r.URL.Path == "/api/trial":
		if a.Config.Commercial.Enabled {
			fail(w, 403, "商业模式请使用获邀测试套餐")
			return
		}
		if u.NeedsEmailVerification && u.EmailVerifiedAt == 0 {
			fail(w, 403, "请先验证邮箱")
			return
		}
		e := a.commit(u, session, func(d *State) error {
			hours := a.accessSettings(d).TrialHours
			if hours == 0 {
				return errTrialDisabled
			}
			x := findUser(d, u.ID)
			if x == nil || x.TrialUsed || x.Role == "admin" || x.Active(time.Now().Unix()) {
				return errors.New("trial used")
			}
			x.TrialUsed = true
			start := time.Now().Unix()
			if x.ExpiresAt > start {
				start = x.ExpiresAt
			}
			x.ExpiresAt = start + int64(hours)*3600
			x.Limit = x.Upload + x.Download + 1<<30
			record(d, u.ID, "trial", u.ID)
			return nil
		})
		if e != nil {
			failCommit(w, e, 409, "试用已使用、账号已开通或保存失败")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	case r.Method == "GET" && r.URL.Path == "/api/orders":
		orders := []Order{}
		for _, o := range d.Orders {
			if o.UserID == u.ID {
				orders = append(orders, o)
			}
		}
		reply(w, 200, orders)
	case r.Method == "POST" && r.URL.Path == "/api/orders":
		if u.NeedsEmailVerification && u.EmailVerifiedAt == 0 {
			fail(w, 403, "请先验证邮箱")
			return
		}
		var body struct {
			PlanID string `json:"plan_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		var order Order
		e := a.commit(u, session, func(d *State) error {
			for _, p := range d.Plans {
				if p.ID == body.PlanID && p.Enabled {
					if len(d.Orders) >= 10000 {
						return errors.New("order limit")
					}
					order = Order{ID: ID(), UserID: u.ID, Plan: p, Status: "pending", CreatedAt: time.Now().Unix()}
					if a.Config.Commercial.Enabled {
						order.ExpiresAt = time.Now().Add(30 * time.Minute).Unix()
						if e := financialPlan(&order.Plan); e != nil {
							return e
						}
						order.Test = a.paymentMode(d) == "test"
						if order.Test && !u.Beta && u.Role != "admin" {
							return errBetaInvite
						}
						if e := validatePurchase(d, order, time.Now().Unix()); e != nil {
							return e
						}
					}
					d.Orders = append(d.Orders, order)
					record(d, u.ID, "order_created", order.ID)
					return nil
				}
			}
			return errors.New("plan missing")
		})
		if e != nil {
			failCommit(w, e, 400, "套餐无效或订单无法保存")
			return
		}
		reply(w, 201, order)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/orders/") && strings.HasSuffix(r.URL.Path, "/cancel"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/orders/"), "/cancel")
		if e := a.commit(u, session, func(d *State) error { return cancelOrder(d, id, u) }); e != nil {
			failCommit(w, e, 409, "订单不存在、已开通或无法取消")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "接口不存在")
	}
}
func (a *API) admin(w http.ResponseWriter, r *http.Request, actor *User, session *Session) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/")
	parts := strings.Split(path, "/")
	if a.adminCommerce(w, r, actor, session, path, parts) {
		return
	}
	if a.adminNodeSetup(w, r, actor, session, path, parts) {
		return
	}
	d := a.Store.Snapshot()
	if r.Method == "GET" {
		switch path {
		case "summary":
			online := 0
			for _, n := range d.Nodes {
				if n.Enabled && n.LastSeen > time.Now().Unix()-90 {
					online++
				}
			}
			nt := map[string][2]int64{}
			for key, counter := range d.Reports {
				parts := strings.SplitN(key, ":", 3)
				if len(parts) == 3 {
					v := nt[parts[0]]
					v[0] += counter.Upload
					v[1] += counter.Download
					nt[parts[0]] = v
				}
			}
			nodeTraffic := []map[string]any{}
			for _, n := range d.Nodes {
				entry := map[string]any{"id": n.ID, "name": n.Name, "active": n.Active, "upload": int64(0), "download": int64(0)}
				if t, ok := nt[n.ID]; ok {
					entry["upload"] = t[0]
					entry["download"] = t[1]
				}
				nodeTraffic = append(nodeTraffic, entry)
			}
			reply(w, 200, map[string]any{"users": len(d.Users), "nodes": len(d.Nodes), "online_nodes": online, "orders": len(d.Orders), "version": ConsoleVersion, "transport": "h2", "node_traffic": nodeTraffic, "traffic_history": d.TrafficHistory})
			return
		case "users":
			v := []map[string]any{}
			for _, u := range d.Users {
				projected := accountPublic(&d, u, u.Beta && a.paymentMode(&d) == "test", time.Now().Unix())
				v = append(v, projected)
			}
			reply(w, 200, v)
			return
		case "nodes":
			v := []map[string]any{}
			for _, n := range d.Nodes {
				v = append(v, n.Public())
			}
			reply(w, 200, v)
			return
		case "plans":
			reply(w, 200, d.Plans)
			return
		case "orders":
			reply(w, 200, d.Orders)
			return
		case "audit":
			reply(w, 200, d.Audit)
			return
		case "settings":
			reply(w, 200, map[string]any{"announcement": d.Announcement, "release": d.Release, "access": a.accessSettings(&d)})
			return
		}
		if len(parts) == 2 && parts[0] == "nodes" {
			n := findNode(&d, parts[1])
			if n == nil {
				fail(w, 404, "节点不存在")
				return
			}
			reply(w, 200, map[string]any{"id": n.ID, "name": n.Name, "region": n.Region, "enabled": n.Enabled, "client": n.Client, "ca_pem": n.CAPEM, "test_only": n.TestOnly})
			return
		}
		if len(parts) == 3 && parts[0] == "nodes" && parts[2] == "agent-config" {
			n := findNode(&d, parts[1])
			if n == nil {
				fail(w, 404, "节点不存在")
				return
			}
			reply(w, 200, map[string]any{"api_url": a.Config.PublicURL, "node_id": n.ID, "agent_key": n.AgentKey, "state_file": "/var/lib/tunnelx/agent-state.json"})
			return
		}
	}
	if r.Method != "POST" {
		fail(w, 404, "接口不存在")
		return
	}
	var fn func(*State) error
	switch {
	case path == "users":
		var b struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			PlanID   string `json:"plan_id"`
		}
		if !decode(w, r, &b) {
			return
		}
		b.Email = strings.ToLower(strings.TrimSpace(b.Email))
		if !validEmail(b.Email) || len(b.Password) < 12 || len(b.Password) > 256 {
			fail(w, 400, "邮箱无效，密码需要 12 至 256 字节")
			return
		}
		if b.PlanID != "" && !a.Config.Commercial.Enabled {
			fail(w, 409, "请在商业套餐模式下分配套餐；旧版授权可在创建后使用续期")
			return
		}
		hash, e := passwordHash(b.Password)
		if e != nil {
			fail(w, 500, "密码处理失败")
			return
		}
		var validationErr error
		e = a.commit(actor, session, func(d *State) error {
			for _, u := range d.Users {
				if u.Email == b.Email {
					validationErr = errors.New("邮箱已存在")
					return validationErr
				}
			}
			if len(d.Users) >= 10000 {
				validationErr = errors.New("用户数量已达上限")
				return validationErr
			}
			u := User{ID: ID(), Email: b.Email, PasswordHash: hash, TunnelToken: Token(), Role: "user", Devices: 3, CreatedAt: time.Now().Unix()}
			d.Users = append(d.Users, u)
			record(d, actor.ID, "register", u.ID)
			if b.PlanID != "" {
				validationErr = assignPlan(d, actor.ID, u.ID, assignmentRequest{PlanID: b.PlanID, Mode: "immediate", Reason: "管理员创建账号时分配", RequestID: ID()}, time.Now().Unix())
				return validationErr
			}
			return nil
		})
		if e != nil {
			if errors.Is(e, errSessionRevoked) {
				failCommit(w, e, 409, "")
			} else if validationErr != nil {
				fail(w, 409, validationErr.Error())
			} else {
				fail(w, 409, "创建未保存，请核实存储状态后重试")
			}
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "assign-plan":
		a.adminAssign(w, r, actor, session, parts[1])
		return
	case path == "nodes" || len(parts) == 2 && parts[0] == "nodes":
		var b struct {
			Node
			TestOnly *bool `json:"test_only"`
		}
		if !decode(w, r, &b) {
			return
		}
		n := b.Node
		if e := validateNode(&n); e != nil {
			fail(w, 400, e.Error())
			return
		}
		fn = func(d *State) error {
			if len(parts) == 1 {
				if len(d.Nodes) >= 256 {
					return errors.New("node limit")
				}
				n.ID = ID()
				n.AgentKey = Token()
				n.TestOnly = a.newNodeTestOnly(d, b.TestOnly)
				d.Nodes = append(d.Nodes, n)
			} else {
				old := findNode(d, parts[1])
				if old == nil || old.Pending() {
					return errors.New("missing node")
				}
				n.ID = old.ID
				n.AgentKey = old.AgentKey
				n.LastSeen = old.LastSeen
				n.Active = old.Active
				n.Version = old.Version
				n.Setup = old.Setup
				n.TestOnly = old.TestOnly
				n.Capabilities = old.Capabilities
				n.LastProbe = old.LastProbe
				n.ProbeOK = old.ProbeOK
				n.ProbeLatencyMS = old.ProbeLatencyMS
				*old = n
			}
			record(d, actor.ID, "node_saved", n.ID)
			return nil
		}
	case path == "plans" || len(parts) == 2 && parts[0] == "plans":
		var p Plan
		if !decode(w, r, &p) {
			return
		}
		p.Name = strings.TrimSpace(p.Name)
		if a.Config.Commercial.Enabled {
			if e := financialPlan(&p); e != nil {
				fail(w, 400, e.Error())
				return
			}
		}
		if validatePlan(p) != nil {
			fail(w, 400, "套餐参数无效")
			return
		}
		fn = func(d *State) error {
			if e := validatePlanNodes(d, p); e != nil {
				return e
			}
			if len(parts) == 1 {
				if len(d.Plans) >= 256 {
					return errors.New("plan limit")
				}
				p.ID = ID()
				d.Plans = append(d.Plans, p)
			} else {
				found := false
				for i := range d.Plans {
					if d.Plans[i].ID == parts[1] {
						p.ID = parts[1]
						d.Plans[i] = p
						found = true
					}
				}
				if !found {
					return errors.New("missing plan")
				}
			}
			record(d, actor.ID, "plan_saved", p.ID)
			return nil
		}
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "scope":
		var b struct {
			TestOnly bool `json:"test_only"`
		}
		if !decode(w, r, &b) {
			return
		}
		fn = func(d *State) error {
			n, e := changeNodeScope(d, parts[1], b.TestOnly, time.Now().Unix())
			if e != nil {
				return e
			}
			record(d, actor.ID, "node_scope_changed", n.ID)
			return nil
		}
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "enabled":
		var b struct {
			Enabled bool `json:"enabled"`
		}
		if !decode(w, r, &b) {
			return
		}
		fn = func(d *State) error {
			n := findNode(d, parts[1])
			if n == nil {
				return errors.New("missing node")
			}
			if n.Pending() {
				return errors.New("complete installation first")
			}
			n.Enabled = b.Enabled
			record(d, actor.ID, "node_status", n.ID)
			return nil
		}
	case len(parts) == 3 && (parts[0] == "nodes" || parts[0] == "plans") && parts[2] == "delete":
		var b struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &b) {
			return
		}
		fn = func(d *State) error {
			if parts[0] == "nodes" {
				for i, n := range d.Nodes {
					if n.ID != parts[1] {
						continue
					}
					if b.Name != n.Name || n.Enabled || n.Active != 0 || n.LastSeen > time.Now().Unix()-90 {
						return errors.New("disable and stop node first")
					}
					if nodeReferenced(d, n.ID, time.Now().Unix()) {
						return errNodeReferenced
					}
					d.Nodes = append(d.Nodes[:i], d.Nodes[i+1:]...)
					record(d, actor.ID, "node_deleted", n.ID)
					return nil
				}
			} else {
				for i, p := range d.Plans {
					if p.ID != parts[1] {
						continue
					}
					if b.Name != p.Name || p.Enabled {
						return errors.New("disable plan first")
					}
					d.Plans = append(d.Plans[:i], d.Plans[i+1:]...)
					record(d, actor.ID, "plan_deleted", p.ID)
					return nil
				}
			}
			return errors.New("record missing")
		}
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "password":
		var b struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &b) {
			return
		}
		if len(b.Password) < 12 || len(b.Password) > 256 {
			fail(w, 400, "密码长度无效")
			return
		}
		hash, e := passwordHash(b.Password)
		if e != nil {
			fail(w, 500, "密码处理失败")
			return
		}
		fn = func(d *State) error {
			u := findUser(d, parts[1])
			if u == nil {
				return errors.New("missing user")
			}
			u.PasswordHash = hash
			u.TunnelToken = Token()
			invalidateCredentialChallenges(d, u.ID, time.Now().Unix())
			sessions := d.Sessions[:0]
			for _, s := range d.Sessions {
				if s.UserID != u.ID {
					sessions = append(sessions, s)
				}
			}
			d.Sessions = sessions
			record(d, actor.ID, "password_reset", u.ID)
			return nil
		}
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "delete":
		var b struct {
			Email string `json:"email"`
		}
		if !decode(w, r, &b) {
			return
		}
		fn = func(d *State) error {
			if parts[1] == actor.ID {
				return errUserDeleteAdmin
			}
			if e := deleteUser(d, parts[1], strings.ToLower(strings.TrimSpace(b.Email))); e != nil {
				return e
			}
			record(d, actor.ID, "user_deleted", parts[1])
			return nil
		}
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "update":
		var b struct {
			Disabled bool `json:"disabled"`
		}
		if !decode(w, r, &b) {
			return
		}
		fn = func(d *State) error {
			u := findUser(d, parts[1])
			if u == nil || u.Role == "admin" {
				return errors.New("cannot change administrator")
			}
			u.Disabled = b.Disabled
			record(d, actor.ID, "user_status", u.ID)
			return nil
		}
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "renew":
		if a.Config.Commercial.Enabled {
			fail(w, 409, "请通过可审计的购买和退款流程调整权益")
			return
		}
		var b struct {
			Days         int   `json:"days"`
			TrafficBytes int64 `json:"traffic_bytes"`
			Devices      int   `json:"devices"`
			SpeedLimit   int64 `json:"speed_limit"`
		}
		if !decode(w, r, &b) {
			return
		}
		fn = func(d *State) error {
			u := findUser(d, parts[1])
			if u == nil {
				return errors.New("missing user")
			}
			if e := extend(u, b.Days, b.TrafficBytes, b.Devices, b.SpeedLimit); e != nil {
				return e
			}
			record(d, actor.ID, "user_renewed", u.ID)
			return nil
		}
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "rotate":
		fn = func(d *State) error {
			u := findUser(d, parts[1])
			if u == nil {
				return errors.New("missing user")
			}
			u.TunnelToken = Token()
			record(d, actor.ID, "user_token_rotated", u.ID)
			return nil
		}
	case len(parts) == 3 && parts[0] == "orders" && parts[2] == "fulfil":
		if a.Config.Commercial.Enabled {
			fail(w, 409, "商业订单由支付通知确认，测试订单请模拟付款")
			return
		}
		fn = func(d *State) error {
			for i := range d.Orders {
				o := &d.Orders[i]
				if o.ID == parts[1] {
					if o.Status == "paid" {
						return nil
					}
					if o.Status != "pending" {
						return errors.New("invalid order status")
					}
					u := findUser(d, o.UserID)
					if u == nil || u.Disabled {
						return errors.New("missing or disabled user")
					}
					if e := extend(u, o.Plan.Days, o.Plan.TrafficBytes, o.Plan.Devices, o.Plan.SpeedLimit); e != nil {
						return e
					}
					o.Status = "paid"
					o.PaidAt = time.Now().Unix()
					record(d, actor.ID, "order_fulfilled", o.ID)
					return nil
				}
			}
			return errors.New("missing order")
		}
	case len(parts) == 3 && parts[0] == "orders" && parts[2] == "cancel":
		fn = func(d *State) error { return cancelOrder(d, parts[1], actor) }
	case path == "settings":
		var b struct {
			Announcement *string         `json:"announcement"`
			Release      *Release        `json:"release"`
			Access       *AccessSettings `json:"access"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.Announcement == nil && b.Release == nil && b.Access == nil {
			fail(w, 400, "需要设置内容")
			return
		}
		if b.Announcement != nil && len(*b.Announcement) > 4000 {
			fail(w, 400, "公告过长")
			return
		}
		if b.Access != nil && (b.Access.TrialHours < 0 || b.Access.TrialHours > 168) {
			fail(w, 400, "试用时间需要 0 至 168 小时")
			return
		}
		if b.Access != nil && a.Config.Commercial.Enabled && b.Access.TrialHours != 0 {
			fail(w, 400, "商业模式不支持旧版免费试用，请使用获邀模拟购买")
			return
		}
		if b.Release != nil {
			if e := validateRelease(b.Release); e != nil {
				fail(w, 400, e.Error())
				return
			}
		}
		fn = func(d *State) error {
			if b.Announcement != nil {
				d.Announcement = *b.Announcement
			}
			if b.Release != nil {
				d.Release = *b.Release
			}
			if b.Access != nil {
				copy := *b.Access
				d.Access = &copy
			}
			record(d, actor.ID, "settings_saved", "")
			return nil
		}
	default:
		fail(w, 404, "接口不存在")
		return
	}
	if e := a.commit(actor, session, fn); e != nil {
		failCommit(w, e, 409, "修改未提交，请检查参数或存储状态")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *API) syncNode(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-TunnelX-Node")
	heads := r.Header.Values("Authorization")
	d := a.Store.Snapshot()
	n := findNode(&d, id)
	if n == nil || len(heads) != 1 || subtle.ConstantTimeCompare([]byte(heads[0]), []byte("Bearer "+n.AgentKey)) != 1 {
		fail(w, 403, "节点认证失败")
		return
	}
	var b SyncRequest
	if !decode(w, r, &b) {
		return
	}
	if len(b.BootID) != 32 || len(b.Version) > 32 || b.Active < 0 || len(b.Counters) > 10000 {
		fail(w, 400, "上报无效")
		return
	}
	seen := map[string]bool{}
	for _, c := range b.Counters {
		if seen[c.UserID] || len(c.UserID) != 32 || c.Upload < 0 || c.Download < 0 || c.Upload > 1<<60 || c.Download > 1<<60 {
			fail(w, 400, "计数无效")
			return
		}
		seen[c.UserID] = true
	}
	response := SyncResponse{ValidForSeconds: 90, Grants: []Grant{}, Acknowledged: b.Counters}
	if e := a.Store.Update(func(d *State) error {
		n := findNode(d, id)
		if n == nil || subtle.ConstantTimeCompare([]byte(heads[0]), []byte("Bearer "+n.AgentKey)) != 1 {
			return errors.New("missing node")
		}
		n.LastSeen = time.Now().Unix()
		n.Active = b.Active
		n.Version = b.Version
		n.Capabilities = b.Capabilities
		if len(b.RecoveredLeases) > 1000 {
			return errors.New("too many recovery leases")
		}
		for _, leaseID := range b.RecoveredLeases {
			found := false
			for i := range d.Leases {
				l := &d.Leases[i]
				if l.ID == leaseID && l.NodeID == n.ID {
					found = true
					if l.Closed {
						response.ClearedRecovery = append(response.ClearedRecovery, l.ID)
					}
				}
				if l.ID == leaseID && l.NodeID == n.ID && !l.Closed {
					l.Uncertain = true
					incident(d, "lease-reconcile:"+l.ID, n.ID, "节点异常重启，有流量预留待对账；未知字节不会自动扣费或重新发放。", true, time.Now().Unix())
				}
			}
			if !found {
				response.ClearedRecovery = append(response.ClearedRecovery, leaseID)
			}
		}
		for _, c := range b.Counters {
			key := id + ":" + b.BootID + ":" + c.UserID
			old := d.Reports[key]
			if c.Upload < old.Upload || c.Download < old.Download {
				return errors.New("counter regression")
			}
			u := findUser(d, c.UserID)
			if u == nil {
				// Deleted account: acknowledge so the node stops resending, but bill nobody.
				d.Reports[key] = c
				continue
			}
			if n.TestOnly {
				test := d.TestUsage[u.ID]
				test.UserID = u.ID
				test.Upload += c.Upload - old.Upload
				test.Download += c.Download - old.Download
				d.TestUsage[u.ID] = test
			} else {
				u.Upload += c.Upload - old.Upload
				u.Download += c.Download - old.Download
			}
			if u.Upload+u.Download > 1<<61 {
				return errors.New("counter overflow")
			}
			d.Reports[key] = c
		}
		if n.Enabled && !n.Pending() {
			for _, u := range d.Users {
				v := accountForNode(d, u, *n, time.Now().Unix())
				commercial := entitled(d, u.ID, n.TestOnly) && !(u.Role == "admin" || !n.TestOnly && u.ExpiresAt > time.Now().Unix())
				if commercial && (!hasCapability(*n, "strict-billing") || !hasCapability(*n, "leases-v1")) {
					continue
				}
				if v.Active(time.Now().Unix()) && !(v.NeedsEmailVerification && v.EmailVerifiedAt == 0) {
					remaining := v.Limit - v.Upload - v.Download
					response.Grants = append(response.Grants, Grant{UserID: u.ID, Token: u.TunnelToken, ExpiresAt: v.ExpiresAt, Remaining: remaining, Unlimited: v.Limit == 0, Devices: v.Devices, SpeedLimit: v.SpeedLimit, RequireLeases: commercial})
				}
			}
		}
		return nil
	}); e != nil {
		fail(w, 409, "上报未提交")
		return
	}
	reply(w, 200, response)
}

// ClientRequest is shared by node agents and desktop clients; redirects never forward credentials.
func ClientRequest(client *http.Client, base, path, token string, input, output any) error {
	_ = config.Version
	var body io.Reader
	method := "GET"
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = strings.NewReader(string(b))
		method = "POST"
	}
	req, e := http.NewRequest(method, strings.TrimRight(base, "/")+path, body)
	if e != nil {
		return e
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := copy.Do(req)
	if e != nil {
		return errors.New("control network request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return safeError(res.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(output)
	}
	return nil
}
