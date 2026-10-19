package items

import (
	"math"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// Benefits are the workplace savings on a W-2 income item. Rates are fractions
// of gross pay (0.06 is 6%); HSA amounts are yearly dollars. Each plan's
// contributions are all Roth or all traditional. Stored as-is in the
// items.benefits jsonb column, so the JSON shape is both the API and the
// storage format.
type Benefits struct {
	Retirement *Retirement `json:"retirement,omitempty"`
	Plan457    *Plan457    `json:"plan457,omitempty"`
	HSA        *HSA        `json:"hsa,omitempty"`
}

// Balance is an account's balance on a date (no year means today's).
type Balance struct {
	Amount    float64 `json:"amount"`
	AsOfYear  *int    `json:"asOfYear,omitempty"`
	AsOfMonth *int    `json:"asOfMonth,omitempty"`
}

// BalanceInput is a balance in a request body.
type BalanceInput struct {
	Amount    *float64 `json:"amount"`
	AsOfYear  *int     `json:"asOfYear"`
	AsOfMonth *int     `json:"asOfMonth"`
}

func (in *BalanceInput) validate(name string) (*Balance, error) {
	if in == nil {
		return nil, nil
	}
	amount, err := dollars(in.Amount, name+".amount")
	if err != nil {
		return nil, err
	}
	b := &Balance{Amount: amount}
	if in.AsOfYear != nil {
		maxYear := time.Now().UTC().Year() + 1
		if *in.AsOfYear < 1900 || *in.AsOfYear > maxYear {
			return nil, fields.Invalid("%s.asOfYear must be between 1900 and %d", name, maxYear)
		}
		y := *in.AsOfYear
		b.AsOfYear = &y
	}
	if in.AsOfMonth != nil {
		if b.AsOfYear == nil {
			return nil, fields.Invalid("%s.asOfMonth requires asOfYear", name)
		}
		if *in.AsOfMonth < 1 || *in.AsOfMonth > 12 {
			return nil, fields.Invalid("%s.asOfMonth must be between 1 and 12", name)
		}
		m := *in.AsOfMonth
		b.AsOfMonth = &m
	}
	return b, nil
}

// Retirement is a 401(k) or 403(b) (they work the same) with the employer's
// match: MatchRate per dollar contributed, on contributions up to MatchUpTo
// of pay (1 means no limit).
type Retirement struct {
	ContributionRate float64  `json:"contributionRate"`
	Roth             bool     `json:"roth"`
	MatchRate        float64  `json:"matchRate"`
	MatchUpTo        float64  `json:"matchUpTo"`
	Balance          *Balance `json:"balance,omitempty"`
}

// Plan457 is a 457(b) plan.
type Plan457 struct {
	ContributionRate float64  `json:"contributionRate"`
	Roth             bool     `json:"roth"`
	Balance          *Balance `json:"balance,omitempty"`
}

// HSA is health savings account contributions made through payroll. Family
// coverage has the higher contribution limit.
type HSA struct {
	EmployeeAnnual float64  `json:"employeeAnnual"`
	EmployerAnnual float64  `json:"employerAnnual"`
	Family         bool     `json:"family"`
	Balance        *Balance `json:"balance,omitempty"`
}

// BenefitsInput is the request body's benefits object. Pointer fields let
// validation report missing values; unknown keys are rejected by the decoder.
type BenefitsInput struct {
	Retirement *RetirementInput `json:"retirement"`
	Plan457    *Plan457Input    `json:"plan457"`
	HSA        *HSAInput        `json:"hsa"`
}

// RetirementInput is benefits.retirement in a request body. roth defaults to false.
type RetirementInput struct {
	ContributionRate *float64      `json:"contributionRate"`
	Roth             *bool         `json:"roth"`
	MatchRate        *float64      `json:"matchRate"`
	MatchUpTo        *float64      `json:"matchUpTo"`
	Balance          *BalanceInput `json:"balance"`
}

// Plan457Input is benefits.plan457 in a request body. roth defaults to false.
type Plan457Input struct {
	ContributionRate *float64      `json:"contributionRate"`
	Roth             *bool         `json:"roth"`
	Balance          *BalanceInput `json:"balance"`
}

// HSAInput is benefits.hsa in a request body. family defaults to false.
type HSAInput struct {
	EmployeeAnnual *float64      `json:"employeeAnnual"`
	EmployerAnnual *float64      `json:"employerAnnual"`
	Family         *bool         `json:"family"`
	Balance        *BalanceInput `json:"balance"`
}

func (in *BenefitsInput) validate() (*Benefits, error) {
	out := &Benefits{}
	if r := in.Retirement; r != nil {
		ret := Retirement{Roth: flag(r.Roth)}
		for _, f := range []struct {
			dst  *float64
			src  *float64
			name string
			max  float64
		}{
			{&ret.ContributionRate, r.ContributionRate, "contributionRate", 1},
			{&ret.MatchRate, r.MatchRate, "matchRate", 2},
			{&ret.MatchUpTo, r.MatchUpTo, "matchUpTo", 1},
		} {
			v, err := rate(f.src, "benefits.retirement."+f.name, f.max)
			if err != nil {
				return nil, err
			}
			*f.dst = v
		}
		balance, err := r.Balance.validate("benefits.retirement.balance")
		if err != nil {
			return nil, err
		}
		ret.Balance = balance
		out.Retirement = &ret
	}
	if p := in.Plan457; p != nil {
		contribution, err := rate(p.ContributionRate, "benefits.plan457.contributionRate", 1)
		if err != nil {
			return nil, err
		}
		balance, err := p.Balance.validate("benefits.plan457.balance")
		if err != nil {
			return nil, err
		}
		out.Plan457 = &Plan457{ContributionRate: contribution, Roth: flag(p.Roth), Balance: balance}
	}
	if h := in.HSA; h != nil {
		employee, err := dollars(h.EmployeeAnnual, "benefits.hsa.employeeAnnual")
		if err != nil {
			return nil, err
		}
		employer, err := dollars(h.EmployerAnnual, "benefits.hsa.employerAnnual")
		if err != nil {
			return nil, err
		}
		balance, err := h.Balance.validate("benefits.hsa.balance")
		if err != nil {
			return nil, err
		}
		out.HSA = &HSA{EmployeeAnnual: employee, EmployerAnnual: employer, Family: flag(h.Family), Balance: balance}
	}
	if out.Retirement == nil && out.Plan457 == nil && out.HSA == nil {
		return nil, nil
	}
	return out, nil
}

func flag(v *bool) bool { return v != nil && *v }

func rate(v *float64, name string, max float64) (float64, error) {
	if v == nil {
		return 0, fields.Invalid("%s is required", name)
	}
	if math.IsNaN(*v) || *v < 0 || *v > max {
		return 0, fields.Invalid("%s must be between 0 and %g", name, max)
	}
	return *v, nil
}

func dollars(v *float64, name string) (float64, error) {
	if v == nil {
		return 0, fields.Invalid("%s is required", name)
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > maxAmount {
		return 0, fields.Invalid("%s must be between 0 and %.2f", name, maxAmount)
	}
	return math.Round(*v*100) / 100, nil
}
