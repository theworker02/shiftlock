package postgres

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/theworker02/shiftlock"
)

// ErrStaleFencingToken is returned when a mutation carries a stale token.
var ErrStaleFencingToken = errors.New("fencing/postgres: stale fencing token")

// Resource is a fenced row helper. Table schema (caller migrates):
//
//	CREATE TABLE IF NOT EXISTS shiftlock_fenced (
//	  name TEXT PRIMARY KEY,
//	  fencing_token BIGINT NOT NULL DEFAULT 0,
//	  value TEXT NOT NULL DEFAULT ''
//	);
type Resource struct {
	DB    *sql.DB
	Table string
	Name  string
}

func (r *Resource) table() string {
	if r.Table == "" {
		return "shiftlock_fenced"
	}
	return r.Table
}

// Write updates value only if token >= stored fencing token (monotonic accept).
func (r *Resource) Write(token shiftlock.FencingToken, value string) error {
	if token.Zero() {
		return ErrStaleFencingToken
	}
	q := fmt.Sprintf(`INSERT INTO %s (name, fencing_token, value) VALUES ($1,$2,$3)
		ON CONFLICT (name) DO UPDATE SET fencing_token=$2, value=$3
		WHERE %s.fencing_token <= $2`, r.table(), r.table())
	res, err := r.DB.Exec(q, r.Name, uint64(token), value)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrStaleFencingToken
	}
	return nil
}

// Read returns value and token.
func (r *Resource) Read() (string, shiftlock.FencingToken, error) {
	q := fmt.Sprintf(`SELECT value, fencing_token FROM %s WHERE name=$1`, r.table())
	var v string
	var tok uint64
	err := r.DB.QueryRow(q, r.Name).Scan(&v, &tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return v, shiftlock.FencingToken(tok), err
}
