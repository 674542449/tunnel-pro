package control

import "testing"

func TestResetAdminRecoversLockedOutAdministrator(t *testing.T) {
	a, s, old := setup(t)
	member := request(t, s, "register", "", map[string]string{"email": "member@example.test", "password": "member-password-123"}, 200)
	if e := a.Store.Update(func(d *State) error {
		d.Users[0].MFASecret, d.Users[0].RecoveryHashes = "secret", "hashes"
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if a.Store.ResetAdmin("member@example.test", "x@example.test", "member-password-123") == nil {
		t.Fatal("a regular account was treated as an administrator")
	}
	if a.Store.ResetAdmin("admin@example.com", "member@example.test", "new-admin-password") == nil {
		t.Fatal("reset took over another account's email")
	}
	if a.Store.ResetAdmin("admin@example.com", "owner@example.test", "short") == nil {
		t.Fatal("short password accepted")
	}
	if e := a.Store.ResetAdmin(" Admin@Example.com ", "Owner@Example.test", "new-admin-password"); e != nil {
		t.Fatal(e)
	}
	request(t, s, "me", old, nil, 401)
	request(t, s, "login", "", map[string]string{"email": "admin@example.com", "password": a.Config.AdminPassword}, 401)
	fresh := request(t, s, "login", "", map[string]string{"email": "owner@example.test", "password": "new-admin-password"}, 200)
	if fresh["user"].(map[string]any)["role"] != "admin" || fresh["user"].(map[string]any)["mfa_enabled"] != false {
		t.Fatal("reset administrator lost its role or kept MFA", fresh["user"])
	}
	request(t, s, "me", member["token"].(string), nil, 200)
}
