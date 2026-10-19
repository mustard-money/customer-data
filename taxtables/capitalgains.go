package taxtables

import (
	"fmt"
	"math"
	"sort"
)

// QualifiedDividendsFrom is the first year qualified dividends were taxed
// like long-term capital gains: the Jobs and Growth Tax Relief Reconciliation
// Act of 2003 (P.L. 108-27, §302) applied the capital gains rates to
// dividends received after 2002. Before that, dividends were ordinary income.
const QualifiedDividendsFrom = 2003

// CapitalGainsTier is one preferential rate on long-term gains in a year
// whose thresholds follow the ordinary brackets (1997–2017): it applies from
// From (only 0, for the first tier) or from where the ordinary bracket taxed
// at FromOrdinaryRate starts.
type CapitalGainsTier struct {
	Rate             float64  `json:"rate"`
	From             *float64 `json:"from,omitempty"`
	FromOrdinaryRate *float64 `json:"fromOrdinaryRate,omitempty"`
}

// NetInvestmentIncomeTax is the 3.8% tax on investment income above a
// modified-AGI threshold (from 2013; the thresholds aren't indexed).
type NetInvestmentIncomeTax struct {
	Rate      float64           `json:"rate"`
	Threshold ByStatus[float64] `json:"threshold"`
}

// CapitalGains is a year's federal treatment of net long-term capital gain
// (and, from 2003, qualified dividends), which is stacked on top of ordinary
// taxable income. Method decides which fields are set:
//   - "exclusion": ExcludedShare of the gain isn't income; the rest is
//     ordinary. Optionally the alternative tax: AlternativeRate on gains up
//     to AlternativeLimit (all of them without one, as in 1981), and
//     AlternativeExcessRate on gains beyond it.
//   - "maxRate": ordinary rates, but none above MaxRate.
//   - "ordinary": no preference.
//   - "brackets": preferential rates by where the gain falls in taxable
//     income: Tiers tied to the ordinary brackets, or Schedules in dollars.
type CapitalGains struct {
	Method                 string                  `json:"method"`
	ExcludedShare          *float64                `json:"excludedShare,omitempty"`
	AlternativeRate        *float64                `json:"alternativeRate,omitempty"`
	AlternativeLimit       *ByStatus[float64]      `json:"alternativeLimit,omitempty"`
	AlternativeExcessRate  *float64                `json:"alternativeExcessRate,omitempty"`
	MaxRate                *float64                `json:"maxRate,omitempty"`
	Tiers                  []CapitalGainsTier      `json:"tiers,omitempty"`
	Schedules              *ByStatus[[]Bracket]    `json:"schedules,omitempty"`
	NetInvestmentIncomeTax *NetInvestmentIncomeTax `json:"netInvestmentIncomeTax,omitempty"`
	Source                 string                  `json:"source"`
	Notes                  string                  `json:"notes,omitempty"`
}

func (c *CapitalGains) validate() error {
	if !validSource(c.Source) {
		return fmt.Errorf("source must be a URL")
	}
	set := func(fields ...bool) int {
		n := 0
		for _, f := range fields {
			if f {
				n++
			}
		}
		return n
	}
	alternative := set(c.AlternativeRate != nil, c.AlternativeLimit != nil, c.AlternativeExcessRate != nil)
	switch c.Method {
	case "exclusion":
		if c.ExcludedShare == nil || !validRate(*c.ExcludedShare) {
			return fmt.Errorf("exclusion needs excludedShare from 0 to <1")
		}
		if alternative > 0 && c.AlternativeRate == nil {
			return fmt.Errorf("the alternative tax needs alternativeRate")
		}
		if c.AlternativeExcessRate != nil && c.AlternativeLimit == nil {
			return fmt.Errorf("alternativeExcessRate needs alternativeLimit")
		}
		if c.AlternativeRate != nil && !validRate(*c.AlternativeRate) ||
			c.AlternativeExcessRate != nil && !validRate(*c.AlternativeExcessRate) {
			return fmt.Errorf("alternative rates must be 0 to <1")
		}
		if err := validByStatus(c.AlternativeLimit, "alternativeLimit"); err != nil {
			return err
		}
		if set(c.MaxRate != nil, c.Tiers != nil, c.Schedules != nil) > 0 {
			return fmt.Errorf("exclusion takes only excludedShare and the alternative tax")
		}
	case "maxRate":
		if c.MaxRate == nil || !validRate(*c.MaxRate) {
			return fmt.Errorf("maxRate needs maxRate from 0 to <1")
		}
		if set(c.ExcludedShare != nil, alternative > 0, c.Tiers != nil, c.Schedules != nil) > 0 {
			return fmt.Errorf("maxRate takes only maxRate")
		}
	case "ordinary":
		if set(c.ExcludedShare != nil, alternative > 0, c.MaxRate != nil, c.Tiers != nil, c.Schedules != nil) > 0 {
			return fmt.Errorf("ordinary takes no rates")
		}
	case "brackets":
		if (c.Tiers == nil) == (c.Schedules == nil) {
			return fmt.Errorf("brackets need tiers or schedules (not both)")
		}
		if set(c.ExcludedShare != nil, alternative > 0, c.MaxRate != nil) > 0 {
			return fmt.Errorf("brackets take only tiers or schedules")
		}
		if c.Schedules != nil {
			if err := validSchedule(c.Schedules.Single); err != nil {
				return fmt.Errorf("single: %w", err)
			}
			if c.Schedules.MarriedJoint != nil {
				if err := validSchedule(*c.Schedules.MarriedJoint); err != nil {
					return fmt.Errorf("marriedJoint: %w", err)
				}
			}
		}
		for i, tier := range c.Tiers {
			if !validRate(tier.Rate) {
				return fmt.Errorf("tier %d: rate must be 0 to <1", i)
			}
			if i == 0 && (tier.From == nil || *tier.From != 0 || tier.FromOrdinaryRate != nil) {
				return fmt.Errorf("the first tier must start from 0")
			}
			if i > 0 && (tier.From != nil || tier.FromOrdinaryRate == nil || !validRate(*tier.FromOrdinaryRate)) {
				return fmt.Errorf("tier %d: later tiers start at an ordinary rate (fromOrdinaryRate)", i)
			}
		}
	default:
		return fmt.Errorf("method %q must be exclusion, maxRate, ordinary or brackets", c.Method)
	}
	if n := c.NetInvestmentIncomeTax; n != nil {
		if !validRate(n.Rate) {
			return fmt.Errorf("netInvestmentIncomeTax rate must be 0 to <1")
		}
		if err := validByStatus(&n.Threshold, "netInvestmentIncomeTax threshold"); err != nil {
			return err
		}
	}
	return nil
}

// checkTiers verifies that every tier's ordinary rate is one of the federal
// schedule's rates that year (so the tier has somewhere to start).
func (c *CapitalGains) checkTiers(federal *Tax) error {
	if len(c.Tiers) == 0 {
		return nil
	}
	if federal == nil || federal.Schedules == nil {
		return fmt.Errorf("tiers need that year's federal brackets")
	}
	schedules := [][]Bracket{federal.Schedules.Single}
	if federal.Schedules.MarriedJoint != nil {
		schedules = append(schedules, *federal.Schedules.MarriedJoint)
	}
	for _, s := range schedules {
		if _, err := c.preferentialSchedule(s, Single); err != nil {
			return err
		}
	}
	return nil
}

// preferentialSchedule is the year's preferential rates as a schedule over
// taxable income, given the ordinary schedule for the filing status.
func (c *CapitalGains) preferentialSchedule(ordinary []Bracket, status Status) ([]Bracket, error) {
	if c.Schedules != nil {
		if status == MarriedJoint && c.Schedules.MarriedJoint != nil {
			return *c.Schedules.MarriedJoint, nil
		}
		return c.Schedules.Single, nil
	}
	out := make([]Bracket, 0, len(c.Tiers))
	for i, tier := range c.Tiers {
		if i == 0 {
			out = append(out, Bracket{From: 0, Rate: tier.Rate})
			continue
		}
		at := -1
		for j, b := range ordinary {
			if math.Abs(b.Rate-*tier.FromOrdinaryRate) < 1e-9 {
				at = j
				break
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("tier %d: no ordinary bracket at rate %v", i, *tier.FromOrdinaryRate)
		}
		out = append(out, Bracket{From: ordinary[at].From, Rate: tier.Rate})
	}
	return out, nil
}

// ProjectCapitalGains is base's rules n years later: dollar thresholds
// indexed like the brackets; rates, tiers tied to the (separately indexed)
// ordinary brackets, and the net investment income tax thresholds (fixed in
// law) unchanged.
func ProjectCapitalGains(base *CapitalGains, n int) *CapitalGains {
	c := *base
	if base.Schedules != nil {
		s := &ByStatus[[]Bracket]{Single: indexSchedule(base.Schedules.Single, n)}
		if base.Schedules.MarriedJoint != nil {
			m := indexSchedule(*base.Schedules.MarriedJoint, n)
			s.MarriedJoint = &m
		}
		c.Schedules = s
	}
	return &c
}

// CapitalGainsYear is a year's capital gains rules, transcribed or projected.
type CapitalGainsYear struct {
	Year         int
	CapitalGains *CapitalGains
	Projected    bool
}

// ExpandCapitalGains lists the transcribed years, then the last one
// projected through ProjectThrough.
func (d *Data) ExpandCapitalGains() []CapitalGainsYear {
	years := make([]int, 0, len(d.CapitalGains))
	for y := range d.CapitalGains {
		years = append(years, y)
	}
	sort.Ints(years)
	var out []CapitalGainsYear
	for _, y := range years {
		out = append(out, CapitalGainsYear{Year: y, CapitalGains: d.CapitalGains[y]})
	}
	if len(years) > 0 {
		last := years[len(years)-1]
		for y := last + 1; y <= ProjectThrough; y++ {
			out = append(out, CapitalGainsYear{Year: y, CapitalGains: ProjectCapitalGains(d.CapitalGains[last], y-last), Projected: true})
		}
	}
	return out
}

// segmentTax is the tax on the slice of taxable income from `from` to `to`
// under schedule, with no rate above maxRate.
func segmentTax(schedule []Bracket, from, to, maxRate float64) float64 {
	tax := 0.0
	for i, b := range schedule {
		top := math.Inf(1)
		if i+1 < len(schedule) {
			top = schedule[i+1].From
		}
		if lo, hi := math.Max(from, b.From), math.Min(to, top); hi > lo {
			tax += (hi - lo) * math.Min(b.Rate, maxRate)
		}
	}
	return tax
}

func scheduleFor(t *Tax, status Status) []Bracket {
	if status == MarriedJoint && t.Schedules.MarriedJoint != nil {
		return *t.Schedules.MarriedJoint
	}
	return t.Schedules.Single
}

// FederalTax is the federal income tax on ordinary income plus net long-term
// capital gain (gains), following the year's capital gains rules, and how
// much of it the gains add. With no rules (cg nil), or a federal tax that
// isn't a bracket schedule, gains are taxed as ordinary income.
//
// Simplified like the rest of this package: standard deduction only, the
// gain is all long-term (or qualified dividends), and before 1987 neither
// the minimum tax on preferences nor the maximum tax on earned income.
func FederalTax(t *Tax, cg *CapitalGains, ordinary, gains float64, status Status) (total, onGains float64) {
	ordinary, gains = math.Max(0, ordinary), math.Max(0, gains)
	without := LayerTax(t, ordinary, status)
	if gains == 0 {
		return without, 0
	}
	asOrdinary := LayerTax(t, ordinary+gains, status)
	if cg == nil || t.Type != "brackets" {
		return asOrdinary, asOrdinary - without
	}
	schedule := scheduleFor(t, status)

	switch cg.Method {
	case "exclusion":
		included := gains * (1 - *cg.ExcludedShare)
		total = LayerTax(t, ordinary+included, status)
		if cg.AlternativeRate != nil {
			// Ordinary taxable income without the gain, then the gain at
			// the alternative rate up to the limit; beyond it, the excess
			// rate or the included share of the excess at ordinary rates.
			taxable := math.Max(0, ordinary+included-t.DeductionFor(ordinary+included, status))
			base := math.Max(0, taxable-math.Min(included, taxable))
			limit := math.Inf(1)
			if cg.AlternativeLimit != nil {
				limit = pick(cg.AlternativeLimit, status)
			}
			alt := ScheduleTax(schedule, base) + *cg.AlternativeRate*math.Min(gains, limit)
			excess := math.Max(0, gains-limit)
			if cg.AlternativeExcessRate != nil {
				alt += *cg.AlternativeExcessRate * excess
			} else {
				alt += segmentTax(schedule, base, base+excess*(1-*cg.ExcludedShare), 1)
			}
			total = math.Min(total, alt)
		}
	case "maxRate", "brackets":
		taxable := math.Max(0, ordinary+gains-t.DeductionFor(ordinary+gains, status))
		base := taxable - math.Min(gains, taxable)
		if cg.Method == "maxRate" {
			total = ScheduleTax(schedule, base) + segmentTax(schedule, base, taxable, *cg.MaxRate)
		} else {
			preferential, err := cg.preferentialSchedule(schedule, status)
			if err != nil { // validated against the federal data; can't happen
				return asOrdinary, asOrdinary - without
			}
			total = ScheduleTax(schedule, base) + segmentTax(preferential, base, taxable, 1)
		}
		total = math.Min(total, asOrdinary)
	default: // "ordinary"
		total = asOrdinary
	}
	return total, total - without
}

// NetInvestmentIncome is the year's net investment income tax on investment
// income nii for a household with modified AGI magi (0 before 2013).
func (c *CapitalGains) NetInvestmentIncome(nii, magi float64, status Status) float64 {
	if c == nil || c.NetInvestmentIncomeTax == nil {
		return 0
	}
	n := c.NetInvestmentIncomeTax
	return n.Rate * math.Min(math.Max(0, nii), math.Max(0, magi-pick(&n.Threshold, status)))
}
