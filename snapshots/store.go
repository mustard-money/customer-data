package snapshots

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mustard-money/customer-data/deletions"
)

// ErrNotFound is returned when a snapshot does not exist.
var ErrNotFound = errors.New("snapshot not found")

// ErrDeleted is returned by Upsert when the snapshot was deleted; deleted ids
// can't be reused.
var ErrDeleted = errors.New("snapshot was deleted")

// Store persists snapshots in Postgres, scoped to one customer per call.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const snapshotColumns = `id, year, month, net_worth, assets, debts, monthly_income, monthly_expenses, created_at, updated_at`

func scanSnapshot(row pgx.Row, extra ...any) (Snapshot, error) {
	var s Snapshot
	dest := append([]any{&s.ID, &s.Year, &s.Month, &s.NetWorth, &s.Assets, &s.Debts, &s.MonthlyIncome,
		&s.MonthlyExpenses, &s.CreatedAt, &s.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	return s, err
}

// List returns the customer's snapshots, oldest month first.
func (s *Store) List(ctx context.Context, customerID string) ([]Snapshot, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+snapshotColumns+` FROM snapshots WHERE customer_id = $1 ORDER BY year, month`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// Get returns the snapshot with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, customerID, id string) (Snapshot, error) {
	return scanSnapshot(s.pool.QueryRow(ctx,
		`SELECT `+snapshotColumns+` FROM snapshots WHERE customer_id = $1 AND id = $2`, customerID, id))
}

func money(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// Upsert creates the snapshot or replaces the one for the same month. The
// server sets updated_at; created reports whether a row was inserted.
func (s *Store) Upsert(ctx context.Context, customerID string, snap Snapshot) (saved Snapshot, created bool, err error) {
	saved, err = scanSnapshot(s.pool.QueryRow(ctx, `
		INSERT INTO snapshots (customer_id, id, year, month, net_worth, assets, debts, monthly_income,
			monthly_expenses, created_at, updated_at)
		SELECT $1, $2, $3::smallint, $4::smallint, $5::numeric, $6::numeric, $7::numeric, $8::numeric,
			$9::numeric, $10::timestamptz, date_trunc('milliseconds', now())
		WHERE `+deletions.NotDeletedSQL(deletions.Snapshots)+`
		ON CONFLICT (customer_id, id) DO UPDATE SET
			year             = EXCLUDED.year,
			month            = EXCLUDED.month,
			net_worth        = EXCLUDED.net_worth,
			assets           = EXCLUDED.assets,
			debts            = EXCLUDED.debts,
			monthly_income   = EXCLUDED.monthly_income,
			monthly_expenses = EXCLUDED.monthly_expenses,
			created_at       = EXCLUDED.created_at,
			updated_at       = EXCLUDED.updated_at
		RETURNING `+snapshotColumns+`, (xmax = 0)`,
		customerID, snap.ID, snap.Year, snap.Month, money(snap.NetWorth), money(snap.Assets), money(snap.Debts),
		money(snap.MonthlyIncome), money(snap.MonthlyExpenses), snap.CreatedAt,
	), &created)
	if errors.Is(err, ErrNotFound) {
		return saved, false, ErrDeleted
	}
	return saved, created, err
}

// Delete removes the snapshot and records the deletion (see package
// deletions). ErrNotFound is returned when it wasn't here.
func (s *Store) Delete(ctx context.Context, customerID, id string) error {
	var existed bool
	err := s.pool.QueryRow(ctx, `
		WITH gone AS (DELETE FROM snapshots WHERE customer_id = $1 AND id = $2 RETURNING id),
		recorded AS (`+deletions.RecordSQL(deletions.Snapshots)+`)
		SELECT EXISTS (SELECT 1 FROM gone)`, customerID, id).Scan(&existed)
	if err != nil {
		return err
	}
	if !existed {
		return ErrNotFound
	}
	return nil
}

// DeletedIDs returns the ids of the customer's deleted snapshots.
func (s *Store) DeletedIDs(ctx context.Context, customerID string) ([]string, error) {
	return deletions.IDs(ctx, s.pool, customerID, deletions.Snapshots)
}
