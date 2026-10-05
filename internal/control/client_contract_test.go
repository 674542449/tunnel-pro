package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestClientNodeAccessMatchesProfileSyncAndRefund(t *testing.T) {
	a, s, admin := commercialSetup(t)
	uid, token := betaAccount(t, a, s, admin)
	one := Node{ID: ID(), Name: "allowed", Enabled: true, TestOnly: true, AgentKey: Token(), Capabilities: []string{"strict-billing", "leases-v1"}}
	other := one
	other.ID = ID()
	other.Name = "outside-plan"
	if err := a.Store.Update(func(d *State) error {
		d.Nodes = append(d.Nodes, one, other)
		d.Plans[0].NodeIDs = []string{one.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pid := purchased(t, a, s, token)
	nodes := func() []map[string]any {
		req, _ := http.NewRequest("GET", s.URL+"/api/nodes", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result []map[string]any
		if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&result) != nil {
			t.Fatal("nodes contract failed")
		}
		return result
	}
	check := func(allowed bool) {
		t.Helper()
		for _, n := range nodes() {
			access, ok := n["access"].(map[string]any)
			if !ok {
				t.Fatal("node list omits account-specific access")
			}
			want := allowed && n["id"] == one.ID
			if access["allowed"] != want {
				t.Fatal("node list disagrees with selected-node entitlement")
			}
			if !want && access["reason"] == "" {
				t.Fatal("missing denial reason")
			}
		}
		status := 403
		if allowed {
			status = 200
		}
		request(t, s, "nodes/"+one.ID+"/profile", token, nil, status)
		request(t, s, "nodes/"+other.ID+"/profile", token, nil, 403)
		b, _ := json.Marshal(SyncRequest{BootID: ID(), Version: Version, Capabilities: one.Capabilities})
		req, _ := http.NewRequest("POST", s.URL+"/api/node/sync", bytes.NewReader(b))
		req.Header.Set("X-TunnelX-Node", one.ID)
		req.Header.Set("Authorization", "Bearer "+one.AgentKey)
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var sync SyncResponse
		if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&sync) != nil {
			t.Fatal("sync contract failed")
		}
		found := false
		for _, grant := range sync.Grants {
			if grant.UserID == uid {
				found = true
			}
		}
		if found != allowed {
			t.Fatal("node grants disagree with UI access")
		}
	}
	check(true)
	request(t, s, "admin/payments/"+pid+"/refund", admin, map[string]string{"reason": "contract test refund"}, 200)
	check(false)
}

func TestNodeAccessMatchesVerificationAndMixedScope(t *testing.T) {
	d, n, u, now := ledger(t)
	n.Enabled = true
	for _, tc := range []struct {
		name    string
		modify  func(*State, *Node, *User)
		allowed bool
	}{
		{"valid", func(*State, *Node, *User) {}, true},
		{"email", func(_ *State, _ *Node, u *User) { u.NeedsEmailVerification = true; u.EmailVerifiedAt = 0 }, false},
		{"disabled", func(_ *State, _ *Node, u *User) { u.Disabled = true }, false},
		{"outside-plan", func(d *State, n *Node, _ *User) { d.Entitlements[0].NodeIDs = []string{ID()} }, false},
		{"expired", func(d *State, _ *Node, _ *User) { d.Entitlements[0].EndsAt = time.Now().Unix() - 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := d
			copy.Entitlements = append([]Entitlement{}, d.Entitlements...)
			node, user := n, u
			tc.modify(&copy, &node, &user)
			result := userNode(&copy, user, node, now)
			access := result["access"].(map[string]any)
			if access["allowed"] != tc.allowed {
				t.Fatal("incorrect access result")
			}
		})
	}
}

func TestUnverifiedLegacyAccountNeverSynced(t *testing.T) {
	a, s, _ := setup(t)
	reg := request(t, s, "register", "", map[string]string{"email": "legacy@example.test", "password": "contract-password-fixture"}, 200)
	uid := reg["user"].(map[string]any)["id"].(string)
	node := Node{ID: ID(), Enabled: true, AgentKey: Token()}
	if err := a.Store.Update(func(d *State) error {
		u := findUser(d, uid)
		u.ExpiresAt = time.Now().Unix() + 3600
		u.NeedsEmailVerification = true
		d.Nodes = append(d.Nodes, node)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(SyncRequest{BootID: ID(), Version: Version})
	req, _ := http.NewRequest("POST", s.URL+"/api/node/sync", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+node.AgentKey)
	req.Header.Set("X-TunnelX-Node", node.ID)
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result SyncResponse
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&result) != nil {
		t.Fatal("sync failed")
	}
	for _, g := range result.Grants {
		if g.UserID == uid {
			t.Fatal("unverified account was authorized contrary to profile export")
		}
	}
}

func TestPublishedVersionMatchesDesktopFormats(t *testing.T) {
	for _, v := range []string{"v0.7.0-rc.2+build.7", "v0.7.0+build.7", "0.7.0", "v0.7.0-rc.10"} {
		r := Release{Version: v}
		if err := validateRelease(&r); err != nil {
			t.Fatal(v, err)
		}
	}
	for _, v := range []string{"v0.7.0-rc..2", "v0.7.0+", "latest"} {
		r := Release{Version: v}
		if validateRelease(&r) == nil {
			t.Fatal("invalid version accepted", v)
		}
	}
}
