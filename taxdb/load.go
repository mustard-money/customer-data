// Package taxdb keeps the tax tables in Postgres in step with the tax data
// that the taxtables package embeds (taxtables/data): it loads them at
// startup whenever that data (or its projection) changes. Nothing in this
// server reads the tables back for clients; tax figures are computed with
// the taxtables package and only results leave the server.
package taxdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mustard-money/customer-data/taxtables"
)

// loadLock is the advisory lock key that keeps two starting servers from
// loading the tables at once.
const loadLock = 0x7461787461626c65 // "taxtable"

// Load writes d (with projected years) into the tax tables unless they
// already hold this version of it. It replaces the tables' contents in one
// transaction, so readers see either the old data or the new.
func Load(ctx context.Context, pool *pgxpool.Pool, d *taxtables.Data, logger *slog.Logger) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(loadLock)); err != nil {
		return err
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT hash FROM tax_data_version`).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if current == d.Hash {
		return nil
	}

	if _, err := tx.Exec(ctx, `TRUNCATE tax_zip_ranges, tax_layers, payroll_tax_years, capital_gains_tax_years, tax_jurisdictions`); err != nil {
		return err
	}

	copies, err := tables(d)
	if err != nil {
		return err
	}
	for _, c := range copies {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{c.name}, c.columns, pgx.CopyFromRows(c.rows)); err != nil {
			return fmt.Errorf("loading %s: %w", c.name, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tax_data_version (hash) VALUES ($1)
		ON CONFLICT (singleton) DO UPDATE SET hash = EXCLUDED.hash, loaded_at = now()`, d.Hash); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	logger.Info("loaded tax tables",
		"jurisdictions", len(copies[0].rows), "layers", len(copies[1].rows), "payroll_years", len(copies[3].rows),
		"capital_gains_years", len(copies[4].rows))
	return nil
}

// table is one tax table's rows, in the order of its columns.
type table struct {
	name    string
	columns []string
	rows    [][]any
}

// tables is what the tax tables hold for d (with projected years): every
// row, table by table in an order that satisfies the foreign keys.
func tables(d *taxtables.Data) ([]table, error) {
	var jurisdictionRows, layerRows, zipRows [][]any
	for _, j := range d.Jurisdictions {
		jurisdictionRows = append(jurisdictionRows, []any{j.ID, j.Kind, nullable(j.Region), nullable(j.State), j.Since})
		for _, yt := range d.Expand(j) {
			fields, err := taxFields(yt.Tax)
			if err != nil {
				return nil, fmt.Errorf("%s %d: %w", j.ID, yt.Year, err)
			}
			layerRows = append(layerRows, []any{
				j.ID, int16(yt.Year), yt.Tax.Type, fields, yt.Tax.Source, nullable(yt.Tax.Notes), yt.Projected,
			})
		}
		zips := d.Zips[j.ID]
		for _, set := range []struct {
			ranges   []string
			coverage string
		}{{zips.Full, "full"}, {zips.Partial, "partial"}} {
			for _, r := range set.ranges {
				from, to, _ := taxtables.ParseZipRange(r) // validated by Parse
				zipRows = append(zipRows, []any{j.ID, int32(from), int32(to), set.coverage})
			}
		}
	}

	var payrollRows [][]any
	for _, py := range d.ExpandPayroll() {
		p := py.Payroll
		var single, joint *float64
		if p.AdditionalMedicareThreshold != nil {
			single = &p.AdditionalMedicareThreshold.Single
			joint = p.AdditionalMedicareThreshold.MarriedJoint
		}
		payrollRows = append(payrollRows, []any{
			int16(py.Year), p.SocialSecurityRate, p.WageBase, p.MedicareRate, p.MedicareWageBase,
			p.AdditionalMedicareRate, single, joint, p.Source, nullable(p.Notes), py.Projected,
		})
	}

	var gainsRows [][]any
	for _, cy := range d.ExpandCapitalGains() {
		rules, err := capitalGainsFields(cy.CapitalGains)
		if err != nil {
			return nil, fmt.Errorf("capital gains %d: %w", cy.Year, err)
		}
		c := cy.CapitalGains
		gainsRows = append(gainsRows, []any{int16(cy.Year), c.Method, rules, c.Source, nullable(c.Notes), cy.Projected})
	}

	return []table{
		{"tax_jurisdictions", []string{"id", "kind", "region", "state", "since"}, jurisdictionRows},
		{"tax_layers", []string{"jurisdiction_id", "tax_year", "type", "tax", "source", "notes", "projected"}, layerRows},
		{"tax_zip_ranges", []string{"jurisdiction_id", "zip_from", "zip_to", "coverage"}, zipRows},
		{"payroll_tax_years", []string{
			"tax_year", "social_security_rate", "wage_base", "medicare_rate", "medicare_wage_base",
			"additional_medicare_rate", "additional_medicare_single", "additional_medicare_joint",
			"source", "notes", "projected",
		}, payrollRows},
		{"capital_gains_tax_years", []string{"tax_year", "method", "rules", "source", "notes", "projected"}, gainsRows},
	}, nil
}

// capitalGainsFields is the jsonb stored for a year's capital gains rules:
// the method's fields, without method, source and notes.
func capitalGainsFields(c *taxtables.CapitalGains) ([]byte, error) {
	r := *c
	r.Method, r.Source, r.Notes = "", "", ""
	return json.Marshal(r)
}

// taxFields is the jsonb stored for a tax: its type's fields, without the
// type, source and notes (which have their own columns).
func taxFields(t *taxtables.Tax) ([]byte, error) {
	c := *t
	c.Type, c.Source, c.Notes = "", "", ""
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	delete(m, "type")
	delete(m, "source")
	return json.Marshal(m)
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
