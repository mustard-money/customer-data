package taxtables

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Status is a filing status: "single" or "marriedJoint".
type Status string

const (
	Single       Status = "single"
	MarriedJoint Status = "marriedJoint"
)

// Layer is one jurisdiction's income tax in one year, as a calculation
// uses it.
type Layer struct {
	ID     string
	Kind   string // federal, state or local
	Region string
	// Coverage, for a local layer found by ZIP code: "full" when the whole
	// ZIP lies inside the jurisdiction, "partial" when only part does.
	Coverage  string
	Year      int
	Projected bool
	Tax       *Tax
}

type zipRange struct {
	from, to int
	id       string
	coverage string
}

// Tables answers which taxes apply in a year and ZIP code: transcribed
// years as the data has them, later years projected (through
// ProjectThrough). Safe for concurrent use.
type Tables struct {
	data       *Data
	lastActual int
	byID       map[string]*Jurisdiction
	zips       []zipRange
	payroll    []int // transcribed payroll years, ascending

	mu             sync.Mutex
	projected      map[string]*Tax          // "id/year" → projected tax
	projectedGains map[string]*CapitalGains // "capital-gains/year" → projected rules
}

// NewTables builds lookups over d.
func NewTables(d *Data) *Tables {
	t := &Tables{
		data:       d,
		lastActual: d.LastActualYear(),
		byID:       map[string]*Jurisdiction{},
		projected:  map[string]*Tax{},

		projectedGains: map[string]*CapitalGains{},
	}
	for _, j := range d.Jurisdictions {
		t.byID[j.ID] = j
		c := d.Zips[j.ID]
		for _, set := range []struct {
			ranges   []string
			coverage string
		}{{c.Full, "full"}, {c.Partial, "partial"}} {
			for _, r := range set.ranges {
				from, to, _ := ParseZipRange(r) // validated by Parse
				t.zips = append(t.zips, zipRange{from, to, j.ID, set.coverage})
			}
		}
	}
	sort.Slice(t.zips, func(a, b int) bool { return t.zips[a].from < t.zips[b].from })
	for y := range d.Payroll {
		t.payroll = append(t.payroll, y)
	}
	sort.Ints(t.payroll)
	return t
}

// Years is the span the tables cover: the first transcribed federal year,
// the last transcribed year, and the last projected one.
func (t *Tables) Years() (first, lastActual, last int) {
	first = t.lastActual
	if f := t.byID["us-federal"]; f != nil && len(f.years()) > 0 {
		first = f.years()[0]
	}
	return first, t.lastActual, ProjectThrough
}

// tax is j's tax in year: transcribed, or projected from the last actual
// year. Nil when there's none (or it isn't known).
func (t *Tables) tax(j *Jurisdiction, year int) (tax *Tax, projected bool) {
	if tx, ok := j.Years[strconv.Itoa(year)]; ok {
		return tx, false
	}
	if year <= t.lastActual || year > ProjectThrough {
		return nil, false
	}
	base, ok := j.Years[strconv.Itoa(t.lastActual)]
	if !ok {
		return nil, false
	}
	key := j.ID + "/" + strconv.Itoa(year)
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.projected[key]; ok {
		return p, true
	}
	p := ProjectTax(base, year-t.lastActual)
	t.projected[key] = p
	return p, true
}

func (t *Tables) layer(j *Jurisdiction, year int, coverage string) (Layer, bool) {
	tx, projected := t.tax(j, year)
	if tx == nil {
		return Layer{}, false
	}
	return Layer{ID: j.ID, Kind: j.Kind, Region: j.Region, Coverage: coverage, Year: year, Projected: projected, Tax: tx}, true
}

// Federal is the federal layer for year.
func (t *Tables) Federal(year int) (Layer, bool) {
	j := t.byID["us-federal"]
	if j == nil {
		return Layer{}, false
	}
	return t.layer(j, year, "")
}

// CapitalGains is the federal capital gains rules for year: transcribed, or
// the last transcribed year's projected. Nil when the year isn't covered
// (gains are then taxed as ordinary income).
func (t *Tables) CapitalGains(year int) (cg *CapitalGains, projected bool) {
	if c, ok := t.data.CapitalGains[year]; ok {
		return c, false
	}
	last := 0
	for y := range t.data.CapitalGains {
		last = max(last, y)
	}
	if last == 0 || year < last || year > ProjectThrough {
		return nil, false
	}
	key := "capital-gains/" + strconv.Itoa(year)
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.projectedGains[key]; ok {
		return p, true
	}
	p := ProjectCapitalGains(t.data.CapitalGains[last], year-last)
	t.projectedGains[key] = p
	return p, true
}

var zipFormat = regexp.MustCompile(`^\d{5}(-\d{4})?$`)

// ValidZip accepts 12345 or 12345-6789.
func ValidZip(zip string) bool { return zipFormat.MatchString(strings.TrimSpace(zip)) }

// ErrUnknownZip: the ZIP code isn't valid or isn't in any state's coverage.
var ErrUnknownZip = fmt.Errorf("unknown ZIP code")

// Lookup is the taxes that apply in a ZIP code in a year.
type Lookup struct {
	// Layers: federal, the state, then local taxes overlapping the ZIP code.
	// Without a ZIP code, federal alone.
	Layers []Layer
	// StateMissing: the ZIP's state tax that year hasn't been transcribed, so
	// Layers leaves it (and any locals) out and totals understate the tax.
	StateMissing string
	// MissingLocals: local taxes overlapping the ZIP code whose tax that
	// year hasn't been transcribed (regions).
	MissingLocals []string
}

// For returns the taxes in year for zip; an empty zip gives federal alone.
// Errors: ErrUnknownZip, or no federal tables for the year.
func (t *Tables) For(zip string, year int) (Lookup, error) {
	federal, ok := t.Federal(year)
	if !ok {
		return Lookup{}, fmt.Errorf("no tax tables for %d", year)
	}
	out := Lookup{Layers: []Layer{federal}}
	zip = strings.TrimSpace(zip)
	if zip == "" {
		return out, nil
	}
	if !ValidZip(zip) {
		return Lookup{}, ErrUnknownZip
	}
	n, _ := strconv.Atoi(zip[:5])
	var state *zipRange
	var locals []zipRange
	for i := range t.zips {
		r := t.zips[i]
		if r.from > n {
			break
		}
		if n > r.to {
			continue
		}
		if strings.HasPrefix(r.id, "us-state-") {
			state = &t.zips[i]
		} else {
			locals = append(locals, r)
		}
	}
	if state == nil {
		return Lookup{}, ErrUnknownZip
	}
	sj := t.byID[state.id]
	sl, ok := t.layer(sj, year, "")
	if !ok {
		out.StateMissing = sj.Region
		return out, nil
	}
	out.Layers = append(out.Layers, sl)
	// Locals in a stable order: by id.
	sort.Slice(locals, func(a, b int) bool { return locals[a].id < locals[b].id })
	for _, r := range locals {
		j := t.byID[r.id]
		if l, ok := t.layer(j, year, r.coverage); ok {
			out.Layers = append(out.Layers, l)
		} else if j.Missing(year) && year <= t.lastActual {
			out.MissingLocals = append(out.MissingLocals, j.Region)
		}
	}
	return out, nil
}

// Payroll is the year's employee Social Security and Medicare parameters:
// transcribed, projected after the last transcribed year, and for years
// before the first one, the first year's.
func (t *Tables) Payroll(year int) (p *Payroll, projected bool) {
	if len(t.payroll) == 0 {
		return nil, false
	}
	first, last := t.payroll[0], t.payroll[len(t.payroll)-1]
	switch {
	case year < first:
		return t.data.Payroll[first], false
	case year <= last:
		if p, ok := t.data.Payroll[year]; ok {
			return p, false
		}
		return t.data.Payroll[last], false
	default:
		return ProjectPayroll(t.data.Payroll[last], min(year, ProjectThrough)-last), true
	}
}
