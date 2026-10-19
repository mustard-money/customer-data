package taxtables

import "math"

func pick(v *ByStatus[float64], status Status) float64 {
	if status == MarriedJoint && v.MarriedJoint != nil {
		return *v.MarriedJoint
	}
	return v.Single
}

// ScheduleTax is the tax on taxable income under a marginal-rate schedule.
func ScheduleTax(schedule []Bracket, taxable float64) float64 {
	tax := 0.0
	for i, b := range schedule {
		top := math.Inf(1)
		if i+1 < len(schedule) {
			top = schedule[i+1].From
		}
		if taxable > b.From {
			tax += (math.Min(taxable, top) - b.From) * b.Rate
		}
	}
	return tax
}

// Deduction is what a brackets or flat tax lets off income before its rates:
// the fixed deduction plus any percentage standard deduction.
func (t *Tax) DeductionFor(income float64, status Status) float64 {
	d := 0.0
	if t.Deduction != nil {
		d = pick(t.Deduction, status)
	}
	if p := t.PercentStandardDeduction; p != nil {
		d += math.Min(math.Max(p.Rate*income, pick(&p.Minimum, status)), pick(&p.Maximum, status))
	}
	return d
}

// LayerTax is a layer's income tax on a year's income.
func LayerTax(t *Tax, income float64, status Status) float64 {
	switch t.Type {
	case "brackets":
		schedule := t.Schedules.Single
		if status == MarriedJoint && t.Schedules.MarriedJoint != nil {
			schedule = *t.Schedules.MarriedJoint
		}
		return ScheduleTax(schedule, math.Max(0, income-t.DeductionFor(income, status)))
	case "flat":
		return math.Max(0, income-t.DeductionFor(income, status)) * *t.Rate
	case "fixed":
		periods := 1.0
		if t.Per == "month" {
			periods = 12
		}
		minimum := 0.0
		if t.MinimumEarnings != nil {
			minimum = *t.MinimumEarnings
		}
		if income/periods >= minimum {
			return *t.Amount * periods
		}
	}
	return 0
}

func (p *Payroll) additionalThreshold(status Status) float64 {
	if p.AdditionalMedicareRate > 0 && p.AdditionalMedicareThreshold != nil {
		return pick(p.AdditionalMedicareThreshold, status)
	}
	return math.Inf(1)
}

func (p *Payroll) medicareCap() float64 {
	if p.MedicareWageBase != nil {
		return *p.MedicareWageBase
	}
	return math.Inf(1)
}

// PayrollParts is the employee's Social Security and Medicare (including
// Additional Medicare Tax) on wages.
func (p *Payroll) PayrollParts(wages float64, status Status) (socialSecurity, medicare float64) {
	socialSecurity = p.SocialSecurityRate * math.Min(wages, p.WageBase)
	medicare = p.MedicareRate*math.Min(wages, p.medicareCap()) +
		p.AdditionalMedicareRate*math.Max(0, wages-p.additionalThreshold(status))
	return socialSecurity, medicare
}

// SelfEmployment is IRS Schedule SE tax on net self-employment earnings for
// someone who also has wages (which use up the Social Security wage base
// first).
type SelfEmployment struct {
	SocialSecurity, Medicare, Total float64
	// DeductibleHalf (half the Social Security and regular Medicare) comes
	// off income for income tax.
	DeductibleHalf float64
}

// SelfEmploymentTax: 92.35% of earnings taxed at twice the employee Social
// Security rate (up to the wage base) and twice the Medicare rate, plus the
// Additional Medicare rate above the threshold; nothing below $400. Before
// 1990 the self-employment rates weren't exactly double; this is close.
func (p *Payroll) SelfEmploymentTax(earnings, wages float64, status Status) SelfEmployment {
	base := math.Max(0, earnings) * 0.9235
	if base < 400 {
		return SelfEmployment{}
	}
	ss := 2 * p.SocialSecurityRate * math.Min(base, math.Max(0, p.WageBase-wages))
	regular := 2 * p.MedicareRate * math.Min(base, math.Max(0, p.medicareCap()-wages))
	// Only the part of the threshold that wages haven't already used.
	additional := p.AdditionalMedicareRate * math.Max(0, base-math.Max(0, p.additionalThreshold(status)-wages))
	medicare := regular + additional
	return SelfEmployment{SocialSecurity: ss, Medicare: medicare, Total: ss + medicare, DeductibleHalf: (ss + regular) / 2}
}

// LayerAmount is one layer's share of a computed tax.
type LayerAmount struct {
	Layer  Layer
	Amount float64
}

// Breakdown is the taxes on a year's income.
type Breakdown struct {
	ByLayer   []LayerAmount
	IncomeTax float64
	// Payroll: employee Social Security and Medicare on wages.
	Payroll float64
	Total   float64
}

// Compute is the taxes on wages (payroll tax) and ordinary income (income
// tax, every layer on the same income; ordinary may include, e.g.,
// traditional IRA withdrawals). Simplified: standard deductions only, no
// credits, no capital gains rates.
func Compute(layers []Layer, p *Payroll, wages, ordinary float64, status Status) Breakdown {
	var b Breakdown
	for _, l := range layers {
		amount := LayerTax(l.Tax, math.Max(0, ordinary), status)
		b.ByLayer = append(b.ByLayer, LayerAmount{l, amount})
		b.IncomeTax += amount
	}
	ss, medicare := p.PayrollParts(math.Max(0, wages), status)
	b.Payroll = ss + medicare
	b.Total = b.IncomeTax + b.Payroll
	return b
}
