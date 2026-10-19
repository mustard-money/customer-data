package taxes

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mustard-money/customer-data/taxtables"
)

func tables(t *testing.T) *taxtables.Tables {
	t.Helper()
	d, err := taxtables.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return taxtables.NewTables(d)
}

func ptr[T any](v T) *T { return &v }

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

// testdata/golden.json holds results from the website's TypeScript tax code
// (before it moved here) for incomes across years, ZIP codes and filing
// statuses; the Go port must give the same taxes. When a projected year is
// later published, only the figures it changes are updated: Social Security
// for 2027 on now uses SSA's published 2027 wage base ($192,300) instead of
// the one projected from 2026.
func TestMatchesTheWebsitesFormerTaxCode(t *testing.T) {
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var groups []struct {
		TaxYear      int      `json:"taxYear"`
		PostalCode   string   `json:"postalCode"`
		FilingStatus string   `json:"filingStatus"`
		Layers       []string `json:"layers"`
		Cases        []struct {
			Case           Case      `json:"case"`
			IncomeTax      []float64 `json:"incomeTax"`
			SocialSecurity float64   `json:"socialSecurity"`
			Medicare       float64   `json:"medicare"`
			SelfEmployment float64   `json:"selfEmployment"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &groups); err != nil {
		t.Fatal(err)
	}
	tb := tables(t)
	checked := 0
	for _, g := range groups {
		req := Request{TaxYear: ptr(g.TaxYear), PostalCode: g.PostalCode, FilingStatus: ptr(g.FilingStatus)}
		for _, c := range g.Cases {
			req.Cases = append(req.Cases, c.Case)
		}
		resp, err := Compute(tb, req)
		if err != nil {
			t.Fatalf("%d %s: %v", g.TaxYear, g.PostalCode, err)
		}
		var ids []string
		for _, l := range resp.Layers {
			ids = append(ids, l.ID)
		}
		if strings.Join(ids, ",") != strings.Join(g.Layers, ",") {
			t.Errorf("%d %s: layers %v, want %v", g.TaxYear, g.PostalCode, ids, g.Layers)
			continue
		}
		for i, want := range g.Cases {
			got := resp.Results[i]
			for j := range want.IncomeTax {
				if !near(got.IncomeTax[j], want.IncomeTax[j]) {
					t.Errorf("%d %s %s case %d %s: %v, want %v", g.TaxYear, g.PostalCode, g.FilingStatus, i, ids[j], got.IncomeTax[j], want.IncomeTax[j])
				}
			}
			if !near(got.SocialSecurity, want.SocialSecurity) || !near(got.Medicare, want.Medicare) || !near(got.SelfEmployment, want.SelfEmployment) {
				t.Errorf("%d %s %s case %d payroll: %+v, want ss %v medicare %v se %v", g.TaxYear, g.PostalCode, g.FilingStatus, i, got, want.SocialSecurity, want.Medicare, want.SelfEmployment)
			}
			checked++
		}
	}
	if checked < 500 {
		t.Errorf("only %d cases checked", checked)
	}
}

func TestCompute(t *testing.T) {
	tb := tables(t)
	// $150,000 of wages, single, 2026, no ZIP: federal 24,734 (hand-worked in
	// the website's tests), Social Security 9,300, Medicare 2,175.
	resp, err := Compute(tb, Request{
		TaxYear: ptr(2026), FilingStatus: ptr("single"),
		Cases: []Case{{PayrollWages: 150_000, Income: 150_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := resp.Results[0]
	if len(resp.Layers) != 1 || resp.Layers[0].ID != "us-federal" || !near(r.IncomeTax[0], 24_734) ||
		!near(r.SocialSecurity, 9_300) || !near(r.Medicare, 2_175) || !near(r.Total, 24_734+9_300+2_175) {
		t.Errorf("got %+v %+v", resp.Layers, r)
	}
	if resp.Projected {
		t.Error("2026 isn't projected")
	}

	// Later years are projected; an unmapped ZIP falls back to federal and says so.
	resp, err = Compute(tb, Request{TaxYear: ptr(2040), PostalCode: "00000", FilingStatus: ptr("marriedJoint"),
		Cases: []Case{{Income: 50_000}}})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Projected || !resp.UnknownPostalCode || len(resp.Layers) != 1 {
		t.Errorf("2040 00000: %+v", resp)
	}
}

func TestComputeRejects(t *testing.T) {
	tb := tables(t)
	ok := Request{TaxYear: ptr(2026), FilingStatus: ptr("single"), Cases: []Case{{Income: 1}}}
	for name, c := range map[string]struct {
		mutate func(*Request)
		want   string
	}{
		"no year":         {func(r *Request) { r.TaxYear = nil }, "taxYear"},
		"year too late":   {func(r *Request) { r.TaxYear = ptr(2101) }, "taxYear"},
		"bad status":      {func(r *Request) { r.FilingStatus = ptr("head") }, "filingStatus"},
		"no cases":        {func(r *Request) { r.Cases = nil }, "cases"},
		"too many":        {func(r *Request) { r.Cases = make([]Case, MaxCases+1) }, "cases"},
		"negative amount": {func(r *Request) { r.Cases = []Case{{Income: -1}} }, "amounts"},
		"NaN":             {func(r *Request) { r.Cases = []Case{{HSA: math.NaN()}} }, "amounts"},
	} {
		t.Run(name, func(t *testing.T) {
			r := ok
			c.mutate(&r)
			if _, err := Compute(tb, r); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestComputeInvestmentIncome(t *testing.T) {
	tb := tables(t)
	compute := func(zip string, c Case) (*Response, Result) {
		t.Helper()
		resp, err := Compute(tb, Request{TaxYear: ptr(2025), PostalCode: zip, FilingStatus: ptr("single"), Cases: []Case{c}})
		if err != nil {
			t.Fatal(err)
		}
		return resp, resp.Results[0]
	}

	// 2025, single: long-term gains are taxed at 0% up to $48,350 of taxable
	// income. $50,000 of gains alone, less the standard deduction, owes nothing.
	resp, r := compute("", Case{CapitalGains: 50_000})
	if resp.CapitalGainsMissing || r.IncomeTax[0] != 0 || r.InvestmentTax[0] != 0 || r.NetInvestmentIncomeTax != 0 {
		t.Errorf("gains alone: %+v %+v", resp, r)
	}

	// On top of $200,000 of wages the gains fall in the 15% band (it runs to
	// $533,400): $15,000. Modified AGI of $300,000 is $100,000 over the
	// threshold, so all $100,000 owes the 3.8% net investment income tax.
	_, without := compute("", Case{PayrollWages: 200_000, Income: 200_000})
	_, r = compute("", Case{PayrollWages: 200_000, Income: 200_000, CapitalGains: 100_000})
	if !near(r.InvestmentTax[0], 15_000) || !near(r.IncomeTax[0], without.IncomeTax[0]+15_000) || !near(r.NetInvestmentIncomeTax, 3_800) {
		t.Errorf("gains on wages: %+v (without %+v)", r, without)
	}
	if !near(r.Total, without.Total+15_000+3_800) {
		t.Errorf("total %v, want %v", r.Total, without.Total+18_800)
	}

	// Interest is ordinary income: $10,000 on top of $100,000 stays in the
	// 22% bracket, $2,200, all of it investment tax; no NIIT below $200,000.
	_, r = compute("", Case{Income: 100_000, Interest: 10_000})
	_, plain := compute("", Case{Income: 110_000})
	if !near(r.InvestmentTax[0], 2_200) || !near(r.IncomeTax[0], plain.IncomeTax[0]) || r.NetInvestmentIncomeTax != 0 {
		t.Errorf("interest: %+v (as income %+v)", r, plain)
	}

	// States tax gains as ordinary income: California's investment part is
	// its tax with the gains less its tax without.
	resp, r = compute("94103", Case{Income: 100_000, CapitalGains: 20_000})
	_, asIncome := compute("94103", Case{Income: 120_000})
	_, noGains := compute("94103", Case{Income: 100_000})
	if len(resp.Layers) < 2 || resp.Layers[1].ID != "us-state-CA" {
		t.Fatalf("layers %+v", resp.Layers)
	}
	if !near(r.IncomeTax[1], asIncome.IncomeTax[1]) || !near(r.InvestmentTax[1], asIncome.IncomeTax[1]-noGains.IncomeTax[1]) || r.InvestmentTax[1] <= 0 {
		t.Errorf("California: %+v", r)
	}
}

func TestComputeQualifiedDividends(t *testing.T) {
	tb := tables(t)
	compute := func(year int, c Case) Result {
		t.Helper()
		resp, err := Compute(tb, Request{TaxYear: ptr(year), FilingStatus: ptr("single"), Cases: []Case{c}})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Results[0]
	}
	// 2025: $10,000 of qualified dividends on $100,000 of income fall in the
	// 15% band, like long-term gains: $1,500.
	r := compute(2025, Case{Income: 100_000, QualifiedDividends: 10_000})
	if gains := compute(2025, Case{Income: 100_000, CapitalGains: 10_000}); !near(r.InvestmentTax[0], 1_500) || !near(r.IncomeTax[0], gains.IncomeTax[0]) {
		t.Errorf("2025: %+v (as gains %+v)", r, gains)
	}
	// 2002: dividends were ordinary income, unlike gains (capped at 20%).
	r = compute(2002, Case{Income: 100_000, QualifiedDividends: 10_000})
	interest := compute(2002, Case{Income: 100_000, Interest: 10_000})
	gains := compute(2002, Case{Income: 100_000, CapitalGains: 10_000})
	if !near(r.IncomeTax[0], interest.IncomeTax[0]) || r.IncomeTax[0] <= gains.IncomeTax[0] {
		t.Errorf("2002: %+v (as interest %+v, as gains %+v)", r, interest, gains)
	}
}

func TestComputeAdditionalTaxes(t *testing.T) {
	tb := tables(t)
	resp, err := Compute(tb, Request{TaxYear: ptr(2026), FilingStatus: ptr("single"), Cases: []Case{
		{Income: 50_000},
		// $20,000 withdrawn from a traditional IRA at 50 and $5,000 from an HSA
		// for non-medical costs: both are income, plus 10% and 20%.
		{Income: 75_000, EarlyDistributions: 20_000, HSANonMedical: 5_000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	plain, early := resp.Results[0], resp.Results[1]
	if !near(early.AdditionalTax, 2_000+1_000) {
		t.Errorf("additional tax %v, want 3,000", early.AdditionalTax)
	}
	if plain.AdditionalTax != 0 || !near(early.Total, early.IncomeTax[0]+early.AdditionalTax) {
		t.Errorf("plain %+v, early %+v", plain, early)
	}
}

func TestComputeExemptInterest(t *testing.T) {
	tb := tables(t)
	compute := func(c Case) Result {
		t.Helper()
		resp, err := Compute(tb, Request{TaxYear: ptr(2025), PostalCode: "94103", FilingStatus: ptr("single"), Cases: []Case{c}})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Results[0]
	}
	plain := compute(Case{Income: 100_000})
	taxable := compute(Case{Income: 100_000, Interest: 10_000})
	// A muni: exempt federally (and from the 3.8% tax), taxed by California.
	muni := compute(Case{Income: 100_000, Interest: 10_000, InterestExemptFederal: 10_000})
	if !near(muni.IncomeTax[0], plain.IncomeTax[0]) || !near(muni.IncomeTax[1], taxable.IncomeTax[1]) {
		t.Errorf("muni: %+v", muni)
	}
	// A Treasury: taxed federally, exempt from state tax.
	treasury := compute(Case{Income: 100_000, Interest: 10_000, InterestExemptState: 10_000, InterestExemptLocal: 10_000})
	if !near(treasury.IncomeTax[0], taxable.IncomeTax[0]) || !near(treasury.IncomeTax[1], plain.IncomeTax[1]) {
		t.Errorf("treasury: %+v", treasury)
	}
	if !near(treasury.InvestmentTax[1], 0) || !near(muni.InvestmentTax[0], 0) {
		t.Errorf("exempt layers add no investment tax: %+v %+v", muni, treasury)
	}
	// Federal-exempt interest isn't net investment income.
	high := compute(Case{Income: 300_000, Interest: 10_000, InterestExemptFederal: 10_000})
	if high.NetInvestmentIncomeTax != 0 {
		t.Errorf("NIIT on exempt interest: %v", high.NetInvestmentIncomeTax)
	}
	if _, err := Compute(tb, Request{TaxYear: ptr(2025), FilingStatus: ptr("single"),
		Cases: []Case{{Interest: 1, InterestExemptState: 2}}}); err == nil {
		t.Error("exempt above interest should be rejected")
	}
}
