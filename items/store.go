package items

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mustard-money/customer-data/deletions"
)

// ErrNotFound is returned when an item does not exist.
var ErrNotFound = errors.New("item not found")

// ErrDeleted is returned by Upsert when the item was deleted; deleted ids
// can't be reused.
var ErrDeleted = errors.New("item was deleted")

// Store persists items in Postgres. Every method is scoped to one customer;
// ids are unique per customer, not globally.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const itemColumns = `id, category, name, amount, frequency, income_type, benefits, loan, scenario_id, replaces, account, workplace, as_of_year, as_of_month, return_rate,
	start_year, start_month, end_year, end_month, withdrawal_start_year, withdrawal_start_month, withdrawal_rate,
	self_employment, qualified_dividends, asset_class, income_rate, tax_exempt, created_at, updated_at`

func scanItem(row pgx.Row, extra ...any) (Item, error) {
	var it Item
	dest := append([]any{&it.ID, &it.Category, &it.Name, &it.Amount, &it.Frequency, &it.IncomeType, &it.Benefits, &it.Loan, &it.ScenarioID, &it.Replaces, &it.Account, &it.Workplace, &it.AsOfYear, &it.AsOfMonth, &it.ReturnRate,
		&it.StartYear, &it.StartMonth, &it.EndYear, &it.EndMonth, &it.WithdrawalStartYear, &it.WithdrawalStartMonth, &it.WithdrawalRate, &it.SelfEmployment, &it.QualifiedDividends, &it.AssetClass, &it.IncomeRate, &it.TaxExempt, &it.CreatedAt, &it.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

// List returns the customer's items ordered by createdAt then id, optionally
// filtered by category (empty means all).
func (s *Store) List(ctx context.Context, customerID, category string) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+itemColumns+`
		FROM items
		WHERE customer_id = $1 AND ($2 = '' OR category = $2)
		ORDER BY created_at, id`, customerID, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Get returns the item with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, customerID, id string) (Item, error) {
	return scanItem(s.pool.QueryRow(ctx,
		`SELECT `+itemColumns+` FROM items WHERE customer_id = $1 AND id = $2`, customerID, id))
}

// Upsert creates the item or fully replaces an existing one with the same id.
// The server sets updated_at; created reports whether a new row was inserted.
func (s *Store) Upsert(ctx context.Context, customerID string, it Item) (saved Item, created bool, err error) {
	// xmax = 0 is true only for freshly inserted rows (not ON CONFLICT updates).
	saved, err = scanItem(s.pool.QueryRow(ctx, `
		INSERT INTO items (customer_id, id, category, name, amount, frequency, income_type, benefits, account,
			as_of_year, as_of_month, return_rate,
			start_year, start_month, end_year, end_month, withdrawal_start_year, withdrawal_start_month, withdrawal_rate,
			loan, scenario_id, replaces, workplace, self_employment, qualified_dividends, asset_class, income_rate, tax_exempt, created_at, updated_at)
		SELECT $1, $2, $3, $4, $5::numeric, $6, $7, $8::jsonb, $9,
			$10::smallint, $11::smallint, $12::double precision,
			$13::smallint, $14::smallint, $15::smallint, $16::smallint, $17::smallint, $18::smallint, $19::double precision,
			$21::jsonb, $22, $23, $24::jsonb, $25::boolean, $26::boolean, $27, $28::double precision, $29::jsonb, $20::timestamptz, date_trunc('milliseconds', now())
		WHERE `+deletions.NotDeletedSQL(deletions.Items)+`
		ON CONFLICT (customer_id, id) DO UPDATE SET
			category   = EXCLUDED.category,
			name       = EXCLUDED.name,
			amount     = EXCLUDED.amount,
			frequency  = EXCLUDED.frequency,
			income_type = EXCLUDED.income_type,
			benefits    = EXCLUDED.benefits,
			account    = EXCLUDED.account,
			as_of_year  = EXCLUDED.as_of_year,
			as_of_month = EXCLUDED.as_of_month,
			return_rate = EXCLUDED.return_rate,
			start_year  = EXCLUDED.start_year,
			start_month = EXCLUDED.start_month,
			end_year    = EXCLUDED.end_year,
			end_month   = EXCLUDED.end_month,
			withdrawal_start_year  = EXCLUDED.withdrawal_start_year,
			withdrawal_start_month = EXCLUDED.withdrawal_start_month,
			withdrawal_rate        = EXCLUDED.withdrawal_rate,
			loan       = EXCLUDED.loan,
			scenario_id = EXCLUDED.scenario_id,
			replaces    = EXCLUDED.replaces,
			workplace   = EXCLUDED.workplace,
			self_employment = EXCLUDED.self_employment,
			qualified_dividends = EXCLUDED.qualified_dividends,
			asset_class = EXCLUDED.asset_class,
			income_rate = EXCLUDED.income_rate,
			tax_exempt  = EXCLUDED.tax_exempt,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
		RETURNING `+itemColumns+`, (xmax = 0)`,
		customerID, it.ID, it.Category, it.Name, strconv.FormatFloat(it.Amount, 'f', 2, 64), it.Frequency, it.IncomeType, it.Benefits, it.Account, it.AsOfYear, it.AsOfMonth, it.ReturnRate,
		it.StartYear, it.StartMonth, it.EndYear, it.EndMonth, it.WithdrawalStartYear, it.WithdrawalStartMonth, it.WithdrawalRate,
		it.CreatedAt, it.Loan, it.ScenarioID, it.Replaces, it.Workplace, it.SelfEmployment, it.QualifiedDividends, it.AssetClass, it.IncomeRate, it.TaxExempt,
	), &created)
	// No row means the NOT EXISTS check refused it.
	if errors.Is(err, ErrNotFound) {
		return saved, false, ErrDeleted
	}
	return saved, created, err
}

// Delete removes the item with the given id and records the deletion, so a
// device holding an old copy can't save it again. The deletion is recorded
// even when the item isn't here (e.g. deleted before it was ever uploaded);
// ErrNotFound is returned in that case.
func (s *Store) Delete(ctx context.Context, customerID, id string) error {
	var existed bool
	err := s.pool.QueryRow(ctx, `
		WITH gone AS (DELETE FROM items WHERE customer_id = $1 AND id = $2 RETURNING id),
		recorded AS (`+deletions.RecordSQL(deletions.Items)+`)
		SELECT EXISTS (SELECT 1 FROM gone)`, customerID, id).Scan(&existed)
	if err != nil {
		return err
	}
	if !existed {
		return ErrNotFound
	}
	return nil
}

// DeletedIDs returns the ids of the customer's deleted items.
func (s *Store) DeletedIDs(ctx context.Context, customerID string) ([]string, error) {
	return deletions.IDs(ctx, s.pool, customerID, deletions.Items)
}
