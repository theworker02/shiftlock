//go:build postgres

// Command postgres-coordination shows PostgreSQL-backed ownership.
// Requires DATABASE_URL (postgres DSN). Skips with a message if unset.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/postgres"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Println("Set DATABASE_URL to a postgres DSN to run this example.")
		os.Exit(0)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	be := postgres.New(db)
	ctx := context.Background()
	if err := be.Migrate(ctx); err != nil {
		log.Fatal(err)
	}

	coord, err := shiftlock.New(shiftlock.Config{
		Service: "demo", InstanceID: "pg-1", Backend: be, LeaseTTL: 15 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer coord.Close()

	claim, _ := coord.Claim(ctx, "billing-reconciler")
	lease, err := claim.WaitForOwnership(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("postgres owner token=%d\n", lease.FencingToken())
}
