package db

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchDB creates an empty database next to TEST_DATABASE_URL's for one
// test and drops it afterwards. Skips without TEST_DATABASE_URL.
func scratchDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(ctx) })
	name := fmt.Sprintf("mustard_migrate_%d_%s", os.Getpid(), strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_")))
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name) //nolint:errcheck // best effort
	})
	return pool
}

func versions(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestMigrateEmptyDatabase(t *testing.T) {
	pool := scratchDB(t)
	ctx := context.Background()
	for range 2 { // the second run finds nothing to do
		if err := Migrate(ctx, pool, quiet); err != nil {
			t.Fatal(err)
		}
	}
	if got := versions(t, pool); strings.Join(got, ",") != baselineVersion {
		t.Errorf("versions = %v", got)
	}
	var tables int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 16 {
		t.Errorf("%d tables, want 16", tables)
	}
}

func TestMigrateDatabaseFromBeforeTheBaseline(t *testing.T) {
	pool := scratchDB(t)
	ctx := context.Background()
	// A database that ran migrations 0001–0032: the same schema, recorded under their names.
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM schema_migrations;
		INSERT INTO schema_migrations (version) VALUES ('0031_item_asset_class'), ('0032_account_profile_cash_return')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool, quiet); err != nil {
		t.Fatal(err)
	}
	want := "0031_item_asset_class,0032_account_profile_cash_return,0032_baseline"
	if got := versions(t, pool); strings.Join(got, ",") != want {
		t.Errorf("versions = %v, want %s", got, want)
	}
}

func TestMigrateRefusesAPartlyMigratedDatabase(t *testing.T) {
	pool := scratchDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
		INSERT INTO schema_migrations (version) VALUES ('0010_scope_data_by_customer')`); err != nil {
		t.Fatal(err)
	}
	err := Migrate(ctx, pool, quiet)
	if err == nil || !strings.Contains(err.Error(), "the database is at migration 0010_scope_data_by_customer") {
		t.Fatalf("err = %v", err)
	}
}
