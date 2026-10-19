package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaSQL is the whole schema as of migration 0032 (sql/schema.sql), the
// baseline a new database starts from. It records itself as
// baselineVersion.
//
//go:embed sql/schema.sql
var schemaSQL string

// migrationsFS holds the migrations after the baseline
// (migrations/NNNN_name.sql, numbered from 0033).
//
//go:embed migrations
var migrationsFS embed.FS

const (
	baselineVersion = "0032_baseline"
	// lastSquashed is the last of the migrations sql/schema.sql replaced: a
	// database that ran it already has the baseline's schema.
	lastSquashed = "0032_account_profile_cash_return"
)

// migrationLockID is an arbitrary constant used with pg_advisory_xact_lock so
// that concurrently starting servers do not apply the same migration twice.
const migrationLockID = 72_110_101_011

// Migrate brings the database to the current schema: an empty database gets
// the baseline (sql/schema.sql), one that ran migrations 0001–0032 before
// they were combined into it is recorded as at the baseline, and then any
// embedded migrations (migrations/NNNN_name.sql) not yet recorded in
// schema_migrations are applied, each in its own transaction together with
// the insert that records it.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	if err := applyBaseline(ctx, pool, logger); err != nil {
		return fmt.Errorf("baseline: %w", err)
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, file := range files {
		version := strings.TrimSuffix(path.Base(file), ".sql")
		sqlBytes, err := migrationsFS.ReadFile(file)
		if err != nil {
			return err
		}
		applied, err := applyMigration(ctx, pool, version, string(sqlBytes))
		if err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if applied {
			logger.Info("applied migration", "version", version)
		}
	}
	return nil
}

// applyBaseline creates the schema in an empty database, or records the
// baseline for one that ran the migrations it replaced. A database part of
// the way through those can't be brought forward from here.
func applyBaseline(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(migrationLockID)); err != nil {
			return err
		}
		var at, latest string
		if err := tx.QueryRow(ctx, `
			SELECT
				COALESCE(MAX(version) FILTER (WHERE version IN ($1, $2)), ''),
				COALESCE(MAX(version), '')
			FROM schema_migrations`, baselineVersion, lastSquashed,
		).Scan(&at, &latest); err != nil {
			return err
		}
		switch {
		case at == baselineVersion:
			return nil
		case at == lastSquashed:
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, baselineVersion)
			if err == nil {
				logger.Info("recorded migrations 0001-0032 as the baseline", "version", baselineVersion)
			}
			return err
		case latest != "":
			return fmt.Errorf("the database is at migration %s, before %s; migrate it with a server from before the migrations were combined, then start this one", latest, lastSquashed)
		}
		// No arguments: pgx uses the simple protocol, so the multi-statement file works.
		if _, err := tx.Exec(ctx, schemaSQL); err != nil {
			return err
		}
		logger.Info("created the schema", "version", baselineVersion)
		return nil
	})
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version, sql string) (applied bool, err error) {
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(migrationLockID)); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
		).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		// No arguments: pgx uses the simple protocol, so multi-statement files work.
		if _, err := tx.Exec(ctx, sql); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}
