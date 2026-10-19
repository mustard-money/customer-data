// Package taxtables holds US income and payroll tax parameters by year: the
// transcribed data embedded from data/ (see data/README.md), its validation,
// projection into future years, lookup by year and ZIP code (Tables), and the
// tax arithmetic (compute.go). The customer-data server imports it to compute
// taxes and to load the tables into Postgres; the tables themselves are never
// sent to clients.
package taxtables

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed data/*.json data/jurisdictions/*.json
var dataFS embed.FS

// Bracket is one marginal rate: Rate applies to taxable income from From up
// to the next bracket's From.
type Bracket struct {
	From float64 `json:"from"`
	Rate float64 `json:"rate"`
}

// ByStatus is an amount for each filing status; MarriedJoint is nil when it's
// the same as Single.
type ByStatus[T any] struct {
	Single       T  `json:"single"`
	MarriedJoint *T `json:"marriedJoint,omitempty"`
}

// PercentDeduction is a standard deduction that's a share of income, clamped
// to Minimum..Maximum (federal 1970–1976).
type PercentDeduction struct {
	Rate    float64           `json:"rate"`
	Minimum ByStatus[float64] `json:"minimum"`
	Maximum ByStatus[float64] `json:"maximum"`
}

// Tax is one jurisdiction's income tax in one year. Which fields are set
// depends on Type (brackets, flat, fixed or none).
type Tax struct {
	Type                     string               `json:"type"`
	Schedules                *ByStatus[[]Bracket] `json:"schedules,omitempty"`
	Rate                     *float64             `json:"rate,omitempty"`
	Deduction                *ByStatus[float64]   `json:"deduction,omitempty"`
	PercentStandardDeduction *PercentDeduction    `json:"percentStandardDeduction,omitempty"`
	Amount                   *float64             `json:"amount,omitempty"`
	Per                      string               `json:"per,omitempty"`
	MinimumEarnings          *float64             `json:"minimumEarnings,omitempty"`
	Source                   string               `json:"source"`
	Notes                    string               `json:"notes,omitempty"`
}

// Jurisdiction is a taxing jurisdiction and its tax by year (one data file).
type Jurisdiction struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Region string `json:"region,omitempty"`
	State  string `json:"state,omitempty"`
	// Since is the first year a local jurisdiction levied its tax, when
	// known: earlier years without a row had no tax, later ones without a row
	// haven't been transcribed. Without it, every missing year is untranscribed.
	Since *int            `json:"since,omitempty"`
	Years map[string]*Tax `json:"years"`
}

// Missing reports whether the jurisdiction's tax in year is unknown: no row,
// and not a year before a local tax began. Federal and state years outside the
// transcribed span are unknown too.
func (j *Jurisdiction) Missing(year int) bool {
	if _, ok := j.Years[strconv.Itoa(year)]; ok {
		return false
	}
	return j.Since == nil || year >= *j.Since
}

// Payroll is a year's employee Social Security and Medicare parameters.
type Payroll struct {
	SocialSecurityRate          float64            `json:"socialSecurityRate"`
	WageBase                    float64            `json:"wageBase"`
	MedicareRate                float64            `json:"medicareRate"`
	MedicareWageBase            *float64           `json:"medicareWageBase,omitempty"`
	AdditionalMedicareRate      float64            `json:"additionalMedicareRate"`
	AdditionalMedicareThreshold *ByStatus[float64] `json:"additionalMedicareThreshold,omitempty"`
	Source                      string             `json:"source"`
	Notes                       string             `json:"notes,omitempty"`
}

// ZipCoverage lists a jurisdiction's ZIP codes as "12345" or "12345-12399".
type ZipCoverage struct {
	Full    []string `json:"full,omitempty"`
	Partial []string `json:"partial,omitempty"`
}

// Data is everything in data/, parsed and checked.
type Data struct {
	Jurisdictions []*Jurisdiction
	Payroll       map[int]*Payroll
	// CapitalGains: the federal treatment of long-term capital gains by
	// year (capital-gains.json; empty while that file doesn't exist).
	CapitalGains map[int]*CapitalGains
	Zips         map[string]ZipCoverage
	// Hash identifies the files and the projection rules, so the loader can
	// tell whether the database is current.
	Hash string
}

// Embedded parses and validates the embedded data files.
func Embedded() (*Data, error) { return Parse(dataFS) }

// Parse reads and validates the data files in fsys (laid out as data/).
func Parse(fsys fs.FS) (*Data, error) {
	h := sha256.New()
	fmt.Fprintf(h, "projection:%s\n", ProjectionVersion)
	read := func(name string, v any) error {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		h.Write([]byte(name))
		h.Write(b)
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}

	d := &Data{Payroll: map[int]*Payroll{}, CapitalGains: map[int]*CapitalGains{}}

	names, err := fs.Glob(fsys, "data/jurisdictions/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		var j Jurisdiction
		if err := read(name, &j); err != nil {
			return nil, err
		}
		if want := strings.TrimSuffix(path.Base(name), ".json"); j.ID != want {
			return nil, fmt.Errorf("%s: id %q doesn't match the file name", name, j.ID)
		}
		if seen[j.ID] {
			return nil, fmt.Errorf("%s: duplicate id", name)
		}
		seen[j.ID] = true
		if err := j.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		d.Jurisdictions = append(d.Jurisdictions, &j)
	}

	var payroll struct {
		Years map[string]*Payroll `json:"years"`
	}
	if err := read("data/payroll.json", &payroll); err != nil {
		return nil, err
	}
	for key, p := range payroll.Years {
		year, err := parseYear(key)
		if err != nil {
			return nil, fmt.Errorf("data/payroll.json: %w", err)
		}
		if err := p.validate(); err != nil {
			return nil, fmt.Errorf("data/payroll.json %d: %w", year, err)
		}
		d.Payroll[year] = p
	}

	if _, err := fs.Stat(fsys, "data/capital-gains.json"); err == nil {
		var cg struct {
			Years map[string]*CapitalGains `json:"years"`
		}
		if err := read("data/capital-gains.json", &cg); err != nil {
			return nil, err
		}
		var federal *Jurisdiction
		for _, j := range d.Jurisdictions {
			if j.ID == "us-federal" {
				federal = j
			}
		}
		for key, c := range cg.Years {
			year, err := parseYear(key)
			if err != nil {
				return nil, fmt.Errorf("data/capital-gains.json: %w", err)
			}
			if c == nil {
				return nil, fmt.Errorf("data/capital-gains.json %d: empty", year)
			}
			if err := c.validate(); err != nil {
				return nil, fmt.Errorf("data/capital-gains.json %d: %w", year, err)
			}
			var fed *Tax
			if federal != nil {
				fed = federal.Years[key]
			}
			if err := c.checkTiers(fed); err != nil {
				return nil, fmt.Errorf("data/capital-gains.json %d: %w", year, err)
			}
			d.CapitalGains[year] = c
		}
	}

	var zips struct {
		Jurisdictions map[string]ZipCoverage `json:"jurisdictions"`
	}
	if err := read("data/zips.json", &zips); err != nil {
		return nil, err
	}
	for id, c := range zips.Jurisdictions {
		if !seen[id] {
			return nil, fmt.Errorf("data/zips.json: unknown jurisdiction %q", id)
		}
		for _, r := range append(append([]string{}, c.Full...), c.Partial...) {
			if _, _, err := ParseZipRange(r); err != nil {
				return nil, fmt.Errorf("data/zips.json %s: %w", id, err)
			}
		}
	}
	d.Zips = zips.Jurisdictions
	d.Hash = hex.EncodeToString(h.Sum(nil))
	return d, nil
}

var (
	idFormat    = regexp.MustCompile(`^us-(federal|state-[A-Z]{2}|local-[a-z0-9-]+)$`)
	stateFormat = regexp.MustCompile(`^[A-Z]{2}$`)
)

// MinYear and MaxYear bound the years the data and projection may cover.
const (
	MinYear = 1900
	MaxYear = 2200
)

func parseYear(key string) (int, error) {
	year, err := strconv.Atoi(key)
	if err != nil || len(key) != 4 || year < MinYear || year > MaxYear {
		return 0, fmt.Errorf("year %q must be 4 digits from %d to %d", key, MinYear, MaxYear)
	}
	return year, nil
}

func (j *Jurisdiction) validate() error {
	if !idFormat.MatchString(j.ID) {
		return fmt.Errorf("id %q must be us-federal, us-state-XX or us-local-<slug>", j.ID)
	}
	switch j.Kind {
	case "federal":
		if j.ID != "us-federal" {
			return fmt.Errorf("kind federal needs id us-federal")
		}
	case "state":
		if !strings.HasPrefix(j.ID, "us-state-") || j.State != j.ID[len("us-state-"):] {
			return fmt.Errorf("a state's id must be us-state-<state>")
		}
	case "local":
		if !strings.HasPrefix(j.ID, "us-local-") {
			return fmt.Errorf("a local jurisdiction's id must start with us-local-")
		}
	default:
		return fmt.Errorf("kind %q must be federal, state or local", j.Kind)
	}
	if j.Kind != "federal" && strings.TrimSpace(j.Region) == "" {
		return fmt.Errorf("region is required")
	}
	if j.State != "" && !stateFormat.MatchString(j.State) {
		return fmt.Errorf("state %q must be a 2-letter postal code", j.State)
	}
	if len(j.Years) == 0 {
		return fmt.Errorf("no years")
	}
	if j.Since != nil {
		if j.Kind != "local" {
			return fmt.Errorf("since is only for local jurisdictions")
		}
		if *j.Since < MinYear || *j.Since > MaxYear {
			return fmt.Errorf("since must be from %d to %d", MinYear, MaxYear)
		}
	}
	for key, t := range j.Years {
		year, err := parseYear(key)
		if err != nil {
			return err
		}
		if j.Since != nil && year < *j.Since {
			return fmt.Errorf("%d: before since (%d)", year, *j.Since)
		}
		if t == nil {
			return fmt.Errorf("%d: empty", year)
		}
		if err := t.validate(); err != nil {
			return fmt.Errorf("%d: %w", year, err)
		}
	}
	return nil
}

func validRate(r float64) bool { return r >= 0 && r < 1 }

func validSource(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func validAmount(a float64) bool { return a >= 0 && a < 1e9 }

func validSchedule(s []Bracket) error {
	if len(s) == 0 || s[0].From != 0 {
		return fmt.Errorf("a schedule's first bracket must start at 0")
	}
	for i, b := range s {
		if !validRate(b.Rate) || !validAmount(b.From) {
			return fmt.Errorf("bracket %d: rate must be 0 to <1 and from a non-negative amount", i)
		}
		if i > 0 && b.From <= s[i-1].From {
			return fmt.Errorf("bracket %d: brackets must ascend", i)
		}
	}
	return nil
}

func validByStatus(v *ByStatus[float64], what string) error {
	if v == nil {
		return nil
	}
	if !validAmount(v.Single) || (v.MarriedJoint != nil && !validAmount(*v.MarriedJoint)) {
		return fmt.Errorf("%s must be non-negative amounts", what)
	}
	return nil
}

func (t *Tax) validate() error {
	if !strings.HasPrefix(t.Source, "https://") && !strings.HasPrefix(t.Source, "http://") {
		return fmt.Errorf("source must be a URL")
	}
	none := func(fields ...bool) bool {
		for _, f := range fields {
			if f {
				return false
			}
		}
		return true
	}
	switch t.Type {
	case "brackets":
		if t.Schedules == nil {
			return fmt.Errorf("brackets need schedules")
		}
		if err := validSchedule(t.Schedules.Single); err != nil {
			return fmt.Errorf("single: %w", err)
		}
		if t.Schedules.MarriedJoint != nil {
			if err := validSchedule(*t.Schedules.MarriedJoint); err != nil {
				return fmt.Errorf("marriedJoint: %w", err)
			}
		}
		if !none(t.Rate != nil, t.Amount != nil, t.Per != "", t.MinimumEarnings != nil) {
			return fmt.Errorf("brackets take only schedules and deductions")
		}
	case "flat":
		if t.Rate == nil || !validRate(*t.Rate) {
			return fmt.Errorf("flat needs a rate from 0 to <1")
		}
		if !none(t.Schedules != nil, t.Amount != nil, t.Per != "", t.MinimumEarnings != nil) {
			return fmt.Errorf("flat takes only a rate and deductions")
		}
	case "fixed":
		if t.Amount == nil || !validAmount(*t.Amount) || (t.Per != "month" && t.Per != "year") {
			return fmt.Errorf("fixed needs an amount and per month or year")
		}
		if t.MinimumEarnings != nil && !validAmount(*t.MinimumEarnings) {
			return fmt.Errorf("minimumEarnings must be a non-negative amount")
		}
		if !none(t.Schedules != nil, t.Rate != nil, t.Deduction != nil, t.PercentStandardDeduction != nil) {
			return fmt.Errorf("fixed takes only amount, per and minimumEarnings")
		}
	case "none":
		if !none(t.Schedules != nil, t.Rate != nil, t.Deduction != nil, t.PercentStandardDeduction != nil,
			t.Amount != nil, t.Per != "", t.MinimumEarnings != nil) {
			return fmt.Errorf("none takes no tax fields")
		}
	default:
		return fmt.Errorf("type %q must be brackets, flat, fixed or none", t.Type)
	}
	if err := validByStatus(t.Deduction, "deduction"); err != nil {
		return err
	}
	if p := t.PercentStandardDeduction; p != nil {
		if !validRate(p.Rate) {
			return fmt.Errorf("percentStandardDeduction rate must be 0 to <1")
		}
		if err := validByStatus(&p.Minimum, "percentStandardDeduction minimum"); err != nil {
			return err
		}
		if err := validByStatus(&p.Maximum, "percentStandardDeduction maximum"); err != nil {
			return err
		}
	}
	return nil
}

func (p *Payroll) validate() error {
	if !strings.HasPrefix(p.Source, "https://") {
		return fmt.Errorf("source must be a URL")
	}
	if !validRate(p.SocialSecurityRate) || !validRate(p.MedicareRate) || !validRate(p.AdditionalMedicareRate) {
		return fmt.Errorf("rates must be 0 to <1")
	}
	if !validAmount(p.WageBase) || (p.MedicareWageBase != nil && !validAmount(*p.MedicareWageBase)) {
		return fmt.Errorf("wage bases must be non-negative amounts")
	}
	if (p.AdditionalMedicareRate > 0) != (p.AdditionalMedicareThreshold != nil) {
		return fmt.Errorf("additionalMedicareRate and additionalMedicareThreshold go together")
	}
	return validByStatus(p.AdditionalMedicareThreshold, "additionalMedicareThreshold")
}

// ParseZipRange parses "12345" or "12345-12399" into inclusive bounds.
func ParseZipRange(s string) (from, to int, err error) {
	lo, hi, found := strings.Cut(s, "-")
	if !found {
		hi = lo
	}
	parse := func(z string) (int, error) {
		n, err := strconv.Atoi(z)
		if err != nil || len(z) != 5 {
			return 0, fmt.Errorf("ZIP range %q: ZIPs must be 5 digits", s)
		}
		return n, nil
	}
	if from, err = parse(lo); err != nil {
		return 0, 0, err
	}
	if to, err = parse(hi); err != nil {
		return 0, 0, err
	}
	if to < from {
		return 0, 0, fmt.Errorf("ZIP range %q runs backwards", s)
	}
	return from, to, nil
}
