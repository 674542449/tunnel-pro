package control

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCommercialEncryptedBackupRestoreAndTamper(t *testing.T) {
	a, _, _ := commercialSetup(t)
	directory := testTempDir(t)
	key := filepath.Join(directory, "key")
	os.WriteFile(key, bytes.Repeat([]byte{27}, 32), 0600)
	backup := filepath.Join(directory, "snapshot.txbk")
	if e := a.Store.EncryptedBackup(backup, key); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(backup)
	before := a.Store.Snapshot()
	if bytes.Contains(raw, []byte(before.Users[0].TunnelToken)) {
		t.Fatal("unencrypted backup")
	}
	c := a.Config
	c.DataFile = filepath.Join(directory, "restored.json")
	if e := RestoreNew(c, backup, key); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(restored.Snapshot())
	if !bytes.Equal(left, right) {
		t.Fatal("restored data differs")
	}
	if e = RestoreNew(c, backup, key); e == nil {
		t.Fatal("restore overwrote existing store")
	}
	raw[len(raw)-1] ^= 1
	os.WriteFile(backup, raw, 0600)
	if _, e = ReadEncryptedBackup(backup, key); e == nil {
		t.Fatal("tampered backup accepted")
	}
}
func TestPostgresEncryptedRestoreIntoNewSchema(t *testing.T) {
	c := pgConfig(t)
	s, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Update(func(d *State) error {
		d.Announcement = "recovery evidence"
		d.Tickets = append(d.Tickets, Ticket{ID: ID(), UserID: d.Users[0].ID, Status: "closed"})
		return nil
	})
	dir := testTempDir(t)
	key := filepath.Join(dir, "key")
	os.WriteFile(key, bytes.Repeat([]byte{28}, 32), 0600)
	backup := filepath.Join(dir, "database.txbk")
	if e = s.EncryptedBackup(backup, key); e != nil {
		t.Fatal(e)
	}
	target := pgConfig(t)
	if e = RestoreNew(target, backup, key); e != nil {
		t.Fatal("restore failed", e)
	}
	r, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if r.Snapshot().Announcement != "recovery evidence" || len(r.Snapshot().Tickets) != 1 {
		t.Fatal("database restore lost records")
	}
	if e = RestoreNew(target, backup, key); e == nil {
		t.Fatal("restore overwrote live database")
	}
}
