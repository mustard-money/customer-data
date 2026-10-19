package taxdb

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mustard-money/customer-data/taxtables"
)

// WriteSQL writes the tax tables' contents for d (with projected years), and
// its version, as a psql script: db/sql/default-data.sql, the same rows Load
// puts in. Run in one transaction (psql -1) on a database with the schema;
// it replaces whatever the tables held, and a server starting on it sees the
// version and leaves them alone.
func WriteSQL(w io.Writer, d *taxtables.Data) error {
	ts, err := tables(d)
	if err != nil {
		return err
	}
	b := bufio.NewWriter(w)
	fmt.Fprintf(b, `-- Default data every database needs that isn't a customer's: the US tax
-- tables (federal, state and local income tax by year, ZIP code coverage,
-- payroll tax and capital gains rules, 1970 onward with projected years) and
-- the version of the tax data they hold.
--
-- GENERATED from taxtables/data; don't edit by hand.
-- Regenerate after the tax data changes:  make default-data
-- (go test ./taxdb -run TestDefaultData -update). The server also loads the
-- same rows at startup whenever the version differs, so a database is never
-- left with old ones.
--
--   psql "$DATABASE_URL" -1 -f db/sql/default-data.sql     (after schema.sql)

TRUNCATE tax_zip_ranges, tax_layers, payroll_tax_years, capital_gains_tax_years, tax_jurisdictions;
`)
	for _, t := range ts {
		fmt.Fprintf(b, "\nCOPY %s (%s) FROM stdin;\n", t.name, strings.Join(t.columns, ", "))
		for _, row := range t.rows {
			for i, v := range row {
				if i > 0 {
					b.WriteByte('\t')
				}
				cell, err := copyText(v)
				if err != nil {
					return fmt.Errorf("%s: %w", t.name, err)
				}
				b.WriteString(cell)
			}
			b.WriteByte('\n')
		}
		b.WriteString("\\.\n")
	}
	fmt.Fprintf(b, `
INSERT INTO tax_data_version (hash) VALUES ('%s')
ON CONFLICT (singleton) DO UPDATE SET hash = EXCLUDED.hash, loaded_at = now();
`, d.Hash)
	return b.Flush()
}

// copyText is a value in COPY's text format: \N for null, and backslash,
// tab, newline and carriage return escaped in text.
func copyText(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return `\N`, nil
	case string:
		return escapeCopy(v), nil
	case *string:
		if v == nil {
			return `\N`, nil
		}
		return escapeCopy(*v), nil
	case []byte:
		return escapeCopy(string(v)), nil
	case bool:
		if v {
			return "t", nil
		}
		return "f", nil
	case int:
		return strconv.Itoa(v), nil
	case *int:
		if v == nil {
			return `\N`, nil
		}
		return strconv.Itoa(*v), nil
	case int16:
		return strconv.Itoa(int(v)), nil
	case int32:
		return strconv.Itoa(int(v)), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case *float64:
		if v == nil {
			return `\N`, nil
		}
		return strconv.FormatFloat(*v, 'f', -1, 64), nil
	}
	return "", fmt.Errorf("can't write a %T", v)
}

var copyEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

func escapeCopy(s string) string { return copyEscaper.Replace(s) }
