package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func pgConfig(t *testing.T) Config {
	t.Helper()
	dsn := os.Getenv("TUNNELX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration requires isolated test database")
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal("invalid test database URL")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal("database open failed")
	}
	schema := "qa_" + ID()
	if _, e = db.ExecContext(context.Background(), "CREATE SCHEMA "+schema); e != nil {
		t.Fatal("test schema creation failed")
	}
	t.Cleanup(func() { db.Exec("DROP SCHEMA " + schema + " CASCADE"); db.Close() })
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	return Config{DatabaseURL: u.String(), DataFile: filepath.Join(testTempDir(t), "snapshot.json"), AdminEmail: "pgadmin@example.test", AdminPassword: "postgres-private-test-password", Listen: "127.0.0.1:18081", PublicURL: "http://127.0.0.1:18081"}
}
func TestPostgresMigrationConcurrentWritersAndRestart(t *testing.T) {
	c := pgConfig(t)
	legacy := c
	legacy.DatabaseURL = ""
	seed, e := OpenStore(legacy)
	if e != nil {
		t.Fatal(e)
	}
	seed.Update(func(d *State) error { d.Announcement = "preserved legacy data"; return nil })
	before := seed.Snapshot()
	one, e := OpenStore(c)
	if e != nil {
		t.Fatal("migration failed", e)
	}
	defer one.Close()
	two, e := OpenStore(c)
	if e != nil {
		t.Fatal("second writer failed", e)
	}
	defer two.Close()
	if one.Snapshot().Users[0] != before.Users[0] || one.Snapshot().Announcement != before.Announcement {
		t.Fatal("legacy identity changed")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := one
			if i%2 == 1 {
				s = two
			}
			e := s.Update(func(d *State) error {
				d.Audit = append(d.Audit, Audit{Time: 1, Actor: "concurrent-test", Action: fmt.Sprint(i)})
				return nil
			})
			if e != nil {
				failures <- e
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal("concurrent transaction failed", e)
	}
	if len(one.Snapshot().Audit) != 24 || len(two.Snapshot().Audit) != 24 {
		t.Fatal("lost update across controllers")
	}
	if e := one.Update(func(d *State) error { d.Announcement = "must roll back"; return errors.New("rejected") }); e == nil {
		t.Fatal("rejected transaction committed")
	}
	if one.Snapshot().Announcement != before.Announcement {
		t.Fatal("transaction rollback lost data")
	}
	three, e := OpenStore(c)
	if e != nil {
		t.Fatal("restart failed", e)
	}
	defer three.Close()
	if len(three.Snapshot().Audit) != 24 {
		t.Fatal("restart lost database rows")
	}
}
func TestPostgresUniquePaymentEventsAndAtomicRollback(t *testing.T) {
	c := pgConfig(t)
	s, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	e = s.Update(func(d *State) error {
		d.PaymentEvents = []PaymentEvent{{ID: ID(), Provider: "test", ExternalID: "same-event"}, {ID: ID(), Provider: "test", ExternalID: "same-event"}}
		d.Announcement = "must not be visible"
		return nil
	})
	if e == nil {
		t.Fatal("SQL uniqueness not enforced")
	}
	d := s.Snapshot()
	if len(d.PaymentEvents) != 0 || d.Announcement != "" {
		t.Fatal("SQL failure partially committed")
	}
	if e = s.Update(func(d *State) error { d.Announcement = "subsequent success"; return nil }); e != nil {
		t.Fatal("connection failed after rollback", e)
	}
}
func TestPostgresBackupExportAndEmptyDatabaseGuard(t *testing.T) {
	c := pgConfig(t)
	s, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Export(filepath.Join(testTempDir(t), "private-backup.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("DELETE FROM control_meta"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if _, e = OpenStore(c); e == nil {
		t.Fatal("empty database reimported stale snapshot")
	}
}
