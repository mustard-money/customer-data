// Package taxes computes estimated taxes for signed-in customers
// (POST /api/v1/taxes) from the tax tables in this server's taxtables
// package. Only results are returned: the tables (brackets, rates,
// deductions, ZIP coverage) never leave the server.
package taxes

import (
	"errors"
	"fmt"
	"math"

	"github.com/mustard-money/customer-data/fields"
	"github.com/mustard-money/customer-data/taxtables"
)

// MaxCases caps the incomes one request may ask about (a 60-year projection
// with its sensitivity runs needs about 660).
const MaxCases = 2000

// maxAmount bounds every amount: well beyond any household, and finite.
const maxAmount = 1e12

// Case is one year of a household's income, as the taxes on it need it.
// Amounts are yearly dollars.
type Case struct {
	// PayrollWages: W-2 wages owing Social Security and Medicare.
	PayrollWages float64 `json:"payrollWages"`
	// Income: what income tax applies to under the federal rules (W-2 box 1
	// wages plus other taxable income), before self-employment income.
	Income float64 `json:"income"`
	// HSA: payroll HSA contributions (employee + employer), which California
	// and New Jersey tax as wages.
	HSA float64 `json:"hsa,omitempty"`
	// Deferrals: traditional 401(k)/403(b)/457(b) contributions, which
	// Pennsylvania taxes as wages.
	Deferrals float64 `json:"deferrals,omitempty"`
	// SelfEmployment: net self-employment earnings, owing self-employment
	// tax (half of which comes off income for income tax).
	SelfEmployment float64 `json:"selfEmployment,omitempty"`
	// CapitalGains: net long-term capital gains and qualified dividends,
	// taxed federally at the year's capital gains rates (stacked on top of
	// Income) and by states and localities as ordinary income. They also
	// count toward the net investment income tax.
	CapitalGains float64 `json:"capitalGains,omitempty"`
	// QualifiedDividends: dividends taxed like CapitalGains from 2003 (and
	// as ordinary investment income, like Interest, before then).
	QualifiedDividends float64 `json:"qualifiedDividends,omitempty"`
	// Interest: interest and other investment income taxed at ordinary
	// rates. Like Income for income tax, but its tax counts as investment
	// tax and it owes the net investment income tax.
	Interest float64 `json:"interest,omitempty"`
	// InterestExemptFederal, InterestExemptState, InterestExemptLocal: the
	// parts of Interest exempt from federal, state and local income tax (e.g.
	// municipal bonds federally, Treasuries from state and local tax).
	// Federal-exempt interest isn't net investment income either.
	InterestExemptFederal float64 `json:"interestExemptFederal,omitempty"`
	InterestExemptState   float64 `json:"interestExemptState,omitempty"`
	InterestExemptLocal   float64 `json:"interestExemptLocal,omitempty"`
	// EarlyDistributions: the part of Income that is retirement plan or IRA
	// distributions before age 59½, owing the 10% additional tax.
	EarlyDistributions float64 `json:"earlyDistributions,omitempty"`
	// HSANonMedical: the part of Income that is HSA distributions not used
	// for medical expenses before age 65, owing the HSA additional tax.
	HSANonMedical float64 `json:"hsaNonMedical,omitempty"`
}

// Request asks for the taxes on some incomes under one year's rules.
type Request struct {
	TaxYear *int `json:"taxYear"`
	// PostalCode: the household's ZIP code; empty for federal tax alone.
	PostalCode   string  `json:"postalCode"`
	FilingStatus *string `json:"filingStatus"`
	Cases        []Case  `json:"cases"`
}

// Layer names a tax a result has an amount for, without its rates.
type Layer struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Region   string `json:"region,omitempty"`
	Coverage string `json:"coverage,omitempty"`
}

// Result is the taxes on one case.
type Result struct {
	// IncomeTax: one amount per Response.Layers entry.
	IncomeTax []float64 `json:"incomeTax"`
	// InvestmentTax: for each layer, the part of its IncomeTax that the
	// case's CapitalGains, QualifiedDividends and Interest add.
	InvestmentTax []float64 `json:"investmentTax"`
	// NetInvestmentIncomeTax: the federal 3.8% tax on investment income
	// (CapitalGains, QualifiedDividends, Interest) above the income
	// threshold (from 2013). Included in Total.
	NetInvestmentIncomeTax float64 `json:"netInvestmentIncomeTax"`
	// AdditionalTax: the additional taxes on EarlyDistributions and
	// HSANonMedical. Included in Total.
	AdditionalTax  float64 `json:"additionalTax"`
	SocialSecurity float64 `json:"socialSecurity"`
	Medicare       float64 `json:"medicare"`
	SelfEmployment float64 `json:"selfEmployment"`
	Total          float64 `json:"total"`
}

// Response holds the taxes on each case, in order.
type Response struct {
	TaxYear int `json:"taxYear"`
	// Projected: the year's rules are indexed forward from the last
	// published year rather than published.
	Projected bool    `json:"projected"`
	Layers    []Layer `json:"layers"`
	// UnknownPostalCode: the ZIP code isn't in any state's coverage, so only
	// federal tax is included.
	UnknownPostalCode bool `json:"unknownPostalCode,omitempty"`
	// StateMissing: the state's tax that year isn't known, so it (and any
	// local tax) is left out and totals understate the tax.
	StateMissing string `json:"stateMissing,omitempty"`
	// MissingLocals: local taxes in the ZIP code whose rules that year
	// aren't known (left out).
	MissingLocals []string `json:"missingLocals,omitempty"`
	// CapitalGainsMissing: the year's federal capital gains rules aren't
	// known, so capital gains are taxed as ordinary income.
	CapitalGainsMissing bool     `json:"capitalGainsMissing,omitempty"`
	Results             []Result `json:"results"`
}

// States whose taxable wages differ from the federal ones.
var (
	statesTaxingHSA       = map[string]bool{"us-state-CA": true, "us-state-NJ": true}
	statesTaxingDeferrals = map[string]bool{"us-state-PA": true}
)

func validAmount(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= maxAmount }

// Compute validates req and works out the taxes on each case.
func Compute(t *taxtables.Tables, req Request) (*Response, error) {
	first, _, last := t.Years()
	if req.TaxYear == nil || *req.TaxYear < first || *req.TaxYear > last {
		return nil, fields.Invalid("taxYear must be from %d to %d", first, last)
	}
	if req.FilingStatus == nil || (*req.FilingStatus != string(taxtables.Single) && *req.FilingStatus != string(taxtables.MarriedJoint)) {
		return nil, fields.Invalid("filingStatus must be single or marriedJoint")
	}
	if len(req.Cases) == 0 || len(req.Cases) > MaxCases {
		return nil, fields.Invalid("cases must hold 1 to %d incomes", MaxCases)
	}
	for i, c := range req.Cases {
		for _, v := range []float64{c.PayrollWages, c.Income, c.HSA, c.Deferrals, c.SelfEmployment, c.CapitalGains, c.QualifiedDividends, c.Interest, c.EarlyDistributions, c.HSANonMedical,
			c.InterestExemptFederal, c.InterestExemptState, c.InterestExemptLocal} {
			if !validAmount(v) {
				return nil, fields.Invalid("cases[%d]: amounts must be from 0 to %.0f", i, maxAmount)
			}
		}
		if c.InterestExemptFederal > c.Interest || c.InterestExemptState > c.Interest || c.InterestExemptLocal > c.Interest {
			return nil, fields.Invalid("cases[%d]: exempt interest can't exceed interest", i)
		}
	}
	year, status := *req.TaxYear, taxtables.Status(*req.FilingStatus)

	resp := &Response{TaxYear: year}
	lookup, err := t.For(req.PostalCode, year)
	if errors.Is(err, taxtables.ErrUnknownZip) {
		resp.UnknownPostalCode = true
		lookup, err = t.For("", year)
	}
	if err != nil {
		return nil, fmt.Errorf("tax tables for %d: %w", year, err)
	}
	payroll, payrollProjected := t.Payroll(year)
	if payroll == nil {
		return nil, fmt.Errorf("no payroll parameters for %d", year)
	}
	resp.StateMissing, resp.MissingLocals = lookup.StateMissing, lookup.MissingLocals
	gains, gainsProjected := t.CapitalGains(year)
	resp.CapitalGainsMissing = gains == nil
	resp.Projected = payrollProjected || gainsProjected
	for _, l := range lookup.Layers {
		resp.Layers = append(resp.Layers, Layer{ID: l.ID, Kind: l.Kind, Region: l.Region, Coverage: l.Coverage})
		resp.Projected = resp.Projected || l.Projected
	}
	resp.Results = make([]Result, len(req.Cases))
	for i, c := range req.Cases {
		resp.Results[i] = compute(year, lookup.Layers, payroll, gains, status, c)
	}
	return resp, nil
}

func compute(year int, layers []taxtables.Layer, p *taxtables.Payroll, gains *taxtables.CapitalGains, status taxtables.Status, c Case) Result {
	se := p.SelfEmploymentTax(c.SelfEmployment, c.PayrollWages, status)
	// Investment income at the capital gains rates, and at ordinary rates.
	preferential, ordinaryInvestment := c.CapitalGains, c.Interest
	if year >= taxtables.QualifiedDividendsFrom {
		preferential += c.QualifiedDividends
	} else {
		ordinaryInvestment += c.QualifiedDividends
	}
	base := c.Income + c.SelfEmployment - se.DeductibleHalf
	r := Result{
		IncomeTax:      make([]float64, len(layers)),
		InvestmentTax:  make([]float64, len(layers)),
		SelfEmployment: se.Total,
	}
	for i, l := range layers {
		income := base
		if statesTaxingHSA[l.ID] {
			income += c.HSA
		}
		if statesTaxingDeferrals[l.ID] {
			income += c.Deferrals
		}
		income = math.Max(0, income)
		without := taxtables.LayerTax(l.Tax, income, status)
		// Interest this layer doesn't tax.
		exempt := c.InterestExemptLocal
		switch l.Kind {
		case "federal":
			exempt = c.InterestExemptFederal
		case "state":
			exempt = c.InterestExemptState
		}
		taxedInvestment := ordinaryInvestment - exempt
		if l.Kind == "federal" {
			r.IncomeTax[i], _ = taxtables.FederalTax(l.Tax, gains, income+taxedInvestment, preferential, status)
		} else {
			// States and localities tax all investment income as ordinary income.
			r.IncomeTax[i] = taxtables.LayerTax(l.Tax, income+taxedInvestment+preferential, status)
		}
		r.InvestmentTax[i] = r.IncomeTax[i] - without
		r.Total += r.IncomeTax[i]
	}
	// Modified AGI, as far as these cases know it: income plus investment income.
	investment := preferential + ordinaryInvestment - c.InterestExemptFederal
	r.NetInvestmentIncomeTax = gains.NetInvestmentIncome(investment, base+investment, status)
	r.AdditionalTax = taxtables.EarlyDistributionRate(year)*c.EarlyDistributions +
		taxtables.HSANonMedicalRate(year)*c.HSANonMedical
	r.SocialSecurity, r.Medicare = p.PayrollParts(c.PayrollWages, status)
	r.Total += r.NetInvestmentIncomeTax + r.AdditionalTax + r.SocialSecurity + r.Medicare + r.SelfEmployment
	return r
}
