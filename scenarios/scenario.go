// Package scenarios implements the what-if projection resource: model,
// validation, Postgres store and HTTP handlers.
package scenarios

import (
	"encoding/json"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// Event kinds.
const (
	KindOneTimeCost   = "oneTimeCost"
	KindOneTimeIncome = "oneTimeIncome"
	KindIncomePause   = "incomePause"
	KindRecurringCost = "recurringCost"
)

// Scenario is a user-defined what-if projection over a set of items.
type Scenario struct {
	ID      string
	Name    string
	ItemIDs []string // never nil
	// Main items the scenario hides (the app treats a scenario as Main plus
	// its own changes). nil on scenarios saved before that, which listed the
	// items they included in ItemIDs instead.
	ExcludedItemIDs *[]string
	Events          []Event // never nil
	Parameters      Parameters
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Event is a one-off or recurring change applied to a projection. It is
// stored as-is in the scenarios.events jsonb column, so its JSON shape is
// both the API and the storage format.
type Event struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Name   string  `json:"name"`
	Year   int     `json:"year"`
	Amount float64 `json:"amount"`
	Months *int    `json:"months,omitempty"` // incomePause only
	Years  *int    `json:"years,omitempty"`  // recurringCost only
}

// Parameters are the projection's assumptions. Rates are fractions (0.07 is
// 7%). Stored as-is in the scenarios.parameters jsonb column.
type Parameters struct {
	Years             int     `json:"years"`
	ReturnRate        float64 `json:"returnRate"`
	Inflation         float64 `json:"inflation"`
	IncomeGrowth      float64 `json:"incomeGrowth"`
	IncomeAdjustment  float64 `json:"incomeAdjustment"`
	ExpenseAdjustment float64 `json:"expenseAdjustment"`
	// When the household retires: the app stops earned income (W-2,
	// 1099-NEC, 1099-K) and its workplace contributions at the end of this
	// month. Optional; the month needs the year, and no month means January.
	RetirementYear  *int `json:"retirementYear,omitempty"`
	RetirementMonth *int `json:"retirementMonth,omitempty"`
}

type scenarioJSON struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	ItemIDs         []string   `json:"itemIds"`
	ExcludedItemIDs *[]string  `json:"excludedItemIds,omitempty"`
	Events          []Event    `json:"events"`
	Parameters      Parameters `json:"parameters"`
	CreatedAt       string     `json:"createdAt"`
	UpdatedAt       string     `json:"updatedAt"`
}

// MarshalJSON renders the scenario in the API's camelCase shape. Empty
// itemIds and events render as [] rather than null.
func (s Scenario) MarshalJSON() ([]byte, error) {
	itemIDs, events := s.ItemIDs, s.Events
	if itemIDs == nil {
		itemIDs = []string{}
	}
	if events == nil {
		events = []Event{}
	}
	return json.Marshal(scenarioJSON{
		ID:              s.ID,
		Name:            s.Name,
		ItemIDs:         itemIDs,
		ExcludedItemIDs: s.ExcludedItemIDs,
		Events:          events,
		Parameters:      s.Parameters,
		CreatedAt:       fields.FormatTime(s.CreatedAt),
		UpdatedAt:       fields.FormatTime(s.UpdatedAt),
	})
}

// ScenarioInput is the PUT request body. Pointer fields distinguish
// "absent/null" from zero values so validation can report missing fields.
// Unknown keys, including inside events and parameters, are rejected by the
// decoder.
type ScenarioInput struct {
	ID              *string          `json:"id"`
	Name            *string          `json:"name"`
	ItemIDs         *[]string        `json:"itemIds"`
	ExcludedItemIDs *[]string        `json:"excludedItemIds"`
	Events          *[]EventInput    `json:"events"`
	Parameters      *ParametersInput `json:"parameters"`
	CreatedAt       *string          `json:"createdAt"`
	// UpdatedAt is accepted so clients can send back a full scenario, but it
	// is ignored: the server sets updatedAt on every write.
	UpdatedAt *string `json:"updatedAt"`
}

// EventInput is one element of the PUT body's events array.
type EventInput struct {
	ID     *string  `json:"id"`
	Kind   *string  `json:"kind"`
	Name   *string  `json:"name"`
	Year   *int     `json:"year"`
	Amount *float64 `json:"amount"`
	Months *int     `json:"months"`
	Years  *int     `json:"years"`
}

// ParametersInput is the PUT body's parameters object. Every field is
// required except the retirement date.
type ParametersInput struct {
	Years             *int     `json:"years"`
	ReturnRate        *float64 `json:"returnRate"`
	Inflation         *float64 `json:"inflation"`
	IncomeGrowth      *float64 `json:"incomeGrowth"`
	IncomeAdjustment  *float64 `json:"incomeAdjustment"`
	ExpenseAdjustment *float64 `json:"expenseAdjustment"`
	RetirementYear    *int     `json:"retirementYear"`
	RetirementMonth   *int     `json:"retirementMonth"`
}
