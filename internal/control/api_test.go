package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*API, *httptest.Server, string) {
	t.Helper()
	c := Config{Listen: "127.0.0.1:18081", PublicURL: "http://127.0.0.1:18081", DataFile: filepath.Join(testTempDir(t), "store.json"), AdminEmail: "admin@example.com", AdminPassword: "admin-password-for-test-only", Registration: true, TrialHours: 24}
	a, e := NewAPI(c)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(a)
	t.Cleanup(s.Close)
	data := request(t, s, "login", "", map[string]any{"email": c.AdminEmail, "password": c.AdminPassword}, 200)
	return a, s, data["token"].(string)
}
func request(t *testing.T, s *httptest.Server, path, token string, body any, status int) map[string]any {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	method := "GET"
	if body != nil {
		method = "POST"
	}
	r, _ := http.NewRequest(method, s.URL+"/api/"+path, bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	res, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("%s status %d, want %d", path, res.StatusCode, status)
	}
	var d map[string]any
	json.NewDecoder(res.Body).Decode(&d)
	return d
}
func TestAccountIsolationOrdersAndDurability(t *testing.T) {
	a, s, admin := setup(t)
	login := request(t, s, "register", "", map[string]any{"email": "user@example.com", "password": "user-password-at-least-12"}, 200)
	userToken := login["token"].(string)
	id := login["user"].(map[string]any)["id"].(string)
	request(t, s, "admin/users", userToken, nil, 403)
	request(t, s, "trial", userToken, map[string]any{}, 200)
	request(t, s, "trial", userToken, map[string]any{}, 409)
	state := a.Store.Snapshot()
	plan := state.Plans[0]
	order := request(t, s, "orders", userToken, map[string]any{"plan_id": plan.ID}, 201)
	oid := order["id"].(string)
	request(t, s, "admin/orders/"+oid+"/fulfil", admin, map[string]any{}, 200)
	after := *findUserPtr(a.Store.Snapshot(), id)
	request(t, s, "admin/orders/"+oid+"/fulfil", admin, map[string]any{}, 200)
	again := *findUserPtr(a.Store.Snapshot(), id)
	if after.ExpiresAt != again.ExpiresAt || after.Limit != again.Limit {
		t.Fatal("duplicate fulfilment changed entitlement")
	}
	res := request(t, s, "me", userToken, nil, 200)
	b, _ := json.Marshal(res)
	if bytes.Contains(b, []byte(after.PasswordHash)) || bytes.Contains(b, []byte(after.TunnelToken)) {
		t.Fatal("user secret leaked")
	}
	request(t, s, "admin/users/"+id+"/update", admin, map[string]any{"disabled": true}, 200)
	request(t, s, "me", userToken, nil, 401)
	reopened, e := OpenStore(a.Config)
	if e != nil || findUserPtr(reopened.Snapshot(), id) == nil {
		t.Fatal("durable store not restored", e)
	}
	original := a.Store.Snapshot()
	a.Store.path = filepath.Dir(a.Store.path)
	if a.Store.Update(func(d *State) error { d.Announcement = "must not commit"; return nil }) == nil {
		t.Fatal("failed disk write accepted")
	}
	if a.Store.Snapshot().Announcement != original.Announcement {
		t.Fatal("failed commit mutated memory")
	}
}

func TestTrialCannotReduceExistingEntitlement(t *testing.T) {
	a, s, admin := setup(t)
	before := a.Store.Snapshot().Users[0]
	request(t, s, "trial", admin, map[string]any{}, 409)
	if after := a.Store.Snapshot().Users[0]; after != before {
		t.Fatal("trial modified administrator entitlement")
	}
	login := request(t, s, "register", "", map[string]any{"email": "paid@example.com", "password": "paid-password-at-least-12"}, 200)
	token := login["token"].(string)
	id := login["user"].(map[string]any)["id"].(string)
	request(t, s, "admin/users/"+id+"/renew", admin, map[string]any{"days": 30, "traffic_bytes": int64(100 << 30), "devices": 3}, 200)
	paid := *findUserPtr(a.Store.Snapshot(), id)
	request(t, s, "trial", token, map[string]any{}, 409)
	if after := *findUserPtr(a.Store.Snapshot(), id); after != paid {
		t.Fatal("trial reduced paid entitlement")
	}
}
func findUserPtr(d State, id string) *User { return findUser(&d, id) }
func TestCookieCSRFAndMalformedStore(t *testing.T) {
	a, s, admin := setup(t)
	r, _ := http.NewRequest("POST", s.URL+"/api/logout", strings.NewReader("{}"))
	r.AddCookie(&http.Cookie{Name: "tunnelx_session", Value: admin})
	res, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("cookie mutation without CSRF accepted")
	}
	r, _ = http.NewRequest("POST", s.URL+"/api/logout", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+admin)
	r.Header.Set("Origin", "https://attacker.invalid")
	res, e = s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross-origin request accepted")
	}
	bad := []byte("{ corrupt")
	if e = os.WriteFile(a.Config.DataFile, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenStore(a.Config); e == nil {
		t.Fatal("corrupt store reset")
	}
	b, _ := os.ReadFile(a.Config.DataFile)
	if !bytes.Equal(b, bad) {
		t.Fatal("corrupt evidence overwritten")
	}
}
func TestNodeCumulativeReportsAreIdempotent(t *testing.T) {
	a, s, _ := setup(t)
	node := Node{ID: ID(), Name: "test", Enabled: true, AgentKey: Token()}
	user := User{ID: ID(), TunnelToken: Token(), Role: "user", ExpiresAt: time.Now().Add(time.Hour).Unix(), Devices: 1, Limit: 5000}
	a.Store.Update(func(d *State) error { d.Nodes = append(d.Nodes, node); d.Users = append(d.Users, user); return nil })
	boot := ID()
	call := func(up int64, status int) {
		b, _ := json.Marshal(SyncRequest{BootID: boot, Version: Version, Counters: []Counter{{UserID: user.ID, Upload: up, Download: 100}}})
		r, _ := http.NewRequest("POST", s.URL+"/api/node/sync", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+node.AgentKey)
		r.Header.Set("X-TunnelX-Node", node.ID)
		res, e := s.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("node status %d, want %d", res.StatusCode, status)
		}
	}
	call(200, 200)
	call(200, 200)
	if findUserPtr(a.Store.Snapshot(), user.ID).Upload != 200 {
		t.Fatal("duplicate traffic counted")
	}
	call(199, 409)
	if findUserPtr(a.Store.Snapshot(), user.ID).Upload != 200 {
		t.Fatal("regression changed traffic")
	}
	call(500, 200)
	u := findUserPtr(a.Store.Snapshot(), user.ID)
	if u.Upload != 500 || u.Download != 100 {
		t.Fatal("cumulative delta incorrect")
	}
}
