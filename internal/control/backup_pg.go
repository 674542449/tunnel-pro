package control

import (
	"context"
	"database/sql"
	"errors"
)

func openRestoreDatabase(c Config) (*sql.DB, error) {
	db, e := sql.Open("pgx", c.DatabaseURL)
	if e != nil {
		return nil, errors.New("restore database unavailable")
	}
	return db, nil
}
func restoreContext() context.Context { return context.Background() }
