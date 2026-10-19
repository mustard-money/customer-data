// Package db owns the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect creates a pgx pool and verifies connectivity with a ping. Errors
// name the server it tried (see Describe), never the password.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool for %s: %w", Describe(cfg), err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to %s: %w", Describe(cfg), err)
	}
	return pool, nil
}

// Describe names the database a config points at, without the password:
// database "mustard" at localhost:5432 as user "mustard".
func Describe(cfg *pgxpool.Config) string {
	c := cfg.ConnConfig
	return fmt.Sprintf("database %q at %s as user %q", c.Database, net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))), c.User)
}
