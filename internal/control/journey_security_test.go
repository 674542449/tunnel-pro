package control

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
	"tunnelx/internal/config"
	"tunnelx/internal/pki"
)

func TestJourneyPasswordChangeInvalidatesOutstandingRecovery(t *testing.T) {
	for _, actor := range []string{"self", "admin", "recovery"} {
		t.Run(actor, func(t *testing.T) {
			a, s, admin := commercialSetup(t)
			uid, user := betaAccount(t, a, s, admin)
			begin := request(t, s, "security/mfa/begin", user, map[string]string{"password": "beta-test-password-strong"}, 200)
			request(t, s, "security/forgot", "", map[string]string{"email": "beta@example.test"}, 202)
			d := a.Store.Snapshot()
			body, err := a.unseal("mail", d.Outbox[len(d.Outbox)-1].Body)
			if err != nil {
				t.Fatal(err)
			}
			token := strings.Fields(strings.Split(body, "#reset=")[1])[0]
			password := "journey-new-password-secure"
			switch actor {
			case "self":
				request(t, s, "security/password", user, map[string]string{"current_password": "beta-test-password-strong", "password": password}, 200)
			case "admin":
				request(t, s, "admin/users/"+uid+"/password", admin, map[string]string{"password": password}, 200)
			case "recovery":
				request(t, s, "security/reset", "", map[string]string{"token": token, "password": password}, 200)
			}
			request(t, s, "me", user, nil, 401)
			request(t, s, "security/reset", "", map[string]string{"token": token, "password": "stale-link-password-secure"}, 400)
			login := request(t, s, "login", "", map[string]string{"email": "beta@example.test", "password": password}, 200)
			code, _ := totp(begin["secret"].(string), time.Now().Unix()/30)
			request(t, s, "security/mfa/confirm", login["token"].(string), map[string]string{"challenge_id": begin["challenge_id"].(string), "code": code}, 400)
			for _, g := range a.Store.Snapshot().Challenges {
				if g.UserID == uid && (g.Purpose == "reset" || g.Purpose == "mfa") && (g.UsedAt == 0 || g.Secret != "") {
					t.Fatal("old credential challenge survived password replacement")
				}
			}
		})
	}
}

func TestJourneyManualNodeScopeAndDeletionReferences(t *testing.T) {
	a, s, admin := commercialSetup(t)
	ca, _, _, err := pki.Certificate("edge.tunnelx.invalid")
	if err != nil {
		t.Fatal(err)
	}
	_, ech, err := pki.ECH("proxy.test")
	if err != nil {
		t.Fatal(err)
	}
	n := Node{Name: "journey-node", CAPEM: string(ca), Client: config.Client{ServerName: "edge.tunnelx.invalid", ServerIP: "203.0.113.10", Port: 8443, ECHConfig: base64.StdEncoding.EncodeToString(ech)}}
	request(t, s, "admin/nodes", admin, n, 200)
	n = a.Store.Snapshot().Nodes[0]
	if !n.TestOnly {
		t.Fatal("test payment mode created a formal manual node")
	}
	plan := a.Store.Snapshot().Plans[0]
	plan.NodeIDs = []string{n.ID}
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	deleteNode := func(status int) {
		request(t, s, "admin/nodes/"+n.ID+"/delete", admin, map[string]string{"name": n.Name}, status)
	}
	deleteNode(409)
	uid, user := betaAccount(t, a, s, admin)
	order := request(t, s, "orders", user, map[string]string{"plan_id": plan.ID}, 201)
	plan.NodeIDs = nil
	request(t, s, "admin/plans/"+plan.ID, admin, plan, 200)
	deleteNode(409)
	request(t, s, "orders/"+order["id"].(string)+"/cancel", user, map[string]any{}, 200)
	if err = a.Store.Update(func(d *State) error {
		d.Entitlements = append(d.Entitlements, Entitlement{ID: ID(), UserID: uid, StartsAt: time.Now().Unix() + 3600, EndsAt: time.Now().Unix() + 7200, NodeIDs: []string{n.ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deleteNode(409)
	if err = a.Store.Update(func(d *State) error {
		for i := range d.Entitlements {
			d.Entitlements[i].RevokedAt = time.Now().Unix()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deleteNode(200)
}
