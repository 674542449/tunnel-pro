package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	mu        sync.RWMutex
	path      string
	state     State
	db        *sql.DB
	revision  int64
	dbHealthy bool
}

// WriteFile commits a complete private snapshot; a failed write never replaces the old file.
func WriteFile(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = replaceSnapshot(name, path); e != nil {
		return e
	}
	syncDirectory(filepath.Dir(path))
	return nil
}
func OpenStore(c Config) (*Store, error) {
	s, err := openStore(c)
	if err != nil {
		return nil, err
	}
	// Persist the two-group migration before accepting requests. Old staff
	// accounts keep their data but must sign in again as ordinary users.
	needsMigration := false
	for _, u := range s.Snapshot().Users {
		if u.Role != "admin" && u.Role != "user" {
			needsMigration = true
			break
		}
	}
	if needsMigration {
		err = s.Update(func(d *State) error {
			for i := range d.Users {
				u := &d.Users[i]
				if u.Role == "admin" || u.Role == "user" {
					continue
				}
				u.Role = "user"
				revokeSessions(d, u.ID, "")
				record(d, "system", "role_migrated", u.ID)
			}
			return nil
		})
		if err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func openStore(c Config) (*Store, error) {
	if c.DatabaseURL != "" {
		return openPostgres(c)
	}
	s := &Store{path: c.DataFile}
	b, e := os.ReadFile(c.DataFile)
	if e == nil {
		if e = json.Unmarshal(b, &s.state); e != nil {
			return nil, errors.New("invalid control store; refusing to overwrite")
		}
		if s.state.Schema != 1 || len(s.state.Users) == 0 || s.state.Reports == nil {
			return nil, errors.New("unsupported control store")
		}
		normalizeLists(&s.state)
		return s, nil
	}
	if !os.IsNotExist(e) {
		return nil, e
	}
	h, e := passwordHash(c.AdminPassword)
	if e != nil {
		return nil, e
	}
	s.state = State{Schema: 1, Reports: map[string]Counter{}, Users: []User{{ID: ID(), Email: c.AdminEmail, PasswordHash: h, TunnelToken: Token(), Role: "admin", ExpiresAt: time.Now().AddDate(10, 0, 0).Unix(), Devices: 8, CreatedAt: time.Now().Unix()}}, Plans: []Plan{{ID: ID(), Name: "标准月度", Days: 30, PriceCents: 0, TrafficBytes: 100 << 30, Devices: 3, Enabled: true}}, Release: Release{Version: Version}}
	normalizeLists(&s.state)
	b, e = json.MarshalIndent(s.state, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = WriteFile(s.path, b); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) Snapshot() State {
	if s.db != nil {
		s.refreshPostgres()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.state)
	var d State
	json.Unmarshal(b, &d)
	normalizeLists(&d)
	return d
}

// Existing schema-1 stores may encode empty collections as null.
func normalizeLists(d *State) {
	if d.Nodes == nil {
		d.Nodes = []Node{}
	}
	if d.Plans == nil {
		d.Plans = []Plan{}
	}
	if d.Orders == nil {
		d.Orders = []Order{}
	}
	if d.Sessions == nil {
		d.Sessions = []Session{}
	}
	if d.Audit == nil {
		d.Audit = []Audit{}
	}
	if d.Payments == nil {
		d.Payments = []Payment{}
	}
	if d.PaymentEvents == nil {
		d.PaymentEvents = []PaymentEvent{}
	}
	if d.Refunds == nil {
		d.Refunds = []Refund{}
	}
	if d.Entitlements == nil {
		d.Entitlements = []Entitlement{}
	}
	if d.Challenges == nil {
		d.Challenges = []Challenge{}
	}
	if d.Outbox == nil {
		d.Outbox = []MailMessage{}
	}
	if d.Tickets == nil {
		d.Tickets = []Ticket{}
	}
	if d.Incidents == nil {
		d.Incidents = []Incident{}
	}
	if d.Invites == nil {
		d.Invites = []BetaInvite{}
	}
	if d.Leases == nil {
		d.Leases = []Lease{}
	}
	if d.TestUsage == nil {
		d.TestUsage = map[string]Counter{}
	}
}
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.updatePostgres(fn)
	}
	b, e := json.Marshal(s.state)
	if e != nil {
		return e
	}
	var next State
	if e = json.Unmarshal(b, &next); e != nil {
		return e
	}
	normalizeLists(&next)
	auditBefore := auditSnapshot(&s.state)
	auditStart := len(s.state.Audit)
	if e = fn(&next); e != nil {
		return e
	}
	enrichAudit(auditBefore, &next, auditStart)
	b, e = json.MarshalIndent(next, "", "  ")
	if e != nil {
		return e
	}
	if e = WriteFile(s.path, b); e != nil {
		return e
	}
	s.state = next
	return nil
}
