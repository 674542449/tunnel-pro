package control

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOnlyAdminAndUserRoles(t *testing.T) {
	a, server, admin := commercialSetup(t)
	uid, token := betaAccount(t, a, server, admin)
	for _, role := range []string{"support", "finance", "owner", "", "Admin"} {
		request(t, server, "admin/users/"+uid+"/role", admin, map[string]string{"role": role}, 400)
		for _, path := range []string{"users", "tickets", "tickets/x/reply", "incidents", "orders", "payments", "payments/x/refund"} {
			for _, method := range []string{"GET", "POST"} {
				if adminPathAllowed(role, path, method) {
					t.Fatalf("legacy or unknown role %q allowed %s", role, path)
				}
			}
		}
	}
	request(t, server, "admin/users/"+uid+"/role", token, map[string]string{"role": "admin"}, 403)
	owner := a.Store.Snapshot().Users[0].ID
	request(t, server, "admin/users/"+owner+"/role", admin, map[string]string{"role": "user"}, 409)
	request(t, server, "admin/users/"+uid+"/role", admin, map[string]string{"role": "admin"}, 200)
	request(t, server, "me", token, nil, 401)
	request(t, server, "admin/users/"+uid+"/role", admin, map[string]string{"role": "user"}, 200)
}

func testRoleMigration(t *testing.T, c Config) {
	t.Helper()
	s, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	admin := s.Snapshot().Users[0]
	var expected []User
	err = s.Update(func(d *State) error {
		for _, role := range []string{"support", "finance", "unknown"} {
			u := User{ID: ID(), Email: role + "@example.test", Role: role, TunnelToken: Token(), ExpiresAt: time.Now().Add(time.Hour).Unix(), Limit: 10 << 30, Upload: 42, Download: 100, Devices: 3}
			d.Users = append(d.Users, u)
			d.Sessions = append(d.Sessions, Session{Hash: Hash(Token()), UserID: u.ID})
			u.Role = "user"
			expected = append(expected, u)
		}
		d.Sessions = append(d.Sessions, Session{Hash: Hash(Token()), UserID: admin.ID})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for attempt := 0; attempt < 2; attempt++ {
		s, err = OpenStore(c)
		if err != nil {
			t.Fatal(err)
		}
		d := s.Snapshot()
		if !reflect.DeepEqual(d.Users[1:], expected) || !reflect.DeepEqual(d.Users[0], admin) {
			t.Fatal("migration changed account data")
		}
		if len(d.Sessions) != 1 || d.Sessions[0].UserID != admin.ID {
			t.Fatal("migration did not revoke only old staff sessions")
		}
		count := 0
		for _, audit := range d.Audit {
			if audit.Action == "role_migrated" {
				count++
				if audit.Actor != "system" || len(audit.Changes) == 0 {
					t.Fatal("migration lacks detailed audit")
				}
			}
		}
		if count != 3 {
			t.Fatal("missing or repeated migration audit")
		}
		s.Close()
	}
}

func TestRoleMigrationJSON(t *testing.T) {
	testRoleMigration(t, Config{DataFile: filepath.Join(testTempDir(t), "state.json"), AdminEmail: "owner@example.test", AdminPassword: "role-migration-test-password"})
}

func TestPostgresRoleMigration(t *testing.T) { testRoleMigration(t, pgConfig(t)) }
