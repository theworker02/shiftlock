//go:build integration && postgres

package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/backendtest"
	"github.com/theworker02/shiftlock/backend/postgres"
)

func TestIntegrationContract(t *testing.T) {
	dsn := os.Getenv("SHIFTLOCK_POSTGRES_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set SHIFTLOCK_POSTGRES_URL for integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skip(err)
	}

	backendtest.RunContract(t, func(t *testing.T) shiftlock.Backend {
		be := postgres.New(db, postgres.WithSchema("public"))
		if err := be.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		// unique claim prefix via factory — contract uses fixed names; truncate
		_, _ = db.ExecContext(ctx, `TRUNCATE shiftlock_claims, shiftlock_generations`)
		return be
	})
}
