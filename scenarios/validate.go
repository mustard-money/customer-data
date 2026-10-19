package scenarios

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mustard-money/customer-data/fields"
)

// Limits.
const (
	MaxItemIDs = 500
	MaxEvents  = 100
	MinYear    = 1
	MaxYear    = 60 // event year, months, years and parameters.years
)

// maxAmount is the largest cents-rounded amount accepted (matches items).
const maxAmount = 999_999_999_999.99

// floatRange is an inclusive range for a fractional parameter.
type floatRange struct{ min, max float64 }

var (
	returnRateRange        = floatRange{-0.5, 0.5}
	inflationRange         = floatRange{-0.1, 0.2}
	incomeGrowthRange      = floatRange{-0.2, 0.3}
	incomeAdjustmentRange  = floatRange{-1, 2}
	expenseAdjustmentRange = floatRange{-0.9, 2}
)

// ValidKind reports whether k is a known event kind.
func ValidKind(k string) bool {
	switch k {
	case KindOneTimeCost, KindOneTimeIncome, KindIncomePause, KindRecurringCost:
		return true
	}
	return false
}

// Validate checks a PUT body against the path id and returns the normalized
// scenario to store (names trimmed, event amounts rounded to cents, createdAt
// in UTC with millisecond precision). UpdatedAt is left zero; the store sets
// it. Errors are *fields.ValidationError.
func (in ScenarioInput) Validate(pathID string) (Scenario, error) {
	if err := fields.ValidateBodyID(in.ID, pathID); err != nil {
		return Scenario{}, err
	}

	name, err := fields.Name(in.Name)
	if err != nil {
		return Scenario{}, err
	}

	itemIDs, err := validateItemIDs(in.ItemIDs)
	if err != nil {
		return Scenario{}, err
	}
	excluded, err := validateExcludedItemIDs(in.ExcludedItemIDs)
	if err != nil {
		return Scenario{}, err
	}

	events, err := validateEvents(in.Events)
	if err != nil {
		return Scenario{}, err
	}

	params, err := in.Parameters.validate()
	if err != nil {
		return Scenario{}, err
	}

	createdAt, err := fields.CreatedAt(in.CreatedAt)
	if err != nil {
		return Scenario{}, err
	}

	return Scenario{
		ID:              pathID,
		Name:            name,
		ItemIDs:         itemIDs,
		ExcludedItemIDs: excluded,
		Events:          events,
		Parameters:      params,
		CreatedAt:       createdAt,
	}, nil
}

// prefixed rewrites a shared-rule error ("id is required", "name must not be
// blank") so it names the nested field ("events[2].id is required").
func prefixed(err error, prefix string) error {
	var ve *fields.ValidationError
	if errors.As(err, &ve) {
		return &fields.ValidationError{Msg: prefix + ve.Msg}
	}
	return err
}

func validateItemIDs(in *[]string) ([]string, error) {
	if in == nil {
		return nil, fields.Invalid("itemIds is required")
	}
	return validateIDList(*in, "itemIds")
}

// validateExcludedItemIDs checks the Main items a scenario hides. Unset
// (nil) is kept as nil: it marks a scenario saved before scenarios were
// layers over Main, which the app reads from itemIds instead.
func validateExcludedItemIDs(in *[]string) (*[]string, error) {
	if in == nil {
		return nil, nil
	}
	ids, err := validateIDList(*in, "excludedItemIds")
	if err != nil {
		return nil, err
	}
	return &ids, nil
}

func validateIDList(in []string, name string) ([]string, error) {
	if len(in) > MaxItemIDs {
		return nil, fields.Invalid("%s must have at most %d entries", name, MaxItemIDs)
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, id := range in {
		if err := fields.ValidateID(id); err != nil {
			// "id must ..." -> "itemIds[3] must ...".
			var ve *fields.ValidationError
			errors.As(err, &ve)
			return nil, fields.Invalid("%s[%d]%s", name, i, strings.TrimPrefix(ve.Msg, "id"))
		}
		if seen[id] {
			return nil, fields.Invalid("%s must not contain duplicates (%q)", name, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func validateEvents(in *[]EventInput) ([]Event, error) {
	if in == nil {
		return nil, fields.Invalid("events is required")
	}
	if len(*in) > MaxEvents {
		return nil, fields.Invalid("events must have at most %d entries", MaxEvents)
	}
	out := make([]Event, 0, len(*in))
	seen := make(map[string]bool, len(*in))
	for i, ev := range *in {
		e, err := ev.validate(fmt.Sprintf("events[%d].", i))
		if err != nil {
			return nil, err
		}
		if seen[e.ID] {
			return nil, fields.Invalid("events must not contain duplicate ids (%q)", e.ID)
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out, nil
}

func (in EventInput) validate(p string) (Event, error) {
	if in.ID == nil {
		return Event{}, fields.Invalid("%sid is required", p)
	}
	if err := fields.ValidateID(*in.ID); err != nil {
		return Event{}, prefixed(err, p)
	}

	if in.Kind == nil || *in.Kind == "" {
		return Event{}, fields.Invalid("%skind is required", p)
	}
	kind := *in.Kind
	if !ValidKind(kind) {
		return Event{}, fields.Invalid("%skind must be one of: %s, %s, %s, %s", p,
			KindOneTimeCost, KindOneTimeIncome, KindIncomePause, KindRecurringCost)
	}

	name, err := fields.Name(in.Name)
	if err != nil {
		return Event{}, prefixed(err, p)
	}

	year, err := intInRange(in.Year, p+"year")
	if err != nil {
		return Event{}, err
	}

	if in.Amount == nil {
		return Event{}, fields.Invalid("%samount is required", p)
	}
	amount := *in.Amount
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return Event{}, fields.Invalid("%samount must be a number >= 0", p)
	}
	amount = math.Round(amount*100) / 100
	if amount > maxAmount {
		return Event{}, fields.Invalid("%samount must be at most %.2f", p, maxAmount)
	}
	if kind == KindIncomePause && *in.Amount != 0 {
		return Event{}, fields.Invalid("%samount must be 0 for %s", p, KindIncomePause)
	}

	months, err := kindOnlyInt(in.Months, kind, KindIncomePause, p+"months")
	if err != nil {
		return Event{}, err
	}
	years, err := kindOnlyInt(in.Years, kind, KindRecurringCost, p+"years")
	if err != nil {
		return Event{}, err
	}

	return Event{
		ID:     *in.ID,
		Kind:   kind,
		Name:   name,
		Year:   year,
		Amount: amount,
		Months: months,
		Years:  years,
	}, nil
}

// kindOnlyInt validates an integer field that is required (1–60) for events
// of kind want and must be absent or null for every other kind.
func kindOnlyInt(v *int, kind, want, name string) (*int, error) {
	if kind != want {
		if v != nil {
			return nil, fields.Invalid("%s must be omitted or null for %s", name, kind)
		}
		return nil, nil
	}
	if v == nil {
		return nil, fields.Invalid("%s is required for %s", name, want)
	}
	n, err := intInRange(v, name)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func intInRange(v *int, name string) (int, error) {
	if v == nil {
		return 0, fields.Invalid("%s is required", name)
	}
	if *v < MinYear || *v > MaxYear {
		return 0, fields.Invalid("%s must be between %d and %d", name, MinYear, MaxYear)
	}
	return *v, nil
}

func floatInRange(v *float64, name string, r floatRange) (float64, error) {
	if v == nil {
		return 0, fields.Invalid("%s is required", name)
	}
	if math.IsNaN(*v) || *v < r.min || *v > r.max {
		return 0, fields.Invalid("%s must be between %g and %g", name, r.min, r.max)
	}
	return *v, nil
}

func (in *ParametersInput) validate() (Parameters, error) {
	if in == nil {
		return Parameters{}, fields.Invalid("parameters is required")
	}
	var (
		p   Parameters
		err error
	)
	if p.Years, err = intInRange(in.Years, "parameters.years"); err != nil {
		return Parameters{}, err
	}
	for _, f := range []struct {
		dst  *float64
		src  *float64
		name string
		r    floatRange
	}{
		{&p.ReturnRate, in.ReturnRate, "returnRate", returnRateRange},
		{&p.Inflation, in.Inflation, "inflation", inflationRange},
		{&p.IncomeGrowth, in.IncomeGrowth, "incomeGrowth", incomeGrowthRange},
		{&p.IncomeAdjustment, in.IncomeAdjustment, "incomeAdjustment", incomeAdjustmentRange},
		{&p.ExpenseAdjustment, in.ExpenseAdjustment, "expenseAdjustment", expenseAdjustmentRange},
	} {
		if *f.dst, err = floatInRange(f.src, "parameters."+f.name, f.r); err != nil {
			return Parameters{}, err
		}
	}
	if p.RetirementYear, p.RetirementMonth, err = in.retirement(); err != nil {
		return Parameters{}, err
	}
	return p, nil
}

// Retirement years the server accepts. The app offers this year through 60
// years ahead; a saved year stays valid after it passes.
const minRetirementYear, maxRetirementYear = 1900, 2200

// retirement checks the optional retirement date: a year, and a month 1–12
// only with the year.
func (in *ParametersInput) retirement() (year, month *int, err error) {
	if in.RetirementYear == nil {
		if in.RetirementMonth != nil {
			return nil, nil, fields.Invalid("parameters.retirementMonth requires parameters.retirementYear")
		}
		return nil, nil, nil
	}
	if y := *in.RetirementYear; y < minRetirementYear || y > maxRetirementYear {
		return nil, nil, fields.Invalid("parameters.retirementYear must be from %d to %d", minRetirementYear, maxRetirementYear)
	}
	if in.RetirementMonth != nil && (*in.RetirementMonth < 1 || *in.RetirementMonth > 12) {
		return nil, nil, fields.Invalid("parameters.retirementMonth must be from 1 to 12")
	}
	y := *in.RetirementYear
	if in.RetirementMonth == nil {
		return &y, nil, nil
	}
	m := *in.RetirementMonth
	return &y, &m, nil
}
