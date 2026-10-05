package control

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"tunnelx/internal/config"
	"tunnelx/internal/pki"
)

func TestEmptyLegacyCollectionsReturnArrays(t *testing.T) {
	a, s, admin := setup(t)
	for _, name := range []string{"orders", "plans", "audit"} {
		a.Store.Update(func(d *State) error { d.Orders = nil; d.Plans = nil; d.Audit = nil; return nil })
		r, _ := http.NewRequest("GET", s.URL+"/api/admin/"+name, nil)
		r.Header.Set("Authorization", "Bearer "+admin)
		res, e := s.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || string(bytes.TrimSpace(b)) != "[]" {
			t.Fatal("empty collection not an array", name)
		}
	}
	before, e := os.ReadFile(a.Config.DataFile)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = OpenStore(a.Config); e != nil {
		t.Fatal("legacy store not readable", e)
	}
	after, _ := os.ReadFile(a.Config.DataFile)
	if !bytes.Equal(before, after) {
		t.Fatal("opening legacy store unexpectedly rewrote disk")
	}
}

func TestAdminCRUDValidationAndProfileBundle(t *testing.T) {
	a, s, admin := setup(t)
	ca, _, key, e := pki.Certificate("edge.tunnelx.invalid")
	if e != nil {
		t.Fatal(e)
	}
	_, ech, e := pki.ECH("proxy.test")
	if e != nil {
		t.Fatal(e)
	}
	node := Node{Name: "first", Region: "KR", Enabled: true, CAPEM: string(ca), Client: config.Client{ServerName: "edge.tunnelx.invalid", ServerIP: "203.0.113.10", Port: 8443, ECHConfig: base64.StdEncoding.EncodeToString(ech)}}
	request(t, s, "admin/nodes", admin, node, 200)
	n := a.Store.Snapshot().Nodes[0]
	detail := request(t, s, "admin/nodes/"+n.ID, admin, nil, 200)
	raw, _ := json.Marshal(detail)
	if bytes.Contains(raw, []byte(n.AgentKey)) || bytes.Contains(raw, key) {
		t.Fatal("private key leaked in node detail")
	}
	node.Name = "renamed"
	node.Enabled = false
	request(t, s, "admin/nodes/"+n.ID, admin, node, 200)
	updated := a.Store.Snapshot().Nodes[0]
	if updated.Name != "renamed" || updated.Enabled || updated.AgentKey != n.AgentKey || updated.ID != n.ID {
		t.Fatal("node edit lost identity or state")
	}
	request(t, s, "admin/nodes/"+n.ID+"/enabled", admin, map[string]bool{"enabled": true}, 200)
	node.CAPEM = string(ca) + string(key)
	request(t, s, "admin/nodes", admin, node, 400)
	node.CAPEM = string(ca)
	node.Client.ECHConfig = "YWJj"
	request(t, s, "admin/nodes", admin, node, 400)
	if len(a.Store.Snapshot().Nodes) != 1 {
		t.Fatal("invalid node persisted")
	}
	r, _ := http.NewRequest("GET", s.URL+"/api/nodes/"+n.ID+"/profile.zip", nil)
	r.Header.Set("Authorization", "Bearer "+admin)
	res, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" {
		t.Fatal("profile ZIP unavailable")
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		reader, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		files[f.Name], e = io.ReadAll(reader)
		reader.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	var profile config.Client
	if e = json.Unmarshal(files["client.json"], &profile); e != nil {
		t.Fatal(e)
	}
	if profile.Validate() != nil || profile.Transport != "h2" || profile.Privacy != "strict" || len(profile.DeviceID) != 32 || profile.CAFile != "origin-ca.pem" || !bytes.Equal(files["origin-ca.pem"], ca) {
		t.Fatal("invalid client bundle")
	}
	if profile.Token != a.Store.Snapshot().Users[0].TunnelToken {
		t.Fatal("bundle credential belongs to another user")
	}
	for _, f := range files {
		if bytes.Contains(f, []byte(n.AgentKey)) || bytes.Contains(f, key) {
			t.Fatal("bundle contains node private key")
		}
	}
	request(t, s, "admin/nodes/"+n.ID+"/enabled", admin, map[string]bool{"enabled": false}, 200)
	request(t, s, "nodes/"+n.ID+"/profile.zip", admin, nil, 404)
	plan := Plan{Name: "fractional", Days: 30, PriceCents: 999, TrafficBytes: 1536 << 20, Devices: 2, Enabled: true}
	request(t, s, "admin/plans", admin, plan, 200)
	all := a.Store.Snapshot().Plans
	p := all[len(all)-1]
	p.PriceCents = 1234
	p.Enabled = false
	request(t, s, "admin/plans/"+p.ID, admin, p, 200)
	stored := a.Store.Snapshot().Plans[len(all)-1]
	if stored.PriceCents != 1234 || stored.Enabled {
		t.Fatal("plan edit not committed")
	}
	request(t, s, "orders", admin, map[string]string{"plan_id": p.ID}, 400)
	request(t, s, "admin/plans/"+p.ID+"/delete", admin, map[string]string{"name": "wrong"}, 409)
	request(t, s, "admin/plans/"+p.ID+"/delete", admin, map[string]string{"name": p.Name}, 200)
	request(t, s, "admin/nodes/"+n.ID+"/delete", admin, map[string]string{"name": updated.Name}, 200)
	if len(a.Store.Snapshot().Nodes) != 0 {
		t.Fatal("disabled offline node not deleted")
	}
}

func TestSettingsPartialSaveAndRuntimeAccess(t *testing.T) {
	a, s, admin := setup(t)
	request(t, s, "admin/settings", admin, map[string]any{"announcement": "keep this"}, 200)
	request(t, s, "admin/settings", admin, map[string]any{"access": AccessSettings{Registration: false, TrialHours: 0}}, 200)
	request(t, s, "register", "", map[string]string{"email": "closed@example.com", "password": "user-password-123456"}, 403)
	public := request(t, s, "public/settings", "", nil, 200)
	if public["registration"] != false || public["trial_hours"] != float64(0) {
		t.Fatal("public registration settings stale")
	}
	state := a.Store.Snapshot()
	if state.Announcement != "keep this" || state.Release.Version != Version {
		t.Fatal("partial settings erased unrelated fields")
	}
	request(t, s, "admin/settings", admin, map[string]any{"release": Release{Version: "v0.4.0", URL: "https://example.com/download.zip", SHA256: strings.Repeat("g", 64)}}, 400)
	request(t, s, "admin/settings", admin, map[string]any{"release": Release{Version: "bad-version"}}, 400)
	request(t, s, "admin/settings", admin, map[string]any{"access": AccessSettings{Registration: true, TrialHours: 169}}, 400)
	request(t, s, "admin/settings", admin, map[string]any{"release": Release{Version: "v0.4.0", URL: "https://example.com/download.zip", SHA256: strings.Repeat("A", 64)}}, 200)
	if a.Store.Snapshot().Release.SHA256 != strings.Repeat("a", 64) {
		t.Fatal("checksum not normalized")
	}
	request(t, s, "admin/settings", admin, map[string]any{"access": AccessSettings{Registration: true, TrialHours: 2}}, 200)
	login := request(t, s, "register", "", map[string]string{"email": "trial@example.com", "password": "user-password-123456"}, 200)
	token := login["token"].(string)
	request(t, s, "trial", token, map[string]any{}, 200)
	uid := login["user"].(map[string]any)["id"].(string)
	expires := findUserPtr(a.Store.Snapshot(), uid).ExpiresAt
	if delta := expires - time.Now().Unix(); delta < 7195 || delta > 7200 {
		t.Fatal("trial ignored runtime settings")
	}
	reopened, e := OpenStore(a.Config)
	if e != nil || reopened.Snapshot().Access.TrialHours != 2 {
		t.Fatal("access settings not durable", e)
	}
}

func TestOrderCancellationIsolationAndFulfilmentRace(t *testing.T) {
	a, s, admin := setup(t)
	user := request(t, s, "register", "", map[string]string{"email": "buyer@example.com", "password": "user-password-123456"}, 200)
	other := request(t, s, "register", "", map[string]string{"email": "other@example.com", "password": "user-password-123456"}, 200)
	token := user["token"].(string)
	uid := user["user"].(map[string]any)["id"].(string)
	pid := a.Store.Snapshot().Plans[0].ID
	order := request(t, s, "orders", token, map[string]string{"plan_id": pid}, 201)
	oid := order["id"].(string)
	request(t, s, "orders/"+oid+"/cancel", other["token"].(string), map[string]any{}, 409)
	request(t, s, "orders/"+oid+"/cancel", token, map[string]any{}, 200)
	request(t, s, "orders/"+oid+"/cancel", token, map[string]any{}, 200)
	request(t, s, "admin/orders/"+oid+"/fulfil", admin, map[string]any{}, 409)
	if findUserPtr(a.Store.Snapshot(), uid).ExpiresAt != 0 {
		t.Fatal("cancelled order granted access")
	}
	order = request(t, s, "orders", token, map[string]string{"plan_id": pid}, 201)
	oid = order["id"].(string)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, path := range []string{"admin/orders/" + oid + "/fulfil", "orders/" + oid + "/cancel"} {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			r, _ := http.NewRequest("POST", s.URL+"/api/"+path, strings.NewReader("{}"))
			bearer := token
			if strings.HasPrefix(path, "admin/") {
				bearer = admin
			}
			r.Header.Set("Authorization", "Bearer "+bearer)
			res, e := s.Client().Do(r)
			if e != nil {
				codes <- 0
				return
			}
			res.Body.Close()
			codes <- res.StatusCode
		}(path)
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 200 {
			success++
		} else if code != 409 {
			t.Fatal("unexpected race result", code)
		}
	}
	if success != 1 {
		t.Fatal("fulfil and cancel both committed")
	}
	state := a.Store.Snapshot()
	o := state.Orders[len(state.Orders)-1]
	u := findUser(&state, uid)
	if (o.Status == "paid") != (u.ExpiresAt > 0) {
		t.Fatal("order state and entitlement disagree")
	}
}

func TestRevokedSessionCannotCommitAndSelfPasswordReset(t *testing.T) {
	a, s, admin := setup(t)
	state := a.Store.Snapshot()
	u := state.Users[0]
	session := state.Sessions[0]
	request(t, s, "admin/users/"+u.ID+"/password", admin, map[string]string{"password": "new-admin-password-for-test"}, 200)
	request(t, s, "me", admin, nil, 401)
	called := false
	if a.commit(&u, &session, func(d *State) error { called = true; d.Announcement = "forbidden"; return nil }) == nil || called {
		t.Fatal("revoked session committed")
	}
	request(t, s, "login", "", map[string]string{"email": u.Email, "password": "new-admin-password-for-test"}, 200)
	if a.Store.Snapshot().Users[0].TunnelToken == u.TunnelToken {
		t.Fatal("password reset retained node credential")
	}
}

func TestFiniteRenewalPreservesActiveUnlimitedAccount(t *testing.T) {
	u := User{ExpiresAt: time.Now().Add(time.Hour).Unix(), Limit: 0, Devices: 3, Upload: 123}
	if e := extend(&u, 30, 100<<30, 3); e != nil || u.Limit != 0 {
		t.Fatal("finite renewal removed unlimited access", e)
	}
	u.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	if e := extend(&u, 30, 100<<30, 3); e != nil || u.Limit != u.Upload+(100<<30) {
		t.Fatal("expired renewal did not grant finite balance", e)
	}
}
