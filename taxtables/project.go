package taxtables

import (
	"math"
	"sort"
	"strconv"
)

// The projection: years after the last transcribed one are indexed the way
// the IRS has indexed federal brackets since 2018 (by chained CPI-U, rounded
// down to $50), with an assumed inflation rate. See data/README.md.
const (
	// ProjectionVersion changes whenever the rules below do, so the loader
	// reloads the tables.
	ProjectionVersion = "chained-cpi-2.3%-v2-capital-gains"
	// AssumedInflation is the yearly chained CPI-U assumed for projections.
	AssumedInflation = 0.023
	// ProjectThrough is the last projected year.
	ProjectThrough   = 2100
	bracketRounding  = 50
	wageBaseRounding = 300
)

// floorTo rounds v down to a multiple of step.
func floorTo(v, step float64) float64 { return math.Floor(v/step+1e-9) * step }

// indexed grows v by AssumedInflation for n years, rounded down to step.
func indexed(v float64, n int, step float64) float64 {
	return floorTo(v*math.Pow(1+AssumedInflation, float64(n)), step)
}

func indexByStatus(v *ByStatus[float64], n int) *ByStatus[float64] {
	if v == nil {
		return nil
	}
	out := &ByStatus[float64]{Single: indexed(v.Single, n, bracketRounding)}
	if v.MarriedJoint != nil {
		m := indexed(*v.MarriedJoint, n, bracketRounding)
		out.MarriedJoint = &m
	}
	return out
}

func indexSchedule(s []Bracket, n int) []Bracket {
	out := make([]Bracket, len(s))
	for i, b := range s {
		out[i] = Bracket{From: indexed(b.From, n, bracketRounding), Rate: b.Rate}
		// Rounding can't merge brackets: keep each above the one before.
		if i > 0 && out[i].From <= out[i-1].From {
			out[i].From = out[i-1].From + bracketRounding
		}
	}
	return out
}

// ProjectTax is base's tax n years later: bracket starts and fixed deductions
// indexed; rates, flat and fixed taxes unchanged.
func ProjectTax(base *Tax, n int) *Tax {
	t := *base
	if base.Schedules != nil {
		s := &ByStatus[[]Bracket]{Single: indexSchedule(base.Schedules.Single, n)}
		if base.Schedules.MarriedJoint != nil {
			m := indexSchedule(*base.Schedules.MarriedJoint, n)
			s.MarriedJoint = &m
		}
		t.Schedules = s
	}
	t.Deduction = indexByStatus(base.Deduction, n)
	if p := base.PercentStandardDeduction; p != nil {
		t.PercentStandardDeduction = &PercentDeduction{
			Rate:    p.Rate,
			Minimum: *indexByStatus(&p.Minimum, n),
			Maximum: *indexByStatus(&p.Maximum, n),
		}
	}
	return &t
}

// ProjectPayroll is base's payroll parameters n years later: the Social
// Security wage base indexed (rounded down to $300); rates and the
// Additional Medicare thresholds (which aren't indexed) unchanged.
func ProjectPayroll(base *Payroll, n int) *Payroll {
	p := *base
	p.WageBase = indexed(base.WageBase, n, wageBaseRounding)
	if base.MedicareWageBase != nil {
		m := indexed(*base.MedicareWageBase, n, wageBaseRounding)
		p.MedicareWageBase = &m
	}
	return &p
}

// YearTax is a jurisdiction's tax in one year, transcribed or projected.
type YearTax struct {
	Year      int
	Tax       *Tax
	Projected bool
}

// LastActualYear is the latest transcribed year across the federal and
// state data: the year projections start after.
func (d *Data) LastActualYear() int {
	last := 0
	for _, j := range d.Jurisdictions {
		if j.Kind == "local" {
			continue
		}
		for _, y := range j.years() {
			last = max(last, y)
		}
	}
	return last
}

func (j *Jurisdiction) years() []int {
	years := make([]int, 0, len(j.Years))
	for key := range j.Years {
		y, _ := strconv.Atoi(key) // validated by Parse
		years = append(years, y)
	}
	sort.Ints(years)
	return years
}

// Expand lists every year of j's tax: the transcribed years, then, for a
// jurisdiction that has a tax in the last actual year, that year projected
// through ProjectThrough.
func (d *Data) Expand(j *Jurisdiction) []YearTax {
	last := d.LastActualYear()
	var out []YearTax
	for _, y := range j.years() {
		out = append(out, YearTax{Year: y, Tax: j.Years[strconv.Itoa(y)]})
	}
	if base, ok := j.Years[strconv.Itoa(last)]; ok {
		for y := last + 1; y <= ProjectThrough; y++ {
			if _, transcribed := j.Years[strconv.Itoa(y)]; transcribed {
				continue
			}
			out = append(out, YearTax{Year: y, Tax: ProjectTax(base, y-last), Projected: true})
		}
	}
	return out
}

// PayrollYear is a year's payroll parameters, transcribed or projected.
type PayrollYear struct {
	Year      int
	Payroll   *Payroll
	Projected bool
}

// ExpandPayroll lists the transcribed payroll years, then the last one
// projected through ProjectThrough.
func (d *Data) ExpandPayroll() []PayrollYear {
	years := make([]int, 0, len(d.Payroll))
	for y := range d.Payroll {
		years = append(years, y)
	}
	sort.Ints(years)
	var out []PayrollYear
	for _, y := range years {
		out = append(out, PayrollYear{Year: y, Payroll: d.Payroll[y]})
	}
	if len(years) > 0 {
		last := years[len(years)-1]
		for y := last + 1; y <= ProjectThrough; y++ {
			out = append(out, PayrollYear{Year: y, Payroll: ProjectPayroll(d.Payroll[last], y-last), Projected: true})
		}
	}
	return out
}
