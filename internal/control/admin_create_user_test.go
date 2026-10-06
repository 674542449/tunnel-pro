package control

import "testing"

func TestAdminCreateUserAssignsPlanAtomically(t *testing.T) {
	a, s, admin := commercialSetup(t)
	plan := a.Store.Snapshot().Plans[0]
	body := map[string]string{"email": " Created@Example.test ", "password": "created-password-123", "plan_id": plan.ID}
	request(t, s, "admin/users", "", body, 401)
	request(t, s, "admin/users", admin, body, 200)
	d := a.Store.Snapshot()
	var created *User
	for i := range d.Users {
		if d.Users[i].Email == "created@example.test" {
			created = &d.Users[i]
		}
	}
	if created == nil || created.Role != "user" || created.NeedsEmailVerification {
		t.Fatal("admin-created user missing or requires verification", created)
	}
	granted := false
	for _, g := range d.Entitlements {
		granted = granted || g.UserID == created.ID && g.Source == "admin_assignment" && g.PlanID == plan.ID && !g.Test
	}
	if !granted {
		t.Fatal("plan was not assigned on creation")
	}
	request(t, s, "login", "", map[string]string{"email": "created@example.test", "password": "created-password-123"}, 200)
	if out := request(t, s, "admin/users", admin, body, 409); out["error"] != "邮箱已存在" {
		t.Fatal("duplicate email lost its specific error", out)
	}
	before := len(a.Store.Snapshot().Users)
	request(t, s, "admin/users", admin, map[string]string{"email": "orphan@example.test", "password": "created-password-123", "plan_id": ID()}, 409)
	if len(a.Store.Snapshot().Users) != before {
		t.Fatal("failed plan assignment left a user behind")
	}
}

func TestAdminCreateUserRejectsPlanOutsideCommercialMode(t *testing.T) {
	a, s, admin := setup(t)
	plan := a.Store.Snapshot().Plans[0]
	request(t, s, "admin/users", admin, map[string]string{"email": "legacy@example.test", "password": "legacy-password-123", "plan_id": plan.ID}, 409)
	request(t, s, "admin/users", admin, map[string]string{"email": "legacy@example.test", "password": "legacy-password-123"}, 200)
	if len(a.Store.Snapshot().Entitlements) != 0 {
		t.Fatal("legacy mode created a commercial entitlement")
	}
}
