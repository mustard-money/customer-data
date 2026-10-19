package account

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no account profile has been saved yet.
var ErrNotFound = errors.New("account profile not set")

// Store persists account profiles in Postgres, one row per customer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const profileColumns = `country, postal_code, filing_status, expected_return, inflation, cash_return, emergency_fund::double precision, updated_at`

func scanProfile(row pgx.Row, extra ...any) (Profile, error) {
	var p Profile
	dest := append([]any{&p.Country, &p.PostalCode, &p.FilingStatus, &p.ExpectedReturn, &p.Inflation, &p.CashReturn, &p.EmergencyFund, &p.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// Get returns the customer's account profile, or ErrNotFound if none has been
// saved.
func (s *Store) Get(ctx context.Context, customerID string) (Profile, error) {
	return scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileColumns+` FROM account_profile WHERE customer_id = $1`, customerID))
}

// Put creates or fully replaces the account profile. The server sets
// updated_at; created reports whether the row was inserted.
func (s *Store) Put(ctx context.Context, customerID string, p Profile) (saved Profile, created bool, err error) {
	// xmax = 0 is true only for freshly inserted rows (not ON CONFLICT updates).
	saved, err = scanProfile(s.pool.QueryRow(ctx, `
		INSERT INTO account_profile (customer_id, country, postal_code, filing_status, expected_return, inflation, emergency_fund, cash_return, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, date_trunc('milliseconds', now()))
		ON CONFLICT (customer_id) DO UPDATE SET
			country       = EXCLUDED.country,
			postal_code   = EXCLUDED.postal_code,
			filing_status = EXCLUDED.filing_status,
			expected_return = EXCLUDED.expected_return,
			inflation       = EXCLUDED.inflation,
			emergency_fund  = EXCLUDED.emergency_fund,
			cash_return     = EXCLUDED.cash_return,
			updated_at    = EXCLUDED.updated_at
		RETURNING `+profileColumns+`, (xmax = 0)`,
		customerID, p.Country, p.PostalCode, p.FilingStatus, p.ExpectedReturn, p.Inflation, p.EmergencyFund, p.CashReturn,
	), &created)
	return saved, created, err
}
