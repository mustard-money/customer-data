package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mustard-money/customer-data/deletions"
)

// ErrNotFound is returned when a scenario does not exist.
var ErrNotFound = errors.New("scenario not found")

// ErrDeleted is returned by Upsert when the scenario was deleted; deleted ids
// can't be reused.
var ErrDeleted = errors.New("scenario was deleted")

// Store persists scenarios in Postgres. itemIds, events and parameters are
// jsonb columns written from and read back into the typed Go values, so the
// API shape (key order, omitted optional fields) does not depend on jsonb's
// internal key ordering.
//
// Every method is scoped to one customer; ids are unique per customer, not
// globally.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const scenarioColumns = `id, name, item_ids, excluded_item_ids, events, parameters, created_at, updated_at`

func scanScenario(row pgx.Row, extra ...any) (Scenario, error) {
	var (
		s                                   Scenario
		itemIDs, excluded, events, paramsJS []byte
	)
	dest := append([]any{&s.ID, &s.Name, &itemIDs, &excluded, &events, &paramsJS, &s.CreatedAt, &s.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	}
	if err != nil {
		return Scenario{}, err
	}
	if err := json.Unmarshal(itemIDs, &s.ItemIDs); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: decode item_ids: %w", s.ID, err)
	}
	// NULL (a scenario from before layers) stays nil.
	if excluded != nil {
		var ids []string
		if err := json.Unmarshal(excluded, &ids); err != nil {
			return Scenario{}, fmt.Errorf("scenario %s: decode excluded_item_ids: %w", s.ID, err)
		}
		s.ExcludedItemIDs = &ids
	}
	if err := json.Unmarshal(events, &s.Events); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: decode events: %w", s.ID, err)
	}
	if err := json.Unmarshal(paramsJS, &s.Parameters); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: decode parameters: %w", s.ID, err)
	}
	return s, nil
}

// List returns the customer's scenarios ordered by createdAt then id.
func (s *Store) List(ctx context.Context, customerID string) ([]Scenario, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+scenarioColumns+` FROM scenarios WHERE customer_id = $1 ORDER BY created_at, id`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Scenario{}
	for rows.Next() {
		sc, err := scanScenario(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Get returns the scenario with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, customerID, id string) (Scenario, error) {
	return scanScenario(s.pool.QueryRow(ctx,
		`SELECT `+scenarioColumns+` FROM scenarios WHERE customer_id = $1 AND id = $2`, customerID, id))
}

// Upsert creates the scenario or fully replaces an existing one with the same
// id. The server sets updated_at; created reports whether a row was inserted.
func (s *Store) Upsert(ctx context.Context, customerID string, sc Scenario) (saved Scenario, created bool, err error) {
	itemIDs, events := sc.ItemIDs, sc.Events
	if itemIDs == nil {
		itemIDs = []string{}
	}
	if events == nil {
		events = []Event{}
	}
	itemIDsJS, err := json.Marshal(itemIDs)
	if err != nil {
		return Scenario{}, false, err
	}
	eventsJS, err := json.Marshal(events)
	if err != nil {
		return Scenario{}, false, err
	}
	paramsJS, err := json.Marshal(sc.Parameters)
	if err != nil {
		return Scenario{}, false, err
	}
	var excludedJS *string
	if sc.ExcludedItemIDs != nil {
		b, err := json.Marshal(*sc.ExcludedItemIDs)
		if err != nil {
			return Scenario{}, false, err
		}
		js := string(b)
		excludedJS = &js
	}

	// xmax = 0 is true only for freshly inserted rows (not ON CONFLICT updates).
	saved, err = scanScenario(s.pool.QueryRow(ctx, `
		INSERT INTO scenarios (customer_id, id, name, item_ids, excluded_item_ids, events, parameters, created_at, updated_at)
		SELECT $1, $2, $3, $4::jsonb, $8::jsonb, $5::jsonb, $6::jsonb, $7::timestamptz, date_trunc('milliseconds', now())
		WHERE `+deletions.NotDeletedSQL(deletions.Scenarios)+`
		ON CONFLICT (customer_id, id) DO UPDATE SET
			name       = EXCLUDED.name,
			item_ids   = EXCLUDED.item_ids,
			excluded_item_ids = EXCLUDED.excluded_item_ids,
			events     = EXCLUDED.events,
			parameters = EXCLUDED.parameters,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
		RETURNING `+scenarioColumns+`, (xmax = 0)`,
		customerID, sc.ID, sc.Name, string(itemIDsJS), string(eventsJS), string(paramsJS), sc.CreatedAt, excludedJS,
	), &created)
	// No row means the NOT EXISTS check refused it.
	if errors.Is(err, ErrNotFound) {
		return saved, false, ErrDeleted
	}
	return saved, created, err
}

// Delete removes the scenario with the given id and records the deletion, so a
// device holding an old copy can't save it again. The deletion is recorded
// even when the scenario isn't here (e.g. deleted before it was ever uploaded);
// ErrNotFound is returned in that case.
func (s *Store) Delete(ctx context.Context, customerID, id string) error {
	var existed bool
	err := s.pool.QueryRow(ctx, `
		WITH gone AS (DELETE FROM scenarios WHERE customer_id = $1 AND id = $2 RETURNING id),
		recorded AS (`+deletions.RecordSQL(deletions.Scenarios)+`)
		SELECT EXISTS (SELECT 1 FROM gone)`, customerID, id).Scan(&existed)
	if err != nil {
		return err
	}
	if !existed {
		return ErrNotFound
	}
	return nil
}

// DeletedIDs returns the ids of the customer's deleted scenarios.
func (s *Store) DeletedIDs(ctx context.Context, customerID string) ([]string, error) {
	return deletions.IDs(ctx, s.pool, customerID, deletions.Scenarios)
}
