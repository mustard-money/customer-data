// Package deletions records which synced records each customer has deleted.
// A device that still holds a copy of a deleted record can't save it again
// (the resource answers 410 Gone), and list responses include the deleted ids
// so every device drops its copy on the next sync.
//
// Resource stores record a deletion in the same statement that deletes the
// row, and refuse upserts of deleted ids, using the SQL below with their
// collection name.
package deletions

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Collection names, matching the resources' URL paths and the
// deletions_collection_valid constraint.
const (
	Items     = "items"
	Profiles  = "profiles"
	Scenarios = "scenarios"
	Snapshots = "snapshots"
)

// RecordSQL inserts the deletion of record $2 of the given collection for
// customer $1. Use it as a data-modifying CTE next to the DELETE.
func RecordSQL(collection string) string {
	return `INSERT INTO deletions (customer_id, collection, id) VALUES ($1, '` + collection + `', $2)
		ON CONFLICT DO NOTHING`
}

// NotDeletedSQL is true unless record $2 of the collection was deleted by
// customer $1. Use it as the WHERE clause of an INSERT ... SELECT upsert.
func NotDeletedSQL(collection string) string {
	return `NOT EXISTS (SELECT 1 FROM deletions
		WHERE customer_id = $1 AND collection = '` + collection + `' AND id = $2)`
}

// IDs returns the ids the customer has deleted from the collection, oldest
// deletion first. Never nil.
func IDs(ctx context.Context, pool *pgxpool.Pool, customerID, collection string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT id FROM deletions
		WHERE customer_id = $1 AND collection = $2
		ORDER BY deleted_at, id`, customerID, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
