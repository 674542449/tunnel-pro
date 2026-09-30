package main

import (
	"crypto/hmac"
	crypto_md5 "crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// -------- models --------

type User struct {
	ID        int64  `json:"id"`
	Email     string `json:"email,omitempty"`
	PassHash  string `json:"pass_hash,omitempty"`
	MachineID string `json:"machine_id,omitempty"`
	Role      string `json:"role"`
	TrialUsed bool   `json:"trial_used"`
	Disabled  bool   `json:"disabled,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
}

func (u User) IsActive() bool {
	return !u.Disabled && u.ExpiresAt > time.Now().Unix()
}

type VersionInfo struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	Changelog   string `json:"changelog,omitempty"`
}

type Node struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Addr    string `json:"addr"`
	IP      string `json:"ip,omitempty"`
	PSK     string `json:"psk"`
	Region  string `json:"region,omitempty"`
	Enabled bool   `json:"enabled"`
	Sort    int    `json:"sort"`
}

type ClientNode struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	IP     string `json:"ip,omitempty"`
	PSK    string `json:"psk"`
	Region string `json:"region,omitempty"`
}

type Order struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	UserEmail string  `json:"user_email,omitempty"`
	Plan      string  `json:"plan"`
	Days      int     `json:"days"`
	Amount    float64 `json:"amount"`
	Status    string  `json:"status"`
	Method    string  `json:"method,omitempty"`
	CreatedAt int64   `json:"created_at"`
	PaidAt    int64   `json:"paid_at,omitempty"`
}

type Plan struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Days    int     `json:"days"`
	Price   float64 `json:"price"`
	Enabled bool    `json:"enabled"`
}

type TrafficLog struct {
	UserID   int64  `json:"user_id"`
	NodeID   int64  `json:"node_id"`
	Date     string `json:"date"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

// -------- store --------

type EPay struct {
	URL string `json:"url"`
	PID string `json:"pid"`
	Key string `json:"key"`
}

type StoreData struct {
	Users   []User      `json:"users"`
	Nodes   []Node      `json:"nodes"`
	Orders  []Order     `json:"orders"`
	Plans   []Plan      `json:"plans"`
	NextID  int64       `json:"next_id"`
	Secret  string      `json:"secret"`
	EPay    EPay        `json:"epay"`
	Version     VersionInfo  `json:"version"`
	TrafficLogs []TrafficLog `json:"traffic_logs,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data StoreData
}

func NewStore(path string) *Store {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, &s.data)
	}
	if s.data.NextID == 0 {
		s.data.NextID = 1
	}
	if s.data.Secret == "" {
		buf := make([]byte, 32)
		rand.Read(buf)
		s.data.Secret = hex.EncodeToString(buf)
	}
	if len(s.data.Plans) == 0 {
		s.data.Plans = []Plan{
			{ID: s.nextID(), Name: "月卡", Days: 30, Price: 15, Enabled: true},
			{ID: s.nextID(), Name: "季卡", Days: 90, Price: 40, Enabled: true},
			{ID: s.nextID(), Name: "年卡", Days: 365, Price: 120, Enabled: true},
		}
	}
	s.flush()
	return s
}

func (s *Store) nextID() int64 {
	id := s.data.NextID
	s.data.NextID++
	return id
}

func (s *Store) flush() {
	data, _ := json.MarshalIndent(s.data, "", "  ")
	os.WriteFile(s.path, data, 0600)
}

func (s *Store) CreateUser(email, password, machineID, role string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if email != "" {
		for _, u := range s.data.Users {
			if u.Email == email {
				return nil, errors.New("邮箱已注册")
			}
		}
	}
	var hash string
	if password != "" {
		h, _ := bcrypt.GenerateFromPassword([]byte(password), 12)
		hash = string(h)
	}
	u := User{
		ID:        s.nextID(),
		Email:     email,
		PassHash:  hash,
		MachineID: machineID,
		Role:      role,
		CreatedAt: time.Now().Unix(),
	}
	s.data.Users = append(s.data.Users, u)
	s.flush()
	return &u, nil
}

func (s *Store) FindByEmail(email string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Users {
		if s.data.Users[i].Email == email {
			return &s.data.Users[i]
		}
	}
	return nil
}

func (s *Store) FindByMachine(mid string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Users {
		if s.data.Users[i].MachineID == mid {
			return &s.data.Users[i]
		}
	}
	return nil
}

func (s *Store) FindByID(id int64) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Users {
		if s.data.Users[i].ID == id {
			return &s.data.Users[i]
		}
	}
	return nil
}

func (s *Store) UpdateUser(id int64, fn func(*User)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if s.data.Users[i].ID == id {
			fn(&s.data.Users[i])
			s.flush()
			return
		}
	}
}

func (s *Store) DeleteUser(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if s.data.Users[i].ID == id {
			s.data.Users = append(s.data.Users[:i], s.data.Users[i+1:]...)
			s.flush()
			return true
		}
	}
	return false
}

func (s *Store) AllUsers() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, len(s.data.Users))
	copy(out, s.data.Users)
	return out
}

func (s *Store) EnabledNodes() []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Node
	for _, n := range s.data.Nodes {
		if n.Enabled {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sort < out[j].Sort })
	return out
}

func (s *Store) GetNode(id int64) *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Nodes {
		if s.data.Nodes[i].ID == id {
			n := s.data.Nodes[i]
			return &n
		}
	}
	return nil
}

func (s *Store) AllNodes() []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Node, len(s.data.Nodes))
	copy(out, s.data.Nodes)
	return out
}

func (s *Store) AddNode(n Node) Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.ID = s.nextID()
	s.data.Nodes = append(s.data.Nodes, n)
	s.flush()
	return n
}

func (s *Store) UpdateNode(id int64, fn func(*Node)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		if s.data.Nodes[i].ID == id {
			fn(&s.data.Nodes[i])
			s.flush()
			return true
		}
	}
	return false
}

func (s *Store) DeleteNode(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		if s.data.Nodes[i].ID == id {
			s.data.Nodes = append(s.data.Nodes[:i], s.data.Nodes[i+1:]...)
			s.flush()
			return true
		}
	}
	return false
}

func (s *Store) AllPlans() []Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Plan, len(s.data.Plans))
	copy(out, s.data.Plans)
	return out
}

func (s *Store) EnabledPlans() []Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Plan
	for _, p := range s.data.Plans {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) AddOrder(o Order) Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	o.ID = s.nextID()
	o.CreatedAt = time.Now().Unix()
	s.data.Orders = append(s.data.Orders, o)
	s.flush()
	return o
}

func (s *Store) AllOrders() []Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Order, len(s.data.Orders))
	copy(out, s.data.Orders)
	return out
}

func (s *Store) FindOrder(id int64) *Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Orders {
		if s.data.Orders[i].ID == id {
			return &s.data.Orders[i]
		}
	}
	return nil
}

func (s *Store) UpdateOrder(id int64, fn func(*Order)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Orders {
		if s.data.Orders[i].ID == id {
			fn(&s.data.Orders[i])
			s.flush()
			return true
		}
	}
	return false
}

func (s *Store) AddPlan(p Plan) Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.ID = s.nextID()
	s.data.Plans = append(s.data.Plans, p)
	s.flush()
	return p
}

func (s *Store) FindPlan(id int64) *Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Plans {
		if s.data.Plans[i].ID == id {
			return &s.data.Plans[i]
		}
	}
	return nil
}

func (s *Store) UpdatePlan(id int64, fn func(*Plan)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Plans {
		if s.data.Plans[i].ID == id {
			fn(&s.data.Plans[i])
			s.flush()
			return true
		}
	}
	return false
}

func (s *Store) DeletePlan(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Plans {
		if s.data.Plans[i].ID == id {
			s.data.Plans = append(s.data.Plans[:i], s.data.Plans[i+1:]...)
			s.flush()
			return true
		}
	}
	return false
}

func (s *Store) AddTraffic(nodeID int64, userID int64, upload, download int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	for i := range s.data.TrafficLogs {
		t := &s.data.TrafficLogs[i]
		if t.UserID == userID && t.NodeID == nodeID && t.Date == today {
			t.Upload += upload
			t.Download += download
			s.flush()
			return
		}
	}
	s.data.TrafficLogs = append(s.data.TrafficLogs, TrafficLog{
		UserID: userID, NodeID: nodeID, Date: today,
		Upload: upload, Download: download,
	})
	s.flush()
}

func (s *Store) GetUserTraffic() []TrafficLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TrafficLog, len(s.data.TrafficLogs))
	copy(out, s.data.TrafficLogs)
	return out
}

func (s *Store) PruneTraffic(keepDays int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	var kept []TrafficLog
	for _, t := range s.data.TrafficLogs {
		if t.Date >= cutoff {
			kept = append(kept, t)
		}
	}
	if len(kept) != len(s.data.TrafficLogs) {
		s.data.TrafficLogs = kept
		s.flush()
	}
}

func (s *Store) GetEPay() EPay {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.EPay
}

func (s *Store) SetEPay(ep EPay) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.EPay = ep
	s.flush()
}

func (s *Store) GetVersion() VersionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Version
}

func (s *Store) SetVersion(v VersionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Version = v
	s.flush()
}

// -------- JWT --------

type Claims struct {
	UID  int64  `json:"uid"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

func signJWT(c Claims, secret string) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pay, _ := json.Marshal(c)
	payB64 := base64.RawURLEncoding.EncodeToString(pay)
	msg := hdr + "." + payB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return msg + "." + sig
}

func verifyJWT(token, secret string) (*Claims, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, errors.New("invalid token")
	}
	msg := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	expect := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expect)) {
		return nil, errors.New("invalid signature")
	}
	pay, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c Claims
	if err := json.Unmarshal(pay, &c); err != nil {
		return nil, err
	}
	if time.Now().Unix() > c.Exp {
		return nil, errors.New("expired")
	}
	return &c, nil
}

// -------- API server --------

type Config struct {
	Listen        string `json:"listen"`
	AdminKey      string `json:"admin_key"`
	TrialHours    int    `json:"trial_hours"`
	SiteURL       string `json:"site_url"`
	NodeReportKey string `json:"node_report_key"`
}

type NodeStatus struct {
	NodeID    int64 `json:"node_id"`
	ConnCount int   `json:"conn_count"`
	LastSeen  int64 `json:"last_seen"`
}

type API struct {
	store      *Store
	config     Config
	cfgMu      sync.RWMutex
	cfgPath    string
	nodeStatus sync.Map // map[int64]*NodeStatus
}

func (a *API) makeToken(u *User, hours int) string {
	return signJWT(Claims{
		UID:  u.ID,
		Role: u.Role,
		Exp:  time.Now().Add(time.Duration(hours) * time.Hour).Unix(),
	}, a.store.data.Secret)
}

func (a *API) authUser(r *http.Request) (*User, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, errors.New("no token")
	}
	c, err := verifyJWT(strings.TrimPrefix(auth, "Bearer "), a.store.data.Secret)
	if err != nil {
		return nil, err
	}
	u := a.store.FindByID(c.UID)
	if u == nil {
		return nil, errors.New("user not found")
	}
	if u.Disabled {
		return nil, errors.New("账号已被禁用")
	}
	return u, nil
}

func (a *API) requireAdmin(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	c, err := verifyJWT(strings.TrimPrefix(auth, "Bearer "), a.store.data.Secret)
	if err != nil {
		return false
	}
	return c.Role == "admin"
}

// POST /api/admin/login {key}
func (a *API) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Key != a.config.AdminKey {
		writeE(w, 401, "密钥错误")
		return
	}
	token := signJWT(Claims{UID: 0, Role: "admin", Exp: time.Now().Add(24 * time.Hour).Unix()}, a.store.data.Secret)
	writeJ(w, map[string]any{"token": token})
}

func writeJ(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeE(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// POST /api/register {email, password}
func (a *API) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	body.Email = strings.TrimSpace(body.Email)

	if body.Email == "" || body.Password == "" {
		writeE(w, 400, "邮箱和密码不能为空")
		return
	}
	if len(body.Password) < 6 {
		writeE(w, 400, "密码至少6位")
		return
	}

	u, err := a.store.CreateUser(body.Email, body.Password, "", "user")
	if err != nil {
		writeE(w, 400, err.Error())
		return
	}

	token := a.makeToken(u, 720)
	writeJ(w, map[string]any{"token": token, "user": safeUser(u)})
}

// POST /api/login {email, password}
func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	u := a.store.FindByEmail(strings.TrimSpace(body.Email))
	if u == nil {
		writeE(w, 401, "邮箱或密码错误")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(body.Password)); err != nil {
		writeE(w, 401, "邮箱或密码错误")
		return
	}

	token := a.makeToken(u, 720)
	writeJ(w, map[string]any{"token": token, "user": safeUser(u)})
}

// POST /api/guest {machine_id}
func (a *API) handleGuest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MachineID string `json:"machine_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	if body.MachineID == "" {
		writeE(w, 400, "缺少机器标识")
		return
	}

	u := a.store.FindByMachine(body.MachineID)
	if u == nil {
		var err error
		u, err = a.store.CreateUser("", "", body.MachineID, "user")
		if err != nil {
			writeE(w, 500, err.Error())
			return
		}
	}

	token := a.makeToken(u, 720)
	writeJ(w, map[string]any{"token": token, "user": safeUser(u)})
}

// POST /api/trial (requires auth)
func (a *API) handleTrial(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	if u.TrialUsed {
		writeE(w, 400, "试用已使用过")
		return
	}

	hours := a.config.TrialHours
	if hours <= 0 {
		hours = 1
	}
	expires := time.Now().Add(time.Duration(hours) * time.Hour).Unix()

	a.store.UpdateUser(u.ID, func(u *User) {
		u.TrialUsed = true
		u.ExpiresAt = expires
	})

	u = a.store.FindByID(u.ID)
	writeJ(w, map[string]any{"ok": true, "user": safeUser(u)})
}

// GET /api/me
func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	writeJ(w, map[string]any{"user": safeUser(u), "plans": a.store.EnabledPlans()})
}

// GET /api/nodes
func (a *API) handleNodes(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	if !u.IsActive() {
		writeE(w, 403, "套餐已到期")
		return
	}

	nodes := a.store.EnabledNodes()
	out := make([]ClientNode, len(nodes))
	for i, n := range nodes {
		out[i] = ClientNode{ID: n.ID, Name: n.Name, Addr: n.Addr, IP: n.IP, PSK: n.PSK, Region: n.Region}
	}
	writeJ(w, map[string]any{"nodes": out})
}

// -------- EPay helpers --------

func epaySign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k != "sign" && k != "sign_type" && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var buf strings.Builder
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte('&')
		}
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(params[k])
	}
	buf.WriteString(key)
	h := crypto_md5.Sum([]byte(buf.String()))
	return hex.EncodeToString(h[:])
}

// POST /api/buy {plan_id}
func (a *API) handleBuy(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	if u.Email == "" {
		writeE(w, 400, "游客账号不支持购买，请先注册邮箱账号")
		return
	}

	var body struct {
		PlanID int64 `json:"plan_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	plan := a.store.FindPlan(body.PlanID)
	if plan == nil || !plan.Enabled {
		writeE(w, 400, "套餐不存在")
		return
	}

	order := a.store.AddOrder(Order{
		UserID:    u.ID,
		UserEmail: u.Email,
		Plan:      plan.Name,
		Days:      plan.Days,
		Amount:    plan.Price,
		Status:    "pending",
		Method:    "epay",
	})

	ep := a.store.GetEPay()
	if ep.URL == "" {
		writeJ(w, map[string]any{"order_id": order.ID, "message": "支付未配置，请联系管理员"})
		return
	}

	notifyURL := a.config.SiteURL + "/api/pay/notify"
	returnURL := a.config.SiteURL + "/api/pay/return"

	params := map[string]string{
		"pid":          ep.PID,
		"type":         "alipay",
		"out_trade_no": fmt.Sprintf("%d", order.ID),
		"notify_url":   notifyURL,
		"return_url":   returnURL,
		"name":         "Tunnel " + plan.Name,
		"money":        fmt.Sprintf("%.2f", plan.Price),
	}
	params["sign"] = epaySign(params, ep.Key)
	params["sign_type"] = "MD5"

	payURL := ep.URL + "/submit.php?"
	first := true
	for k, v := range params {
		if !first {
			payURL += "&"
		}
		payURL += k + "=" + url.QueryEscape(v)
		first = false
	}

	writeJ(w, map[string]any{"order_id": order.ID, "pay_url": payURL})
}

// GET /api/pay/notify (EPay async callback)
func (a *API) handlePayNotify(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	params := make(map[string]string)
	for k, v := range r.Form {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	sign := params["sign"]
	ep := a.store.GetEPay()
	expect := epaySign(params, ep.Key)
	if sign != expect {
		w.Write([]byte("fail"))
		return
	}

	if params["trade_status"] != "TRADE_SUCCESS" {
		w.Write([]byte("fail"))
		return
	}

	orderID, _ := strconv.ParseInt(params["out_trade_no"], 10, 64)
	order := a.store.FindOrder(orderID)
	if order == nil || order.Status != "pending" {
		w.Write([]byte("success"))
		return
	}

	a.store.UpdateOrder(orderID, func(o *Order) {
		o.Status = "paid"
		o.PaidAt = time.Now().Unix()
	})

	a.store.UpdateUser(order.UserID, func(u *User) {
		base := u.ExpiresAt
		if base < time.Now().Unix() {
			base = time.Now().Unix()
		}
		u.ExpiresAt = base + int64(order.Days)*86400
	})

	log.Printf("[pay] Order %d paid, user %d +%d days", orderID, order.UserID, order.Days)
	w.Write([]byte("success"))
}

// GET /api/pay/return (EPay sync redirect)
func (a *API) handlePayReturn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>支付结果</title></head><body style="background:#0c0c1d;color:#e2e8f0;font-family:sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0"><div style="text-align:center"><h2 style="color:#22c55e;margin-bottom:16px">支付成功</h2><p>套餐已自动到账，请返回客户端查看</p><p style="color:#64748b;margin-top:12px;font-size:14px">可以关闭此页面</p></div></body></html>`))
}

// GET /api/order?id=N
func (a *API) handleOrderStatus(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	orderID, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	order := a.store.FindOrder(orderID)
	if order == nil || order.UserID != u.ID {
		writeE(w, 404, "订单不存在")
		return
	}
	writeJ(w, map[string]any{"order": order})
}

func (a *API) handleMyOrders(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	all := a.store.AllOrders()
	var out []Order
	for _, o := range all {
		if o.UserID == u.ID {
			out = append(out, o)
		}
	}
	writeJ(w, map[string]any{"orders": out})
}

func (a *API) handleMyTraffic(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	logs := a.store.GetUserTraffic()
	var totalUp, totalDown int64
	for _, t := range logs {
		if t.UserID == u.ID {
			totalUp += t.Upload
			totalDown += t.Download
		}
	}
	writeJ(w, map[string]any{"upload": totalUp, "download": totalDown, "total": totalUp + totalDown})
}

func (a *API) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u, err := a.authUser(r)
	if err != nil {
		writeE(w, 401, "请先登录")
		return
	}
	if u.Email == "" {
		writeE(w, 400, "游客账号无法修改密码")
		return
	}
	var body struct {
		OldPass string `json:"old_password"`
		NewPass string `json:"new_password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if len(body.NewPass) < 6 {
		writeE(w, 400, "新密码至少6位")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(body.OldPass)); err != nil {
		writeE(w, 400, "旧密码错误")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(body.NewPass), 12)
	a.store.UpdateUser(u.ID, func(u *User) {
		u.PassHash = string(h)
	})
	writeJ(w, map[string]any{"ok": true})
}

func safeUser(u *User) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"role":       u.Role,
		"trial_used": u.TrialUsed,
		"disabled":   u.Disabled,
		"active":     u.IsActive(),
		"expires_at": u.ExpiresAt,
		"created_at": u.CreatedAt,
	}
}

// -------- admin handlers --------

func (a *API) adminGuard(w http.ResponseWriter, r *http.Request) bool {
	if !a.requireAdmin(r) {
		writeE(w, 403, "无权限")
		return false
	}
	return true
}

func (a *API) adminUsers(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	users := a.store.AllUsers()
	safe := make([]map[string]any, len(users))
	for i, u := range users {
		safe[i] = safeUser(&u)
		safe[i]["machine_id"] = u.MachineID
	}
	writeJ(w, map[string]any{"users": safe})
}

func (a *API) adminExtend(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	var body struct {
		UserID int64 `json:"user_id"`
		Days   int   `json:"days"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Days <= 0 {
		writeE(w, 400, "天数无效")
		return
	}

	u := a.store.FindByID(body.UserID)
	if u == nil {
		writeE(w, 404, "用户不存在")
		return
	}

	a.store.UpdateUser(body.UserID, func(u *User) {
		base := u.ExpiresAt
		if base < time.Now().Unix() {
			base = time.Now().Unix()
		}
		u.ExpiresAt = base + int64(body.Days)*86400
	})

	a.store.AddOrder(Order{
		UserID:    body.UserID,
		UserEmail: u.Email,
		Plan:      fmt.Sprintf("管理员充值 %d天", body.Days),
		Days:      body.Days,
		Status:    "paid",
		Method:    "admin",
		PaidAt:    time.Now().Unix(),
	})

	writeJ(w, map[string]bool{"ok": true})
}

func (a *API) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if !a.store.DeleteUser(id) {
		writeE(w, 404, "用户不存在")
		return
	}
	writeJ(w, map[string]bool{"ok": true})
}

func (a *API) adminToggleUser(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	var body struct {
		UserID   int64 `json:"user_id"`
		Disabled bool  `json:"disabled"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	u := a.store.FindByID(body.UserID)
	if u == nil {
		writeE(w, 404, "用户不存在")
		return
	}
	a.store.UpdateUser(body.UserID, func(u *User) {
		u.Disabled = body.Disabled
	})
	writeJ(w, map[string]bool{"ok": true})
}

func (a *API) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	var body struct {
		UserID   int64  `json:"user_id"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if len(body.Password) < 6 {
		writeE(w, 400, "密码至少6位")
		return
	}
	u := a.store.FindByID(body.UserID)
	if u == nil {
		writeE(w, 404, "用户不存在")
		return
	}
	if u.Email == "" {
		writeE(w, 400, "游客账号无密码")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	a.store.UpdateUser(body.UserID, func(u *User) {
		u.PassHash = string(h)
	})
	writeJ(w, map[string]bool{"ok": true})
}

func (a *API) adminNodes(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}

	switch r.Method {
	case "GET":
		writeJ(w, map[string]any{"nodes": a.store.AllNodes()})

	case "POST":
		var n Node
		json.NewDecoder(r.Body).Decode(&n)
		if n.Addr == "" || n.PSK == "" {
			writeE(w, 400, "addr和psk必填")
			return
		}
		if n.Name == "" {
			n.Name = n.Addr
		}
		n.Enabled = true
		n = a.store.AddNode(n)
		writeJ(w, map[string]any{"ok": true, "node": n})

	case "PUT":
		var body struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Addr    string `json:"addr"`
			IP      string `json:"ip"`
			PSK     string `json:"psk"`
			Region  string `json:"region"`
			Enabled bool   `json:"enabled"`
			Sort    int    `json:"sort"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		ok := a.store.UpdateNode(body.ID, func(n *Node) {
			if body.Name != "" {
				n.Name = body.Name
			}
			if body.Addr != "" {
				n.Addr = body.Addr
			}
			n.IP = body.IP
			if body.PSK != "" {
				n.PSK = body.PSK
			}
			n.Region = body.Region
			n.Enabled = body.Enabled
			n.Sort = body.Sort
		})
		if !ok {
			writeE(w, 404, "节点不存在")
			return
		}
		writeJ(w, map[string]bool{"ok": true})

	case "DELETE":
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if !a.store.DeleteNode(id) {
			writeE(w, 404, "节点不存在")
			return
		}
		writeJ(w, map[string]bool{"ok": true})
	}
}

func (a *API) adminOrders(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	orders := a.store.AllOrders()
	sort.Slice(orders, func(i, j int) bool { return orders[i].CreatedAt > orders[j].CreatedAt })
	writeJ(w, map[string]any{"orders": orders})
}

func (a *API) adminPlans(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	switch r.Method {
	case "GET":
		writeJ(w, map[string]any{"plans": a.store.AllPlans()})

	case "POST":
		var p Plan
		json.NewDecoder(r.Body).Decode(&p)
		if p.Name == "" || p.Days <= 0 || p.Price <= 0 {
			writeE(w, 400, "名称、天数、价格必填")
			return
		}
		p.Enabled = true
		p = a.store.AddPlan(p)
		writeJ(w, map[string]any{"ok": true, "plan": p})

	case "PUT":
		var body struct {
			ID      int64   `json:"id"`
			Name    string  `json:"name"`
			Days    int     `json:"days"`
			Price   float64 `json:"price"`
			Enabled bool    `json:"enabled"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		ok := a.store.UpdatePlan(body.ID, func(p *Plan) {
			if body.Name != "" {
				p.Name = body.Name
			}
			if body.Days > 0 {
				p.Days = body.Days
			}
			if body.Price > 0 {
				p.Price = body.Price
			}
			p.Enabled = body.Enabled
		})
		if !ok {
			writeE(w, 404, "套餐不存在")
			return
		}
		writeJ(w, map[string]bool{"ok": true})

	case "DELETE":
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if !a.store.DeletePlan(id) {
			writeE(w, 404, "套餐不存在")
			return
		}
		writeJ(w, map[string]bool{"ok": true})
	}
}

func (a *API) adminSettings(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	switch r.Method {
	case "GET":
		ep := a.store.GetEPay()
		ver := a.store.GetVersion()
		writeJ(w, map[string]any{"epay": ep, "version": ver, "site_url": a.config.SiteURL, "node_report_key": a.config.NodeReportKey})
	case "PUT":
		var body struct {
			EPay    *EPay        `json:"epay,omitempty"`
			Version *VersionInfo `json:"version,omitempty"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.EPay != nil {
			a.store.SetEPay(*body.EPay)
		}
		if body.Version != nil {
			a.store.SetVersion(*body.Version)
		}
		writeJ(w, map[string]bool{"ok": true})
	}
}

func (a *API) handleVersion(w http.ResponseWriter, r *http.Request) {
	ver := a.store.GetVersion()
	writeJ(w, ver)
}

func (a *API) adminStats(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	users := a.store.AllUsers()
	now := time.Now().Unix()
	total := len(users)
	active := 0
	for _, u := range users {
		if u.ExpiresAt > now {
			active++
		}
	}
	orders := a.store.AllOrders()
	writeJ(w, map[string]any{
		"total_users":  total,
		"active_users": active,
		"total_orders": len(orders),
		"total_nodes":  len(a.store.AllNodes()),
	})
}

// -------- node reporting --------

func (a *API) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID    int64  `json:"node_id"`
		ReportKey string `json:"report_key"`
		ConnCount int    `json:"conn_count"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.ReportKey != a.config.NodeReportKey {
		writeE(w, 403, "invalid report key")
		return
	}
	a.nodeStatus.Store(body.NodeID, &NodeStatus{
		NodeID:    body.NodeID,
		ConnCount: body.ConnCount,
		LastSeen:  time.Now().Unix(),
	})
	writeJ(w, map[string]bool{"ok": true})
}

func (a *API) handleNodeTraffic(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID    int64  `json:"node_id"`
		ReportKey string `json:"report_key"`
		Entries   []struct {
			UserID   string `json:"user_id"`
			Upload   int64  `json:"upload"`
			Download int64  `json:"download"`
		} `json:"entries"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.ReportKey != a.config.NodeReportKey {
		writeE(w, 403, "invalid report key")
		return
	}
	for _, e := range body.Entries {
		uid, _ := strconv.ParseInt(e.UserID, 10, 64)
		a.store.AddTraffic(body.NodeID, uid, e.Upload, e.Download)
	}
	writeJ(w, map[string]bool{"ok": true})
}

func (a *API) handleDeployScript(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key != a.config.NodeReportKey {
		http.Error(w, "invalid key", 403)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	node := a.store.GetNode(id)
	if node == nil {
		http.Error(w, "node not found", 404)
		return
	}

	cmd := fmt.Sprintf(
		`curl -fsSL https://raw.githubusercontent.com/674542449/tunnel-pro/master/scripts/tunnel-node.sh | bash -s install --domain %s --psk %s --api-url %s --node-id %d --report-key %s`,
		node.Addr, node.PSK, a.config.SiteURL, node.ID, a.config.NodeReportKey,
	)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(cmd))
}

func (a *API) adminNodeStatus(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	nodes := a.store.AllNodes()
	now := time.Now().Unix()
	type nodeInfo struct {
		Node
		Online    bool  `json:"online"`
		ConnCount int   `json:"conn_count"`
		LastSeen  int64 `json:"last_seen"`
	}
	out := make([]nodeInfo, len(nodes))
	for i, n := range nodes {
		out[i] = nodeInfo{Node: n}
		if v, ok := a.nodeStatus.Load(n.ID); ok {
			st := v.(*NodeStatus)
			out[i].Online = (now - st.LastSeen) < 60
			out[i].ConnCount = st.ConnCount
			out[i].LastSeen = st.LastSeen
		}
	}
	writeJ(w, map[string]any{"nodes": out})
}

func (a *API) adminDashboard(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	now := time.Now()
	users := a.store.AllUsers()
	orders := a.store.AllOrders()
	traffic := a.store.GetUserTraffic()

	regByDay := map[string]int{}
	revByDay := map[string]float64{}
	trafficByDay := map[string][2]int64{}
	for i := 29; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		regByDay[d] = 0
		revByDay[d] = 0
		trafficByDay[d] = [2]int64{}
	}
	cutoff := now.AddDate(0, 0, -30).Unix()

	for _, u := range users {
		d := time.Unix(u.CreatedAt, 0).Format("2006-01-02")
		if _, ok := regByDay[d]; ok {
			regByDay[d]++
		}
	}
	for _, o := range orders {
		if o.Status == "paid" && o.PaidAt > cutoff {
			d := time.Unix(o.PaidAt, 0).Format("2006-01-02")
			if _, ok := revByDay[d]; ok {
				revByDay[d] += o.Amount
			}
		}
	}
	cutoffDate := now.AddDate(0, 0, -30).Format("2006-01-02")
	for _, t := range traffic {
		if t.Date >= cutoffDate {
			if v, ok := trafficByDay[t.Date]; ok {
				v[0] += t.Upload
				v[1] += t.Download
				trafficByDay[t.Date] = v
			}
		}
	}

	type dayData struct {
		Date     string  `json:"date"`
		Reg      int     `json:"reg"`
		Revenue  float64 `json:"revenue"`
		Upload   int64   `json:"upload"`
		Download int64   `json:"download"`
	}
	var days []dayData
	for i := 29; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		td := trafficByDay[d]
		days = append(days, dayData{Date: d, Reg: regByDay[d], Revenue: revByDay[d], Upload: td[0], Download: td[1]})
	}

	onlineNodes := 0
	a.nodeStatus.Range(func(k, v any) bool {
		st := v.(*NodeStatus)
		if (now.Unix() - st.LastSeen) < 60 {
			onlineNodes++
		}
		return true
	})

	var todayTraffic int64
	today := now.Format("2006-01-02")
	for _, t := range traffic {
		if t.Date == today {
			todayTraffic += t.Upload + t.Download
		}
	}

	writeJ(w, map[string]any{
		"days":          days,
		"online_nodes":  onlineNodes,
		"today_traffic": todayTraffic,
	})
}

func (a *API) adminUserTraffic(w http.ResponseWriter, r *http.Request) {
	if !a.adminGuard(w, r) {
		return
	}
	traffic := a.store.GetUserTraffic()
	type userSum struct {
		UserID   int64  `json:"user_id"`
		Email    string `json:"email"`
		Upload   int64  `json:"upload"`
		Download int64  `json:"download"`
	}
	sums := map[int64]*userSum{}
	for _, t := range traffic {
		s, ok := sums[t.UserID]
		if !ok {
			s = &userSum{UserID: t.UserID}
			sums[t.UserID] = s
		}
		s.Upload += t.Upload
		s.Download += t.Download
	}
	users := a.store.AllUsers()
	emailMap := map[int64]string{}
	for _, u := range users {
		emailMap[u.ID] = u.Email
	}
	var out []userSum
	for _, s := range sums {
		s.Email = emailMap[s.UserID]
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Upload+out[i].Download > out[j].Upload+out[j].Download })
	writeJ(w, map[string]any{"traffic": out})
}

// -------- admin panel --------

func (a *API) serveAdmin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(adminHTML))
}

// -------- main --------

func main() {
	cfgPath := flag.String("c", "api.json", "config file")
	flag.Parse()

	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}
	var cfg Config
	json.Unmarshal(data, &cfg)
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8081"
	}
	if cfg.TrialHours == 0 {
		cfg.TrialHours = 1
	}
	if cfg.AdminKey == "" {
		buf := make([]byte, 16)
		rand.Read(buf)
		cfg.AdminKey = hex.EncodeToString(buf)
		log.Printf("Generated admin key: %s", cfg.AdminKey)
	}

	dir := "."
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}

	store := NewStore(filepath.Join(dir, "data.json"))
	store.PruneTraffic(90)

	api := &API{store: store, config: cfg, cfgPath: *cfgPath}

	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/register", api.handleRegister)
	mux.HandleFunc("POST /api/login", api.handleLogin)
	mux.HandleFunc("POST /api/guest", api.handleGuest)
	mux.HandleFunc("POST /api/trial", api.handleTrial)
	mux.HandleFunc("GET /api/me", api.handleMe)
	mux.HandleFunc("GET /api/nodes", api.handleNodes)

	mux.HandleFunc("POST /api/buy", api.handleBuy)
	mux.HandleFunc("GET /api/order", api.handleOrderStatus)
	mux.HandleFunc("GET /api/orders", api.handleMyOrders)
	mux.HandleFunc("GET /api/my-traffic", api.handleMyTraffic)
	mux.HandleFunc("POST /api/change-password", api.handleChangePassword)
	mux.HandleFunc("GET /api/version", api.handleVersion)
	mux.HandleFunc("/api/pay/notify", api.handlePayNotify)
	mux.HandleFunc("GET /api/pay/return", api.handlePayReturn)

	mux.HandleFunc("POST /api/node/heartbeat", api.handleNodeHeartbeat)
	mux.HandleFunc("POST /api/node/traffic", api.handleNodeTraffic)
	mux.HandleFunc("GET /api/node/deploy-script", api.handleDeployScript)

	mux.HandleFunc("POST /api/admin/login", api.handleAdminLogin)
	mux.HandleFunc("GET /api/admin/stats", api.adminStats)
	mux.HandleFunc("GET /api/admin/node-status", api.adminNodeStatus)
	mux.HandleFunc("GET /api/admin/dashboard", api.adminDashboard)
	mux.HandleFunc("GET /api/admin/user-traffic", api.adminUserTraffic)
	mux.HandleFunc("GET /api/admin/users", api.adminUsers)
	mux.HandleFunc("POST /api/admin/extend", api.adminExtend)
	mux.HandleFunc("DELETE /api/admin/users", api.adminDeleteUser)
	mux.HandleFunc("POST /api/admin/toggle-user", api.adminToggleUser)
	mux.HandleFunc("POST /api/admin/reset-password", api.adminResetPassword)
	mux.HandleFunc("/api/admin/nodes", api.adminNodes)
	mux.HandleFunc("GET /api/admin/orders", api.adminOrders)
	mux.HandleFunc("/api/admin/plans", api.adminPlans)
	mux.HandleFunc("/api/admin/settings", api.adminSettings)

	mux.HandleFunc("GET /admin", api.serveAdmin)
	mux.HandleFunc("GET /admin/", api.serveAdmin)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("API server: http://%s", cfg.Listen)
	log.Printf("Admin key:  %s", cfg.AdminKey)
	log.Fatal(http.Serve(ln, mux))
}

// -------- admin HTML --------

const adminHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Tunnel Admin</title>
<script src="https://cdnjs.cloudflare.com/ajax/libs/Chart.js/4.4.1/chart.umd.min.js"></script>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:'Inter',system-ui,-apple-system,sans-serif;background:#f5f0e8;color:#2d2b27;min-height:100vh}
.layout{display:flex;min-height:100vh}
.sidebar{width:220px;background:#1a1a2e;color:#c4b5a0;display:flex;flex-direction:column;position:fixed;top:0;left:0;bottom:0;z-index:100}
.sidebar-brand{padding:24px 20px;display:flex;align-items:center;gap:10px;border-bottom:1px solid #2a2a45}
.sidebar-brand h1{font-size:16px;font-weight:600;color:#f5f0e8}
.sidebar-brand .dot{width:10px;height:10px;border-radius:50%;background:#da7756}
.sidebar-nav{flex:1;padding:12px 8px}
.nav-item{display:flex;align-items:center;gap:10px;padding:10px 14px;border-radius:8px;cursor:pointer;font-size:14px;color:#8b8077;transition:all .15s;margin-bottom:2px;border:none;background:none;width:100%;text-align:left;font-family:inherit}
.nav-item:hover{background:#252540;color:#e8dfd3}
.nav-item.active{background:#da775620;color:#da7756;font-weight:500}
.nav-item svg{width:18px;height:18px;flex-shrink:0}
.sidebar-footer{padding:16px 20px;border-top:1px solid #2a2a45}
.sidebar-footer button{background:none;border:1px solid #3a3a55;color:#8b8077;padding:8px 0;border-radius:8px;width:100%;cursor:pointer;font-size:13px;font-family:inherit;transition:all .15s}
.sidebar-footer button:hover{border-color:#da7756;color:#da7756}
.main{flex:1;margin-left:220px;padding:28px 32px;min-height:100vh}
.main-header{margin-bottom:24px}
.main-header h2{font-size:22px;font-weight:600;color:#2d2b27}
.main-header p{font-size:13px;color:#8b8077;margin-top:4px}
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:14px;margin-bottom:24px}
.stat{background:#fff;border-radius:12px;padding:18px;border:1px solid #e8e0d4;box-shadow:0 1px 3px rgba(0,0,0,.04)}
.stat-num{font-size:28px;font-weight:700;color:#da7756}
.stat-label{font-size:12px;color:#8b8077;margin-top:4px}
.panel{display:none}
.panel.active{display:block}
.card{background:#fff;border-radius:12px;border:1px solid #e8e0d4;box-shadow:0 1px 3px rgba(0,0,0,.04);overflow:hidden}
table{width:100%;border-collapse:collapse}
th,td{padding:11px 16px;text-align:left;font-size:13px;border-bottom:1px solid #f0e8dc}
th{background:#faf6f0;color:#8b8077;font-weight:500;font-size:12px;text-transform:uppercase;letter-spacing:.5px}
tr:last-child td{border:none}
tr:hover td{background:#fdf9f3}
.badge{display:inline-block;padding:3px 10px;border-radius:20px;font-size:11px;font-weight:600}
.badge-ok{background:#dcfce7;color:#16a34a}
.badge-exp{background:#fee2e2;color:#dc2626}
.btn{padding:7px 16px;border:none;border-radius:8px;cursor:pointer;font-size:12px;font-family:inherit;font-weight:500;transition:all .15s}
.btn-primary{background:#da7756;color:#fff}
.btn-primary:hover{background:#c4623e}
.btn-sm{background:#da7756;color:#fff}
.btn-sm:hover{background:#c4623e}
.btn-danger{background:#fef2f2;color:#dc2626;border:1px solid #fecaca}
.btn-danger:hover{background:#fee2e2}
.btn-ghost{background:#f5f0e8;color:#6b6560;border:1px solid #e8e0d4}
.btn-ghost:hover{border-color:#da7756;color:#da7756}
.btn-success{background:#dcfce7;color:#16a34a;border:1px solid #bbf7d0}
.btn-success:hover{background:#bbf7d0}
.toolbar{display:flex;gap:10px;margin-bottom:16px;align-items:center}
.toolbar input{background:#fff;border:1px solid #e8e0d4;color:#2d2b27;padding:9px 14px;border-radius:8px;font-size:13px;font-family:inherit;outline:none;min-width:200px}
.toolbar input:focus{border-color:#da7756;box-shadow:0 0 0 3px #da775615}
.form-row{display:flex;gap:10px;margin-bottom:12px;align-items:center;flex-wrap:wrap}
.form-row label{width:80px;font-size:13px;color:#8b8077;flex-shrink:0;font-weight:500}
.form-row input,.form-row select{flex:1;min-width:120px;background:#fff;border:1px solid #e8e0d4;color:#2d2b27;padding:9px 14px;border-radius:8px;font-size:13px;font-family:inherit;outline:none}
.form-row input:focus,.form-row select:focus{border-color:#da7756;box-shadow:0 0 0 3px #da775615}
.modal{display:none;position:fixed;inset:0;background:rgba(0,0,0,.4);backdrop-filter:blur(4px);z-index:1000;justify-content:center;align-items:center}
.modal.show{display:flex}
.modal-box{background:#fff;border-radius:16px;padding:28px;width:92%;max-width:500px;box-shadow:0 20px 60px rgba(0,0,0,.15)}
.modal-box h3{margin-bottom:20px;font-size:17px;font-weight:600;color:#2d2b27}
.modal-footer{display:flex;gap:10px;margin-top:20px;justify-content:flex-end}
.toast{position:fixed;top:20px;left:50%;transform:translateX(-50%) translateY(-80px);background:#fff;border:1px solid #e8e0d4;color:#2d2b27;padding:10px 24px;border-radius:12px;font-size:14px;transition:transform .3s;z-index:2000;box-shadow:0 4px 12px rgba(0,0,0,.1)}
.toast.show{transform:translateX(-50%) translateY(0)}
.toast.ok{border-color:#16a34a;color:#16a34a}
.toast.err{border-color:#dc2626;color:#dc2626}
.empty{text-align:center;padding:48px;color:#8b8077;font-size:14px}
.login-wrap{display:flex;justify-content:center;align-items:center;min-height:100vh;background:#f5f0e8}
.login-box{background:#fff;border:1px solid #e8e0d4;border-radius:20px;padding:40px;width:92%;max-width:380px;text-align:center;box-shadow:0 4px 24px rgba(0,0,0,.06)}
.login-box h2{font-size:20px;font-weight:600;margin-bottom:6px;color:#2d2b27}
.login-box p{color:#8b8077;font-size:13px;margin-bottom:28px}
.login-box input{width:100%;background:#faf6f0;border:1px solid #e8e0d4;color:#2d2b27;padding:12px 16px;border-radius:10px;font-size:14px;outline:none;margin-bottom:16px;text-align:center;letter-spacing:1px;font-family:inherit}
.login-box input:focus{border-color:#da7756;box-shadow:0 0 0 3px #da775615}
.login-box .btn{width:100%;padding:12px;font-size:14px}
.login-err{color:#dc2626;font-size:13px;margin-bottom:12px;display:none}
.settings-card{background:#fff;border:1px solid #e8e0d4;border-radius:12px;padding:24px;margin-bottom:16px;box-shadow:0 1px 3px rgba(0,0,0,.04)}
.settings-card h3{font-size:15px;margin-bottom:18px;color:#da7756;font-weight:600}
.chart-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:20px}
@media(max-width:900px){.chart-grid{grid-template-columns:1fr}.sidebar{width:60px}.sidebar-brand h1,.nav-item span{display:none}.sidebar-brand{padding:16px 12px;justify-content:center}.nav-item{padding:10px;justify-content:center}.main{margin-left:60px;padding:20px 16px}}
.chart-card{background:#fff;border:1px solid #e8e0d4;border-radius:12px;padding:18px;box-shadow:0 1px 3px rgba(0,0,0,.04)}
.chart-card h4{font-size:13px;color:#8b8077;margin-bottom:12px;font-weight:500}
.chart-card canvas{width:100%!important;max-height:220px}
.online-dot{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:6px}
.online-dot.on{background:#16a34a;box-shadow:0 0 6px #16a34a}
.online-dot.off{background:#dc2626}
</style>
</head>
<body>

<div id="loginPage" class="login-wrap" style="display:none">
  <div class="login-box">
    <div style="width:48px;height:48px;border-radius:12px;background:#da7756;display:flex;align-items:center;justify-content:center;margin:0 auto 20px">
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 12h10M14 6l6 6-6 6"/></svg>
    </div>
    <h2>Tunnel 管理后台</h2>
    <p>输入管理员密钥继续</p>
    <div class="login-err" id="loginErr"></div>
    <input type="password" id="loginKey" placeholder="管理员密钥" onkeydown="if(event.key==='Enter')doLogin()">
    <button class="btn btn-primary" onclick="doLogin()">登录</button>
  </div>
</div>

<div id="mainPage" class="layout" style="display:none">
  <aside class="sidebar">
    <div class="sidebar-brand">
      <div class="dot"></div>
      <h1>Tunnel</h1>
    </div>
    <nav class="sidebar-nav">
      <button class="nav-item active" onclick="showTab('dash',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
        <span>仪表盘</span>
      </button>
      <button class="nav-item" onclick="showTab('users',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/></svg>
        <span>用户</span>
      </button>
      <button class="nav-item" onclick="showTab('nodes',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z"/></svg>
        <span>节点</span>
      </button>
      <button class="nav-item" onclick="showTab('plans',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z"/></svg>
        <span>套餐</span>
      </button>
      <button class="nav-item" onclick="showTab('orders',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>
        <span>订单</span>
      </button>
      <button class="nav-item" onclick="showTab('traffic',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
        <span>流量</span>
      </button>
      <button class="nav-item" onclick="showTab('settings',this)">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>
        <span>设置</span>
      </button>
    </nav>
    <div class="sidebar-footer">
      <button onclick="doLogout()">退出登录</button>
    </div>
  </aside>

  <div class="main">
    <div class="stats" id="stats"></div>

    <div class="panel active" id="p-dash">
      <div class="chart-grid">
        <div class="chart-card"><h4>30天注册趋势</h4><canvas id="chartReg"></canvas></div>
        <div class="chart-card"><h4>30天收入趋势</h4><canvas id="chartRev"></canvas></div>
        <div class="chart-card"><h4>30天流量趋势</h4><canvas id="chartTraffic"></canvas></div>
        <div class="chart-card"><h4>节点连接数</h4><canvas id="chartNodes"></canvas></div>
      </div>
    </div>

    <div class="panel" id="p-users">
      <div class="toolbar">
        <input id="userSearch" placeholder="搜索邮箱 / ID..." oninput="renderUsers()">
        <span style="flex:1"></span>
        <span id="userCount" style="font-size:13px;color:#8b8077"></span>
      </div>
      <div class="card" id="userTable"></div>
    </div>

    <div class="panel" id="p-nodes">
      <div class="toolbar">
        <button class="btn btn-primary" onclick="showAddNode()">+ 添加节点</button>
      </div>
      <div class="card" id="nodeTable"></div>
    </div>

    <div class="panel" id="p-plans">
      <div class="toolbar">
        <button class="btn btn-primary" onclick="showAddPlan()">+ 添加套餐</button>
      </div>
      <div class="card" id="planTable"></div>
    </div>

    <div class="panel" id="p-orders">
      <div class="card" id="orderTable"></div>
    </div>

    <div class="panel" id="p-traffic">
      <div class="toolbar">
        <input id="trafficSearch" placeholder="搜索邮箱 / ID..." oninput="renderTraffic()">
        <span style="flex:1"></span>
        <span id="trafficTotal" style="font-size:13px;color:#8b8077"></span>
      </div>
      <div class="card" id="trafficTable"></div>
    </div>

    <div class="panel" id="p-settings">
      <div class="settings-card">
        <h3>支付配置 (EPay)</h3>
        <div class="form-row"><label>接口地址</label><input id="epayURL" placeholder="https://pay.example.com"></div>
        <div class="form-row"><label>商户 PID</label><input id="epayPID" placeholder="商户 PID"></div>
        <div class="form-row"><label>商户密钥</label><input id="epayKey" placeholder="商户密钥"></div>
        <div style="margin-top:16px;display:flex;gap:10px;justify-content:flex-end">
          <button class="btn btn-primary" onclick="saveEPay()">保存</button>
        </div>
      </div>
      <div class="settings-card">
        <h3>客户端版本</h3>
        <div class="form-row"><label>版本号</label><input id="verNum" placeholder="如 1.0.1"></div>
        <div class="form-row"><label>下载地址</label><input id="verURL" placeholder="https://example.com/client.exe"></div>
        <div class="form-row"><label>更新日志</label><input id="verLog" placeholder="可选"></div>
        <div style="margin-top:16px;display:flex;gap:10px;justify-content:flex-end">
          <button class="btn btn-primary" onclick="saveVersion()">保存</button>
        </div>
      </div>
    </div>
  </div>
</div>

<div class="modal" id="extendModal" onclick="if(event.target===this)this.classList.remove('show')">
  <div class="modal-box">
    <h3>续期</h3>
    <div class="form-row"><label>用户</label><input id="extUser" disabled></div>
    <div class="form-row"><label>天数</label><input id="extDays" type="number" value="30" min="1"></div>
    <div class="modal-footer">
      <button class="btn btn-ghost" onclick="document.getElementById('extendModal').classList.remove('show')">取消</button>
      <button class="btn btn-primary" onclick="doExtend()">确认</button>
    </div>
  </div>
</div>

<div class="modal" id="nodeModal" onclick="if(event.target===this)this.classList.remove('show')">
  <div class="modal-box">
    <h3 id="nodeModalTitle">添加节点</h3>
    <input type="hidden" id="nodeEditId">
    <div class="form-row"><label>名称</label><input id="nName" placeholder="显示名称"></div>
    <div class="form-row"><label>域名</label><input id="nAddr" placeholder="example.com"></div>
    <div class="form-row"><label>IP</label><input id="nIP" placeholder="可选"></div>
    <div class="form-row"><label>PSK</label><input id="nPSK" placeholder="预共享密钥"></div>
    <div class="form-row"><label>地区</label><input id="nRegion" placeholder="如 US, JP, KR"></div>
    <div class="form-row"><label>排序</label><input id="nSort" type="number" value="0"></div>
    <div class="modal-footer">
      <button class="btn btn-ghost" onclick="document.getElementById('nodeModal').classList.remove('show')">取消</button>
      <button class="btn btn-primary" onclick="doSaveNode()">保存</button>
    </div>
  </div>
</div>

<div class="modal" id="deployModal" onclick="if(event.target===this)this.classList.remove('show')">
  <div class="modal-box" style="max-width:620px">
    <h3>部署命令</h3>
    <div class="form-row"><label>节点</label><span id="deployNodeName" style="color:#2d2b27;font-weight:500"></span></div>
    <div class="form-row"><label>域名</label><span id="deployDomain" style="color:#2d2b27"></span></div>
    <p style="font-size:12px;color:#8b8077;margin:10px 0">在目标服务器上以 root 身份运行，自动从 GitHub 下载并安装：</p>
    <textarea id="deployCmd" readonly rows="4" style="width:100%;background:#1a1a2e;color:#a5f3fc;border:1px solid #e8e0d4;border-radius:8px;padding:12px;font-family:'SF Mono',monospace;font-size:12px;resize:none;word-break:break-all"></textarea>
    <p style="font-size:11px;color:#8b8077;margin:6px 0">安装后运行 <code style="background:#f5f0e8;padding:2px 8px;border-radius:4px;color:#da7756;font-size:11px">tunnel-node.sh menu</code> 进入管理菜单</p>
    <div class="modal-footer">
      <button class="btn btn-ghost" onclick="document.getElementById('deployModal').classList.remove('show')">关闭</button>
      <button class="btn btn-success" onclick="copyDeployCmd()">复制命令</button>
    </div>
  </div>
</div>

<div class="modal" id="resetPwdModal" onclick="if(event.target===this)this.classList.remove('show')">
  <div class="modal-box">
    <h3>重置密码</h3>
    <div class="form-row"><label>用户</label><input id="rpUser" disabled></div>
    <div class="form-row"><label>新密码</label><input id="rpPwd" type="text" placeholder="最少6位"></div>
    <div class="modal-footer">
      <button class="btn btn-ghost" onclick="document.getElementById('resetPwdModal').classList.remove('show')">取消</button>
      <button class="btn btn-primary" onclick="doResetPwd()">确认重置</button>
    </div>
  </div>
</div>

<div class="modal" id="planModal" onclick="if(event.target===this)this.classList.remove('show')">
  <div class="modal-box">
    <h3 id="planModalTitle">添加套餐</h3>
    <input type="hidden" id="planEditId">
    <div class="form-row"><label>名称</label><input id="pName" placeholder="如 月付套餐"></div>
    <div class="form-row"><label>天数</label><input id="pDays" type="number" placeholder="30" min="1"></div>
    <div class="form-row"><label>价格</label><input id="pPrice" type="number" placeholder="15" min="0.01" step="0.01"></div>
    <div class="form-row"><label>状态</label><select id="pEnabled" style="flex:1;background:#fff;border:1px solid #e8e0d4;color:#2d2b27;padding:9px 14px;border-radius:8px;font-size:13px;font-family:inherit"><option value="1">启用</option><option value="0">停用</option></select></div>
    <div class="modal-footer">
      <button class="btn btn-ghost" onclick="document.getElementById('planModal').classList.remove('show')">取消</button>
      <button class="btn btn-primary" onclick="doSavePlan()">保存</button>
    </div>
  </div>
</div>

<div class="toast" id="toast"></div>

<script>
var TOKEN=localStorage.getItem("admin_token")||"";
var users=[],nodes=[],orders=[],plans=[];

function H(t,d){
  var opts=Object.assign({headers:{"Authorization":"Bearer "+TOKEN,"Content-Type":"application/json"}},d||{});
  return fetch("/api/admin/"+t,opts).then(function(r){
    if(r.status===403||r.status===401){doLogout();throw new Error("unauthorized")}
    return r.json()
  })
}

function toast(m,t){var e=document.getElementById("toast");e.textContent=m;e.className="toast show "+(t||"");clearTimeout(toast.t);toast.t=setTimeout(function(){e.className="toast"},2500)}

function doLogin(){
  var key=document.getElementById("loginKey").value.trim();
  if(!key)return;
  var errEl=document.getElementById("loginErr");
  errEl.style.display="none";
  fetch("/api/admin/login",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({key:key})}).then(function(r){return r.json()}).then(function(d){
    if(d.error){errEl.textContent=d.error;errEl.style.display="block";return}
    TOKEN=d.token;
    localStorage.setItem("admin_token",TOKEN);
    showMain()
  }).catch(function(){errEl.textContent="网络错误";errEl.style.display="block"})
}

function doLogout(){
  TOKEN="";
  localStorage.removeItem("admin_token");
  document.getElementById("mainPage").style.display="none";
  document.getElementById("loginPage").style.display="flex";
  document.getElementById("loginKey").value="";
}

function showMain(){
  document.getElementById("loginPage").style.display="none";
  document.getElementById("mainPage").style.display="flex";
  loadStats();loadDashboard();loadUsers();loadNodes();loadPlans();loadOrders();loadSettings()
}

function showTab(name,btn){
  document.querySelectorAll(".panel").forEach(function(p){p.classList.remove("active")});
  document.querySelectorAll(".nav-item").forEach(function(t){t.classList.remove("active")});
  document.getElementById("p-"+name).classList.add("active");
  btn.classList.add("active");
  if(name==="dash")loadDashboard();
  if(name==="plans")loadPlans();
  if(name==="orders")loadOrders();
  if(name==="traffic")loadTraffic();
  if(name==="settings")loadSettings();
  if(name==="nodes")loadNodes();
}

function fmtTime(ts){if(!ts)return"-";var d=new Date(ts*1000);return d.getFullYear()+"-"+(d.getMonth()+1).toString().padStart(2,"0")+"-"+d.getDate().toString().padStart(2,"0")+" "+d.getHours().toString().padStart(2,"0")+":"+d.getMinutes().toString().padStart(2,"0")}

function fmtBytes(b){if(!b||b===0)return"0 B";var u=["B","KB","MB","GB","TB"];var i=Math.floor(Math.log(b)/Math.log(1024));return(b/Math.pow(1024,i)).toFixed(i>0?1:0)+" "+u[i]}

function loadStats(){
  H("stats").then(function(d){
    var h='<div class="stat"><div class="stat-num">'+d.total_users+'</div><div class="stat-label">总用户</div></div>'+
      '<div class="stat"><div class="stat-num">'+d.active_users+'</div><div class="stat-label">活跃用户</div></div>'+
      '<div class="stat"><div class="stat-num">'+d.total_orders+'</div><div class="stat-label">订单数</div></div>'+
      '<div class="stat"><div class="stat-num">'+d.total_nodes+'</div><div class="stat-label">节点数</div></div>';
    document.getElementById("stats").innerHTML=h;
    H("dashboard").then(function(dd){
      h+='<div class="stat"><div class="stat-num">'+dd.online_nodes+'</div><div class="stat-label">在线节点</div></div>';
      h+='<div class="stat"><div class="stat-num">'+fmtBytes(dd.today_traffic)+'</div><div class="stat-label">今日流量</div></div>';
      document.getElementById("stats").innerHTML=h
    }).catch(function(){})
  }).catch(function(){})
}

function loadUsers(){
  H("users").then(function(d){users=d.users||[];renderUsers()}).catch(function(){})
}

function renderUsers(){
  var q=document.getElementById("userSearch").value.toLowerCase();
  var f=users.filter(function(u){return !q||String(u.id).indexOf(q)>=0||(u.email||"").toLowerCase().indexOf(q)>=0||(u.machine_id||"").toLowerCase().indexOf(q)>=0});
  document.getElementById("userCount").textContent=f.length+"/"+users.length+" 用户";
  if(f.length===0){document.getElementById("userTable").innerHTML='<div class="empty">暂无用户</div>';return}
  var now=Math.floor(Date.now()/1000);
  var h='<table><tr><th>ID</th><th>邮箱</th><th>状态</th><th>到期时间</th><th>注册时间</th><th>操作</th></tr>';
  f.forEach(function(u){
    var label=u.email||(u.machine_id?"游客:"+u.machine_id.substring(0,8)+"...":"未知");
    var st=u.disabled?"已禁用":(u.expires_at>now?"活跃":"已过期");
    var cls=u.disabled?"badge-exp":(u.expires_at>now?"badge-ok":"badge-exp");
    h+="<tr><td>"+u.id+"</td><td>"+label+"</td>";
    h+='<td><span class="badge '+cls+'">'+st+"</span></td>";
    h+="<td>"+fmtTime(u.expires_at)+"</td><td>"+fmtTime(u.created_at)+"</td>";
    h+='<td style="white-space:nowrap">';
    h+='<button class="btn btn-sm" onclick="showExtend('+u.id+",'"+label.replace(/'/g,"")+"')\">续期</button> ";
    if(u.disabled){
      h+='<button class="btn btn-success" onclick="toggleUser('+u.id+',false)">启用</button> '
    }else{
      h+='<button class="btn btn-ghost" onclick="toggleUser('+u.id+',true)">禁用</button> '
    }
    if(u.email){h+='<button class="btn btn-ghost" onclick="showResetPwd('+u.id+",'"+label.replace(/'/g,"")+"')\">重置密码</button> "}
    h+='<button class="btn btn-danger" onclick="delUser('+u.id+')">删除</button>';
    h+="</td></tr>"
  });
  h+="</table>";
  document.getElementById("userTable").innerHTML=h
}

function showExtend(uid,label){
  document.getElementById("extUser").value=label+" (ID:"+uid+")";
  document.getElementById("extDays").value=30;
  document.getElementById("extendModal").dataset.uid=uid;
  document.getElementById("extendModal").classList.add("show")
}

function doExtend(){
  var uid=parseInt(document.getElementById("extendModal").dataset.uid);
  var days=parseInt(document.getElementById("extDays").value);
  H("extend",{method:"POST",body:JSON.stringify({user_id:uid,days:days})}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("续期成功","ok");loadUsers();loadStats()}
    document.getElementById("extendModal").classList.remove("show")
  }).catch(function(){})
}

function toggleUser(uid,disabled){
  var msg=disabled?"确定禁用此用户？":"确定启用此用户？";
  if(!confirm(msg))return;
  H("toggle-user",{method:"POST",body:JSON.stringify({user_id:uid,disabled:disabled})}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast(disabled?"已禁用":"已启用","ok");loadUsers()}
  }).catch(function(){})
}

function delUser(uid){
  if(!confirm("确定删除此用户？此操作不可撤销！"))return;
  H("users?id="+uid,{method:"DELETE"}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("已删除","ok");loadUsers();loadStats()}
  }).catch(function(){})
}

function showResetPwd(uid,label){
  document.getElementById("rpUser").value=label+" (ID:"+uid+")";
  document.getElementById("rpPwd").value="";
  document.getElementById("resetPwdModal").dataset.uid=uid;
  document.getElementById("resetPwdModal").classList.add("show")
}

function doResetPwd(){
  var uid=parseInt(document.getElementById("resetPwdModal").dataset.uid);
  var pwd=document.getElementById("rpPwd").value;
  if(pwd.length<6){toast("密码至少6位","err");return}
  H("reset-password",{method:"POST",body:JSON.stringify({user_id:uid,password:pwd})}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("密码已重置","ok")}
    document.getElementById("resetPwdModal").classList.remove("show")
  }).catch(function(){})
}

function loadNodes(){
  H("node-status").then(function(d){nodes=d.nodes||[];renderNodes()}).catch(function(){
    H("nodes").then(function(d){nodes=d.nodes||[];renderNodes()}).catch(function(){})
  })
}

function renderNodes(){
  if(nodes.length===0){document.getElementById("nodeTable").innerHTML='<div class="empty">暂无节点</div>';return}
  var h='<table><tr><th>ID</th><th>名称</th><th>域名</th><th>状态</th><th>连接数</th><th>地区</th><th>启用</th><th>操作</th></tr>';
  nodes.forEach(function(n){
    var online=n.online;
    var dot='<span class="online-dot '+(online?"on":"off")+'"></span>'+(online?"在线":"离线");
    h+="<tr><td>"+n.id+"</td><td>"+n.name+"</td><td style='color:#8b8077;font-size:12px'>"+n.addr+"</td>";
    h+="<td>"+dot+"</td><td>"+(n.conn_count||0)+"</td><td>"+(n.region||"-")+"</td>";
    h+='<td><span class="badge '+(n.enabled?"badge-ok":"badge-exp")+'">'+(n.enabled?"On":"Off")+"</span></td>";
    h+='<td style="white-space:nowrap"><button class="btn btn-sm" onclick="showEditNode('+n.id+')">编辑</button> ';
    h+='<button class="btn btn-success" onclick="showDeploy('+n.id+')">部署</button> ';
    h+='<button class="btn btn-danger" onclick="delNode('+n.id+')">删除</button></td></tr>'
  });
  h+="</table>";
  document.getElementById("nodeTable").innerHTML=h
}

function showAddNode(){
  document.getElementById("nodeModalTitle").textContent="添加节点";
  document.getElementById("nodeEditId").value="";
  ["nName","nAddr","nIP","nPSK","nRegion"].forEach(function(id){document.getElementById(id).value=""});
  document.getElementById("nSort").value="0";
  document.getElementById("nodeModal").classList.add("show")
}

function showEditNode(id){
  var n=nodes.find(function(x){return x.id===id});if(!n)return;
  document.getElementById("nodeModalTitle").textContent="编辑节点";
  document.getElementById("nodeEditId").value=id;
  document.getElementById("nName").value=n.name;
  document.getElementById("nAddr").value=n.addr;
  document.getElementById("nIP").value=n.ip||"";
  document.getElementById("nPSK").value=n.psk;
  document.getElementById("nRegion").value=n.region||"";
  document.getElementById("nSort").value=n.sort||0;
  document.getElementById("nodeModal").classList.add("show")
}

function doSaveNode(){
  var editId=document.getElementById("nodeEditId").value;
  var data={name:document.getElementById("nName").value,addr:document.getElementById("nAddr").value,ip:document.getElementById("nIP").value,psk:document.getElementById("nPSK").value,region:document.getElementById("nRegion").value,sort:parseInt(document.getElementById("nSort").value)||0,enabled:true};
  if(editId){
    data.id=parseInt(editId);
    H("nodes",{method:"PUT",body:JSON.stringify(data)}).then(function(d){
      if(d.error){toast(d.error,"err")}else{toast("已更新","ok");loadNodes()}
      document.getElementById("nodeModal").classList.remove("show")
    }).catch(function(){})
  }else{
    H("nodes",{method:"POST",body:JSON.stringify(data)}).then(function(d){
      if(d.error){toast(d.error,"err")}else{toast("已添加","ok");loadNodes();loadStats();if(d.node)showDeploy(d.node.id)}
      document.getElementById("nodeModal").classList.remove("show")
    }).catch(function(){})
  }
}

function delNode(id){
  if(!confirm("确定删除此节点？"))return;
  H("nodes?id="+id,{method:"DELETE"}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("已删除","ok");loadNodes();loadStats()}
  }).catch(function(){})
}

function showDeploy(id){
  var n=nodes.find(function(x){return x.id===id});
  if(!n){toast("请先刷新节点列表","err");return}
  H("settings").then(function(s){
    var rk=s.node_report_key||"YOUR_REPORT_KEY";
    var origin=location.origin;
    fetch(origin+"/api/node/deploy-script?id="+id+"&key="+rk).then(function(r){return r.text()}).then(function(cmd){
      document.getElementById("deployNodeName").textContent=n.name+" (ID:"+n.id+")";
      document.getElementById("deployDomain").textContent=n.addr;
      document.getElementById("deployCmd").value=cmd;
      document.getElementById("deployModal").classList.add("show")
    })
  }).catch(function(){})
}

function copyDeployCmd(){
  var t=document.getElementById("deployCmd");
  t.select();document.execCommand("copy");
  toast("已复制","ok")
}

function loadOrders(){
  H("orders").then(function(d){orders=d.orders||[];renderOrders()}).catch(function(){})
}

function renderOrders(){
  if(orders.length===0){document.getElementById("orderTable").innerHTML='<div class="empty">暂无订单</div>';return}
  var h='<table><tr><th>ID</th><th>用户</th><th>套餐</th><th>金额</th><th>支付方式</th><th>状态</th><th>时间</th></tr>';
  orders.forEach(function(o){
    h+="<tr><td>"+o.id+"</td><td>"+(o.user_email||"ID:"+o.user_id)+"</td><td>"+o.plan+"</td>";
    h+="<td>"+(o.amount>0?"¥"+o.amount:"-")+"</td><td>"+(o.method||"-")+"</td>";
    h+='<td><span class="badge badge-ok">'+o.status+"</span></td>";
    h+="<td>"+fmtTime(o.created_at)+"</td></tr>"
  });
  h+="</table>";
  document.getElementById("orderTable").innerHTML=h
}

function loadPlans(){
  H("plans").then(function(d){plans=d.plans||[];renderPlans()}).catch(function(){})
}

function renderPlans(){
  if(plans.length===0){document.getElementById("planTable").innerHTML='<div class="empty">暂无套餐</div>';return}
  var h='<table><tr><th>ID</th><th>名称</th><th>天数</th><th>价格</th><th>状态</th><th>操作</th></tr>';
  plans.forEach(function(p){
    h+="<tr><td>"+p.id+"</td><td>"+p.name+"</td><td>"+p.days+"d</td><td>¥"+p.price+"</td>";
    h+='<td><span class="badge '+(p.enabled?"badge-ok":"badge-exp")+'">'+(p.enabled?"On":"Off")+"</span></td>";
    h+='<td><button class="btn btn-sm" onclick="showEditPlan('+p.id+')">编辑</button> ';
    h+='<button class="btn btn-danger" onclick="delPlan('+p.id+')">删除</button></td></tr>'
  });
  h+="</table>";
  document.getElementById("planTable").innerHTML=h
}

function showAddPlan(){
  document.getElementById("planModalTitle").textContent="添加套餐";
  document.getElementById("planEditId").value="";
  document.getElementById("pName").value="";
  document.getElementById("pDays").value="";
  document.getElementById("pPrice").value="";
  document.getElementById("pEnabled").value="1";
  document.getElementById("planModal").classList.add("show")
}

function showEditPlan(id){
  var p=plans.find(function(x){return x.id===id});if(!p)return;
  document.getElementById("planModalTitle").textContent="编辑套餐";
  document.getElementById("planEditId").value=id;
  document.getElementById("pName").value=p.name;
  document.getElementById("pDays").value=p.days;
  document.getElementById("pPrice").value=p.price;
  document.getElementById("pEnabled").value=p.enabled?"1":"0";
  document.getElementById("planModal").classList.add("show")
}

function doSavePlan(){
  var editId=document.getElementById("planEditId").value;
  var data={name:document.getElementById("pName").value.trim(),days:parseInt(document.getElementById("pDays").value)||0,price:parseFloat(document.getElementById("pPrice").value)||0,enabled:document.getElementById("pEnabled").value==="1"};
  if(!data.name||data.days<=0||data.price<=0){toast("请填写所有字段","err");return}
  if(editId){
    data.id=parseInt(editId);
    H("plans",{method:"PUT",body:JSON.stringify(data)}).then(function(d){
      if(d.error){toast(d.error,"err")}else{toast("已更新","ok");loadPlans()}
      document.getElementById("planModal").classList.remove("show")
    }).catch(function(){})
  }else{
    H("plans",{method:"POST",body:JSON.stringify(data)}).then(function(d){
      if(d.error){toast(d.error,"err")}else{toast("已添加","ok");loadPlans()}
      document.getElementById("planModal").classList.remove("show")
    }).catch(function(){})
  }
}

function delPlan(id){
  if(!confirm("确定删除此套餐？"))return;
  H("plans?id="+id,{method:"DELETE"}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("已删除","ok");loadPlans()}
  }).catch(function(){})
}

function loadSettings(){
  H("settings").then(function(d){
    if(d.epay){
      document.getElementById("epayURL").value=d.epay.url||"";
      document.getElementById("epayPID").value=d.epay.pid||"";
      document.getElementById("epayKey").value=d.epay.key||""
    }
    if(d.version){
      document.getElementById("verNum").value=d.version.version||"";
      document.getElementById("verURL").value=d.version.download_url||"";
      document.getElementById("verLog").value=d.version.changelog||""
    }
  }).catch(function(){})
}

function saveEPay(){
  var data={epay:{url:document.getElementById("epayURL").value.trim(),pid:document.getElementById("epayPID").value.trim(),key:document.getElementById("epayKey").value.trim()}};
  H("settings",{method:"PUT",body:JSON.stringify(data)}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("已保存","ok")}
  }).catch(function(){})
}

function saveVersion(){
  var data={version:{version:document.getElementById("verNum").value.trim(),download_url:document.getElementById("verURL").value.trim(),changelog:document.getElementById("verLog").value.trim()}};
  H("settings",{method:"PUT",body:JSON.stringify(data)}).then(function(d){
    if(d.error){toast(d.error,"err")}else{toast("已保存","ok")}
  }).catch(function(){})
}

var charts={};
function loadDashboard(){
  H("dashboard").then(function(d){
    var days=d.days||[];
    var labels=days.map(function(x){return x.date.substring(5)});
    var chartOpts={responsive:true,plugins:{legend:{display:false}},scales:{x:{ticks:{color:"#8b8077",maxTicksLimit:10},grid:{color:"#e8e0d4"}},y:{ticks:{color:"#8b8077"},grid:{color:"#e8e0d4"}}}};

    if(charts.reg)charts.reg.destroy();
    charts.reg=new Chart(document.getElementById("chartReg"),{type:"line",data:{labels:labels,datasets:[{data:days.map(function(x){return x.reg}),borderColor:"#da7756",backgroundColor:"#da775615",fill:true,tension:.3}]},options:chartOpts});

    if(charts.rev)charts.rev.destroy();
    charts.rev=new Chart(document.getElementById("chartRev"),{type:"bar",data:{labels:labels,datasets:[{data:days.map(function(x){return x.revenue}),backgroundColor:"#16a34a40",borderColor:"#16a34a",borderWidth:1}]},options:chartOpts});

    if(charts.traffic)charts.traffic.destroy();
    charts.traffic=new Chart(document.getElementById("chartTraffic"),{type:"line",data:{labels:labels,datasets:[{label:"Upload",data:days.map(function(x){return x.upload/1048576}),borderColor:"#f59e0b",tension:.3},{label:"Download",data:days.map(function(x){return x.download/1048576}),borderColor:"#3b82f6",tension:.3}]},options:Object.assign({},chartOpts,{plugins:{legend:{display:true,labels:{color:"#8b8077"}}}})});

    H("node-status").then(function(nd){
      var ns=nd.nodes||[];
      var onNodes=ns.filter(function(n){return n.online});
      if(charts.nodes)charts.nodes.destroy();
      if(onNodes.length>0){
        charts.nodes=new Chart(document.getElementById("chartNodes"),{type:"doughnut",data:{labels:onNodes.map(function(n){return n.name}),datasets:[{data:onNodes.map(function(n){return n.conn_count||0}),backgroundColor:["#da7756","#16a34a","#f59e0b","#dc2626","#8b5cf6","#06b6d4"]}]},options:{responsive:true,plugins:{legend:{position:"bottom",labels:{color:"#8b8077"}}}}})
      }else{
        document.getElementById("chartNodes").parentElement.innerHTML="<h4>节点连接数</h4><div class='empty'>暂无在线节点</div>"
      }
    }).catch(function(){})
  }).catch(function(){})
}

var trafficData=[];
function loadTraffic(){
  H("user-traffic").then(function(d){trafficData=d.traffic||[];renderTraffic()}).catch(function(){})
}
function renderTraffic(){
  var q=document.getElementById("trafficSearch").value.toLowerCase();
  var f=trafficData.filter(function(t){return !q||String(t.user_id).indexOf(q)>=0||(t.email||"").toLowerCase().indexOf(q)>=0});
  var totalUp=0,totalDown=0;
  f.forEach(function(t){totalUp+=t.upload;totalDown+=t.download});
  document.getElementById("trafficTotal").textContent="上传 "+fmtBytes(totalUp)+"  下载 "+fmtBytes(totalDown);
  if(f.length===0){document.getElementById("trafficTable").innerHTML='<div class="empty">暂无流量数据</div>';return}
  var h='<table><tr><th>用户ID</th><th>邮箱</th><th>上传</th><th>下载</th><th>合计</th></tr>';
  f.forEach(function(t){
    h+="<tr><td>"+t.user_id+"</td><td>"+(t.email||"游客")+"</td>";
    h+="<td>"+fmtBytes(t.upload)+"</td><td>"+fmtBytes(t.download)+"</td>";
    h+="<td>"+fmtBytes(t.upload+t.download)+"</td></tr>"
  });
  h+="</table>";
  document.getElementById("trafficTable").innerHTML=h
}

if(TOKEN){
  H("stats").then(function(){showMain()}).catch(function(){doLogout()})
}else{
  document.getElementById("loginPage").style.display="flex"
}
</script>
</body>
</html>`
