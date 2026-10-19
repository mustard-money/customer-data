package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/mustard-money/customer-data/db"
	"github.com/mustard-money/customer-data/taxdb"
	"github.com/mustard-money/customer-data/taxtables"
)

func TestTaxTablesLoadAndStayPrivate(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	pool, err := db.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	data, err := taxtables.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Loading twice: the second run sees the same version and does nothing.
	for range 2 {
		if err := taxdb.Load(ctx, pool, data, logger); err != nil {
			t.Fatal(err)
		}
	}
	var federal, states, lastYear int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM tax_layers WHERE jurisdiction_id = 'us-federal' AND NOT projected),
		       (SELECT count(*) FROM tax_jurisdictions WHERE kind = 'state'),
		       (SELECT max(tax_year) FROM tax_layers)`).Scan(&federal, &states, &lastYear); err != nil {
		t.Fatal(err)
	}
	if federal < 57 || states != 51 || lastYear != taxtables.ProjectThrough {
		t.Errorf("loaded federal years %d, states %d, last year %d", federal, states, lastYear)
	}

	// The tables aren't served: the old public routes need a sign-in and
	// then don't exist.
	expectStatus(t, do(t, srv, "GET", "/api/v1/tax-tables/2026", "", anonymousHeader, "1"), 401)
	expectStatus(t, do(t, srv, "GET", "/api/v1/tax-tables/2026", ""), 404)
	expectStatus(t, do(t, srv, "GET", "/api/v1/tax-tables/zips", ""), 404)

	// Signed in, taxes are computed, and only results come back.
	r := do(t, srv, "POST", "/api/v1/taxes", `{"taxYear":2026,"postalCode":"94103","filingStatus":"single","cases":[{"payrollWages":150000,"income":150000}]}`)
	expectStatus(t, r, 200)
	body := r.json(t)
	layers := body["layers"].([]any)
	if len(layers) != 2 || layers[1].(map[string]any)["id"] != "us-state-CA" {
		t.Errorf("layers %v", layers)
	}
	for _, l := range layers {
		if _, leaked := l.(map[string]any)["schedules"]; leaked {
			t.Errorf("a layer's rates were sent: %v", l)
		}
	}
	result := body["results"].([]any)[0].(map[string]any)
	if result["socialSecurity"] != 9300.0 || result["total"].(float64) < 40_000 {
		t.Errorf("result %v", result)
	}
	expectStatus(t, do(t, srv, "POST", "/api/v1/taxes", `{"cases":[]}`), 400)
	expectStatus(t, do(t, srv, "POST", "/api/v1/taxes", `{}`, anonymousHeader, "1"), 401)
	r = do(t, srv, "GET", "/api/v1/taxes/years", "")
	expectStatus(t, r, 200)
	if r.json(t)["firstYear"] != 1970.0 {
		t.Errorf("years %s", r.body)
	}
}
