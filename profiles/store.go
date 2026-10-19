package profiles

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mustard-money/customer-data/deletions"
)

// ErrNotFound is returned when a profile does not exist.
var ErrNotFound = errors.New("profile not found")

// ErrDeleted is returned by Upsert when the profile was deleted; deleted ids
// can't be reused.
var ErrDeleted = errors.New("profile was deleted")

// Store persists profiles in Postgres. Every method is scoped to one
// customer; ids are unique per customer, not globally.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const profileColumns = `id, name, relationship, birth_year, birth_month, created_at, updated_at`

func scanProfile(row pgx.Row, extra ...any) (Profile, error) {
	var p Profile
	dest := append([]any{&p.ID, &p.Name, &p.Relationship, &p.BirthYear, &p.BirthMonth, &p.CreatedAt, &p.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// List returns the customer's profiles ordered by createdAt then id.
func (s *Store) List(ctx context.Context, customerID string) ([]Profile, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE customer_id = $1 ORDER BY created_at, id`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns the profile with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, customerID, id string) (Profile, error) {
	return scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE customer_id = $1 AND id = $2`, customerID, id))
}

// Upsert creates the profile or fully replaces an existing one with the same
// id. The server sets updated_at; created reports whether a row was inserted.
func (s *Store) Upsert(ctx context.Context, customerID string, p Profile) (saved Profile, created bool, err error) {
	// xmax = 0 is true only for freshly inserted rows (not ON CONFLICT updates).
	saved, err = scanProfile(s.pool.QueryRow(ctx, `
		INSERT INTO profiles (customer_id, id, name, relationship, birth_year, birth_month, created_at, updated_at)
		SELECT $1, $2, $3, $4, $5::integer, $6::smallint, $7::timestamptz, date_trunc('milliseconds', now())
		WHERE `+deletions.NotDeletedSQL(deletions.Profiles)+`
		ON CONFLICT (customer_id, id) DO UPDATE SET
			name         = EXCLUDED.name,
			relationship = EXCLUDED.relationship,
			birth_year   = EXCLUDED.birth_year,
			birth_month  = EXCLUDED.birth_month,
			created_at   = EXCLUDED.created_at,
			updated_at   = EXCLUDED.updated_at
		RETURNING `+profileColumns+`, (xmax = 0)`,
		customerID, p.ID, p.Name, p.Relationship, p.BirthYear, p.BirthMonth, p.CreatedAt,
	), &created)
	// No row means the NOT EXISTS check refused it.
	if errors.Is(err, ErrNotFound) {
		return saved, false, ErrDeleted
	}
	return saved, created, err
}

// Delete removes the profile with the given id and records the deletion, so a
// device holding an old copy can't save it again. The deletion is recorded
// even when the profile isn't here (e.g. deleted before it was ever uploaded);
// ErrNotFound is returned in that case.
func (s *Store) Delete(ctx context.Context, customerID, id string) error {
	var existed bool
	err := s.pool.QueryRow(ctx, `
		WITH gone AS (DELETE FROM profiles WHERE customer_id = $1 AND id = $2 RETURNING id),
		recorded AS (`+deletions.RecordSQL(deletions.Profiles)+`)
		SELECT EXISTS (SELECT 1 FROM gone)`, customerID, id).Scan(&existed)
	if err != nil {
		return err
	}
	if !existed {
		return ErrNotFound
	}
	return nil
}

// DeletedIDs returns the ids of the customer's deleted profiles.
func (s *Store) DeletedIDs(ctx context.Context, customerID string) ([]string, error) {
	return deletions.IDs(ctx, s.pool, customerID, deletions.Profiles)
}
