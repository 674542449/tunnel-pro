package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var backupMagic = []byte("TUNNELX-BACKUP-1\n")

func backupCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("backup encryption key must contain 32 raw bytes")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (s *Store) EncryptedBackup(path, keyFile string) error {
	if !filepath.IsAbs(path) || !filepath.IsAbs(keyFile) {
		return errors.New("backup and key paths must be absolute")
	}
	key, e := os.ReadFile(keyFile)
	if e != nil {
		return errors.New("private backup key unavailable")
	}
	g, e := backupCipher(key)
	if e != nil {
		return e
	}
	d := s.Snapshot()
	if !s.Healthy() {
		return errors.New("database unavailable")
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	iv := make([]byte, g.NonceSize())
	if _, e = rand.Read(iv); e != nil {
		return e
	}
	out := append(append([]byte{}, backupMagic...), iv...)
	out = g.Seal(out, iv, raw, backupMagic)
	return WriteFile(path, out)
}
func ReadEncryptedBackup(path, keyFile string) (State, error) {
	var d State
	key, e := os.ReadFile(keyFile)
	if e != nil {
		return d, errors.New("private backup key unavailable")
	}
	g, e := backupCipher(key)
	if e != nil {
		return d, e
	}
	file, e := os.Open(path)
	if e != nil {
		return d, e
	}
	defer file.Close()
	b, e := io.ReadAll(io.LimitReader(file, 512<<20))
	if e != nil || len(b) < len(backupMagic)+g.NonceSize() || string(b[:len(backupMagic)]) != string(backupMagic) {
		return d, errors.New("invalid encrypted backup")
	}
	raw, e := g.Open(nil, b[len(backupMagic):len(backupMagic)+g.NonceSize()], b[len(backupMagic)+g.NonceSize():], backupMagic)
	if e != nil {
		return d, errors.New("backup integrity verification failed")
	}
	if e = json.Unmarshal(raw, &d); e != nil || d.Schema != 1 || len(d.Users) == 0 || d.Reports == nil {
		return d, errors.New("invalid restored database state")
	}
	normalizeLists(&d)
	return d, nil
}

// RestoreNew initializes an empty target. It never overwrites an existing store.
func RestoreNew(c Config, backup, key string) error {
	if c.DatabaseURL != "" {
		db, e := openRestoreDatabase(c)
		if e != nil {
			return e
		}
		defer db.Close()
		var count int
		ctx := restoreContext()
		if e = db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('control_meta','control_entities')").Scan(&count); e != nil {
			return errors.New("restore database check failed")
		}
		if count > 0 {
			return errors.New("restore requires an empty target schema")
		}
	}
	if _, e := os.Stat(c.DataFile); !os.IsNotExist(e) {
		return errors.New("restore requires a new snapshot path")
	}
	if _, e := os.Stat(c.DataFile + ".postgres-migrated"); !os.IsNotExist(e) {
		return errors.New("restore requires a new migration path")
	}
	d, e := ReadEncryptedBackup(backup, key)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if e = WriteFile(c.DataFile, raw); e != nil {
		return e
	}
	s, e := OpenStore(c)
	if e != nil {
		return e
	}
	return s.Close()
}
