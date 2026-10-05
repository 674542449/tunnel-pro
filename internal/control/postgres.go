package control

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"path/filepath"
	"time"
)

const postgresSchema = `
CREATE TABLE IF NOT EXISTS control_meta (id integer PRIMARY KEY CHECK(id=1), revision bigint NOT NULL, payload jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS control_entities (kind text NOT NULL, id text NOT NULL, ordinal integer NOT NULL, payload jsonb NOT NULL, PRIMARY KEY(kind,id), CHECK(jsonb_typeof(payload)='object'));
CREATE UNIQUE INDEX IF NOT EXISTS control_user_email ON control_entities((lower(payload->>'email'))) WHERE kind='users';
CREATE UNIQUE INDEX IF NOT EXISTS control_payment_ref ON control_entities((payload->>'provider'),(payload->>'reference')) WHERE kind='payments' AND payload->>'reference'<>'';
CREATE UNIQUE INDEX IF NOT EXISTS control_payment_event ON control_entities((payload->>'provider'),(payload->>'external_id')) WHERE kind='payment_events';
CREATE UNIQUE INDEX IF NOT EXISTS control_purchase_grant ON control_entities((payload->>'order_id'),(payload->>'test')) WHERE kind='entitlements' AND payload->>'source'='purchase';
CREATE UNIQUE INDEX IF NOT EXISTS control_refund_payment ON control_entities((payload->>'payment_id')) WHERE kind='refunds';
CREATE INDEX IF NOT EXISTS control_entity_user ON control_entities(kind,(payload->>'user_id'));
`

var entityKinds = []string{"users", "nodes", "plans", "orders", "sessions", "audit", "payments", "payment_events", "refunds", "entitlements", "challenges", "outbox", "tickets", "incidents", "invites", "leases", "reports", "test_usage"}

type pgRow struct {
	Kind, ID string
	Ordinal  int
	Payload  []byte
}
type pgQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func encodeRows(d State) ([]byte, map[string]pgRow, error) {
	raw, e := json.Marshal(d)
	if e != nil {
		return nil, nil, e
	}
	var parts map[string]json.RawMessage
	if e = json.Unmarshal(raw, &parts); e != nil {
		return nil, nil, e
	}
	out := map[string]pgRow{}
	for _, kind := range entityKinds {
		data := parts[kind]
		delete(parts, kind)
		if len(data) == 0 || string(data) == "null" {
			continue
		}
		if kind == "reports" || kind == "test_usage" {
			var m map[string]json.RawMessage
			if e = json.Unmarshal(data, &m); e != nil {
				return nil, nil, e
			}
			for id, payload := range m {
				out[kind+":"+id] = pgRow{Kind: kind, ID: id, Payload: payload}
			}
			continue
		}
		var values []json.RawMessage
		if e = json.Unmarshal(data, &values); e != nil {
			return nil, nil, e
		}
		for i, payload := range values {
			var identity struct {
				ID      string `json:"id"`
				Hash    string `json:"hash"`
				Time    int64  `json:"time"`
				Actor   string `json:"actor"`
				Action  string `json:"action"`
				Subject string `json:"subject"`
			}
			if e = json.Unmarshal(payload, &identity); e != nil {
				return nil, nil, e
			}
			id := identity.ID
			if kind == "sessions" {
				id = identity.Hash
			}
			if kind == "audit" {
				id = Hash(string(payload)) + fmt.Sprintf("-%d", i)
			}
			if id == "" {
				return nil, nil, errors.New("missing entity identity")
			}
			key := kind + ":" + id
			if _, exists := out[key]; exists {
				return nil, nil, errors.New("duplicate entity identity")
			}
			out[key] = pgRow{kind, id, i, payload}
		}
	}
	meta, e := json.Marshal(parts)
	return meta, out, e
}
func readPostgres(ctx context.Context, q pgQuery) (State, int64, error) {
	var meta []byte
	var revision int64
	var d State
	if e := q.QueryRowContext(ctx, "SELECT payload,revision FROM control_meta WHERE id=1").Scan(&meta, &revision); e != nil {
		return d, 0, e
	}
	var parts map[string]json.RawMessage
	if e := json.Unmarshal(meta, &parts); e != nil {
		return d, 0, e
	}
	lists := map[string][]json.RawMessage{}
	maps := map[string]map[string]json.RawMessage{"reports": {}, "test_usage": {}}
	rows, e := q.QueryContext(ctx, "SELECT kind,id,payload FROM control_entities ORDER BY kind,ordinal,id")
	if e != nil {
		return d, 0, e
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var payload []byte
		if e = rows.Scan(&kind, &id, &payload); e != nil {
			return d, 0, e
		}
		if m, ok := maps[kind]; ok {
			m[id] = payload
		} else {
			lists[kind] = append(lists[kind], json.RawMessage(payload))
		}
	}
	if e = rows.Err(); e != nil {
		return d, 0, e
	}
	for kind, list := range lists {
		parts[kind], e = json.Marshal(list)
		if e != nil {
			return d, 0, e
		}
	}
	for kind, m := range maps {
		parts[kind], e = json.Marshal(m)
		if e != nil {
			return d, 0, e
		}
	}
	raw, e := json.Marshal(parts)
	if e != nil {
		return d, 0, e
	}
	e = json.Unmarshal(raw, &d)
	normalizeLists(&d)
	return d, revision, e
}
func openPostgres(c Config) (*Store, error) {
	db, e := sql.Open("pgx", c.DatabaseURL)
	if e != nil {
		return nil, errors.New("invalid database configuration")
	}
	db.SetMaxOpenConns(6)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	success := false
	defer func() {
		if !success {
			db.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e = db.PingContext(ctx); e != nil {
		return nil, errors.New("database connection unavailable")
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(872764500)"); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, postgresSchema); e != nil {
		return nil, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM control_meta").Scan(&count); e != nil {
		return nil, e
	}
	if count == 0 {
		if _, e = os.Stat(c.DataFile + ".postgres-migrated"); e == nil {
			return nil, errors.New("database empty after migration; restore a database backup")
		}
		legacy := c
		legacy.DatabaseURL = ""
		seed, e := OpenStore(legacy)
		if e != nil {
			return nil, e
		}
		d := seed.Snapshot()
		meta, rows, e := encodeRows(d)
		if e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO control_meta(id,revision,payload) VALUES(1,1,$1)", meta); e != nil {
			return nil, e
		}
		for _, row := range rows {
			if _, e = tx.ExecContext(ctx, "INSERT INTO control_entities(kind,id,ordinal,payload) VALUES($1,$2,$3,$4)", row.Kind, row.ID, row.Ordinal, row.Payload); e != nil {
				return nil, e
			}
		}
	}
	d, revision, e := readPostgres(ctx, tx)
	if e != nil {
		return nil, e
	}
	if d.Schema != 1 || len(d.Users) == 0 || d.Reports == nil {
		return nil, errors.New("invalid database state")
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if e = WriteFile(c.DataFile+".postgres-migrated", []byte("PostgreSQL is authoritative. Restore database backups; do not reuse the JSON snapshot as current state.\n")); e != nil {
		return nil, e
	}
	success = true
	return &Store{path: c.DataFile, db: db, state: d, revision: revision, dbHealthy: true}, nil
}
func (s *Store) refreshPostgres() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		s.dbHealthy = false
		return
	}
	defer tx.Rollback()
	var revision int64
	if e = tx.QueryRowContext(ctx, "SELECT revision FROM control_meta WHERE id=1").Scan(&revision); e != nil {
		s.dbHealthy = false
		return
	}
	if revision != s.revision {
		d, rev, e := readPostgres(ctx, tx)
		if e != nil {
			s.dbHealthy = false
			return
		}
		s.state = d
		s.revision = rev
	}
	s.dbHealthy = true
}
func (s *Store) updatePostgres(fn func(*State) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		s.dbHealthy = false
		return errors.New("database transaction unavailable")
	}
	defer tx.Rollback()
	var revision int64
	if e = tx.QueryRowContext(ctx, "SELECT revision FROM control_meta WHERE id=1 FOR UPDATE").Scan(&revision); e != nil {
		s.dbHealthy = false
		return e
	}
	old, _, e := readPostgres(ctx, tx)
	if e != nil {
		s.dbHealthy = false
		return e
	}
	_, before, e := encodeRows(old)
	if e != nil {
		return e
	}
	auditBefore := auditSnapshot(&old)
	auditStart := len(old.Audit)
	if e = fn(&old); e != nil {
		return e
	}
	enrichAudit(auditBefore, &old, auditStart)
	normalizeLists(&old)
	meta, after, e := encodeRows(old)
	if e != nil {
		return e
	}
	for key, row := range before {
		if _, exists := after[key]; !exists {
			if _, e = tx.ExecContext(ctx, "DELETE FROM control_entities WHERE kind=$1 AND id=$2", row.Kind, row.ID); e != nil {
				return e
			}
		}
	}
	for key, row := range after {
		if prior, ok := before[key]; ok && prior.Ordinal == row.Ordinal && bytes.Equal(prior.Payload, row.Payload) {
			continue
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO control_entities(kind,id,ordinal,payload) VALUES($1,$2,$3,$4) ON CONFLICT(kind,id) DO UPDATE SET ordinal=excluded.ordinal,payload=excluded.payload", row.Kind, row.ID, row.Ordinal, row.Payload); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE control_meta SET revision=revision+1,payload=$1 WHERE id=1", meta); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		s.dbHealthy = false
		return errors.New("database commit unavailable")
	}
	s.state = old
	s.revision = revision + 1
	s.dbHealthy = true
	return nil
}
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
func (s *Store) Backend() string {
	if s.db != nil {
		return "postgresql"
	}
	return "json"
}
func (s *Store) Healthy() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.db == nil || s.dbHealthy }
func (s *Store) Export(path string) error {
	d := s.Snapshot()
	if !s.Healthy() {
		return errors.New("database unavailable")
	}
	if !filepath.IsAbs(path) {
		return errors.New("backup path must be absolute")
	}
	raw, e := json.MarshalIndent(d, "", "  ")
	if e != nil {
		return e
	}
	return WriteFile(path, raw)
}
