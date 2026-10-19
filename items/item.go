// Package items implements the personal-finance item resource: model,
// validation, Postgres store and HTTP handlers.
package items

import (
	"encoding/json"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// Category values.
const (
	CategoryIncome      = "income"
	CategoryInvestments = "investments"
	CategoryExpenses    = "expenses"
)

// Frequency values.
const (
	FrequencyOnce        = "once"  // expenses only: a one-time cost in its start month
	FrequencyDaily       = "daily" // expenses only
	FrequencyWeekly      = "weekly"
	FrequencyBiweekly    = "biweekly"    // every two weeks
	FrequencySemimonthly = "semimonthly" // twice a month
	FrequencyMonthly     = "monthly"
	FrequencyQuarterly   = "quarterly"
	FrequencySemiannual  = "semiannually" // twice a year
	FrequencyYearly      = "yearly"
)

// IncomeTypes lists, in display order, the tax forms income can be reported
// on. Must match IncomeTypes in the website's src/data/income-types.ts and the
// items_income_type_valid constraint.
var IncomeTypes = []string{
	"w2",       // W-2 wages
	"1099nec",  // contract work
	"1099misc", // rents, royalties, other
	"1099k",    // payment apps and marketplaces
	"k1",       // partnership or S corporation share
	"1099int",  // interest
	"1099div",  // dividends
	"1099b",    // stock and other sales
	"1099r",    // pensions and retirement plans
	"ssa1099",  // Social Security
	"1099g",    // unemployment, state refunds
	"other",
}

// Accounts lists, in display order, the kinds of account an investment can be
// held in. Must match AccountTypes in the website's src/data/accounts.ts and
// the items_account_valid constraint.
var Accounts = []string{
	"taxable",
	"bank",
	"cd",
	"hsa",
	"traditionalIra",
	"rothIra",
	"traditional401k",
	"roth401k",
	"traditional403b",
	"roth403b",
	"traditional457b",
	"roth457b",
}

// allowedFrequencies lists, in display order, the frequencies each recurring
// category accepts. Investments have no frequency.
var allowedFrequencies = map[string][]string{
	CategoryIncome: {
		FrequencyWeekly, FrequencyBiweekly, FrequencySemimonthly,
		FrequencyMonthly, FrequencyQuarterly, FrequencySemiannual, FrequencyYearly,
	},
	CategoryExpenses: {FrequencyOnce, FrequencyDaily, FrequencyWeekly, FrequencyMonthly, FrequencyYearly},
}

// ExpenseFrequencies are the frequencies expenses accept, in display order.
var ExpenseFrequencies = allowedFrequencies[CategoryExpenses]

// IncomeFrequencies are the frequencies income accepts, in display order.
var IncomeFrequencies = allowedFrequencies[CategoryIncome]

// Item is a single personal-finance entry.
type Item struct {
	ID         string
	Category   string
	Name       string
	Amount     float64
	Frequency  *string // nil for investments
	IncomeType *string // income only; optional
	// Income only: self-employment earnings (owing self-employment tax).
	// Nil means no; never false.
	SelfEmployment *bool
	// 1099-DIV income only: false when the dividends aren't qualified (taxed
	// as ordinary income). Nil means qualified, the default; never true.
	QualifiedDividends *bool
	Benefits           *Benefits // W-2 income only; optional
	Loan               *Loan     // monthly expenses only: the debt this is the payment on
	// The scenario the item belongs to (nil: Main), and the Main item it
	// stands in for there when it's the scenario's copy of one. Ids only; the
	// app resolves them (see the website's scenarios/scenario-items.ts).
	ScenarioID *string
	Replaces   *string
	AsOfYear   *int     // investments only: when Amount was the value
	AsOfMonth  *int     // 1–12; only with AsOfYear
	ReturnRate *float64 // investments only: expected yearly return override
	// Income only: when it starts and ends. Months only with their years.
	StartYear, StartMonth, EndYear, EndMonth *int
	// Retirement account investments only: when withdrawals start and the
	// share of the balance withdrawn each year. Start and rate come together.
	WithdrawalStartYear, WithdrawalStartMonth *int
	WithdrawalRate                            *float64
	Account                                   *string // investments only; optional
	// Brokerage investments only: "fixedIncome" (nil means equities), its
	// yearly income rate, and the taxes its interest is exempt from.
	AssetClass *string
	IncomeRate *float64
	TaxExempt  *TaxExempt
	// Investments only: the W-2 income whose workplace plan this is; optional.
	Workplace *Workplace
	CreatedAt time.Time
	UpdatedAt time.Time
}

type itemJSON struct {
	ID                   string     `json:"id"`
	Category             string     `json:"category"`
	Name                 string     `json:"name"`
	Amount               float64    `json:"amount"`
	Frequency            *string    `json:"frequency,omitempty"`
	IncomeType           *string    `json:"incomeType,omitempty"`
	SelfEmployment       *bool      `json:"selfEmployment,omitempty"`
	QualifiedDividends   *bool      `json:"qualifiedDividends,omitempty"`
	Benefits             *Benefits  `json:"benefits,omitempty"`
	Loan                 *Loan      `json:"loan,omitempty"`
	ScenarioID           *string    `json:"scenarioId,omitempty"`
	Replaces             *string    `json:"replaces,omitempty"`
	AsOfYear             *int       `json:"asOfYear,omitempty"`
	AsOfMonth            *int       `json:"asOfMonth,omitempty"`
	ReturnRate           *float64   `json:"returnRate,omitempty"`
	StartYear            *int       `json:"startYear,omitempty"`
	StartMonth           *int       `json:"startMonth,omitempty"`
	EndYear              *int       `json:"endYear,omitempty"`
	EndMonth             *int       `json:"endMonth,omitempty"`
	WithdrawalStartYear  *int       `json:"withdrawalStartYear,omitempty"`
	WithdrawalStartMonth *int       `json:"withdrawalStartMonth,omitempty"`
	WithdrawalRate       *float64   `json:"withdrawalRate,omitempty"`
	Account              *string    `json:"account,omitempty"`
	AssetClass           *string    `json:"assetClass,omitempty"`
	IncomeRate           *float64   `json:"incomeRate,omitempty"`
	TaxExempt            *TaxExempt `json:"taxExempt,omitempty"`
	Workplace            *Workplace `json:"workplace,omitempty"`
	CreatedAt            string     `json:"createdAt"`
	UpdatedAt            string     `json:"updatedAt"`
}

// MarshalJSON renders the item in the API's camelCase shape.
func (it Item) MarshalJSON() ([]byte, error) {
	return json.Marshal(itemJSON{
		ID:                   it.ID,
		Category:             it.Category,
		Name:                 it.Name,
		Amount:               it.Amount,
		Frequency:            it.Frequency,
		IncomeType:           it.IncomeType,
		SelfEmployment:       it.SelfEmployment,
		QualifiedDividends:   it.QualifiedDividends,
		Benefits:             it.Benefits,
		Loan:                 it.Loan,
		ScenarioID:           it.ScenarioID,
		Replaces:             it.Replaces,
		AsOfYear:             it.AsOfYear,
		AsOfMonth:            it.AsOfMonth,
		ReturnRate:           it.ReturnRate,
		StartYear:            it.StartYear,
		StartMonth:           it.StartMonth,
		EndYear:              it.EndYear,
		EndMonth:             it.EndMonth,
		WithdrawalStartYear:  it.WithdrawalStartYear,
		WithdrawalStartMonth: it.WithdrawalStartMonth,
		WithdrawalRate:       it.WithdrawalRate,
		Account:              it.Account,
		AssetClass:           it.AssetClass,
		IncomeRate:           it.IncomeRate,
		TaxExempt:            it.TaxExempt,
		Workplace:            it.Workplace,
		CreatedAt:            fields.FormatTime(it.CreatedAt),
		UpdatedAt:            fields.FormatTime(it.UpdatedAt),
	})
}

// ItemInput is the PUT request body. Pointer fields distinguish "absent/null"
// from zero values so validation can report missing fields.
type ItemInput struct {
	ID                   *string         `json:"id"`
	Category             *string         `json:"category"`
	Name                 *string         `json:"name"`
	Amount               *float64        `json:"amount"`
	Frequency            *string         `json:"frequency"`
	IncomeType           *string         `json:"incomeType"`
	SelfEmployment       *bool           `json:"selfEmployment"`
	QualifiedDividends   *bool           `json:"qualifiedDividends"`
	Benefits             *BenefitsInput  `json:"benefits"`
	Loan                 *LoanInput      `json:"loan"`
	ScenarioID           *string         `json:"scenarioId"`
	Replaces             *string         `json:"replaces"`
	AsOfYear             *int            `json:"asOfYear"`
	AsOfMonth            *int            `json:"asOfMonth"`
	ReturnRate           *float64        `json:"returnRate"`
	StartYear            *int            `json:"startYear"`
	StartMonth           *int            `json:"startMonth"`
	EndYear              *int            `json:"endYear"`
	EndMonth             *int            `json:"endMonth"`
	WithdrawalStartYear  *int            `json:"withdrawalStartYear"`
	WithdrawalStartMonth *int            `json:"withdrawalStartMonth"`
	WithdrawalRate       *float64        `json:"withdrawalRate"`
	Account              *string         `json:"account"`
	AssetClass           *string         `json:"assetClass"`
	IncomeRate           *float64        `json:"incomeRate"`
	TaxExempt            *TaxExempt      `json:"taxExempt"`
	Workplace            *WorkplaceInput `json:"workplace"`
	CreatedAt            *string         `json:"createdAt"`
	// UpdatedAt is accepted so clients can send back a full item, but it is
	// ignored: the server sets updatedAt on every write.
	UpdatedAt *string `json:"updatedAt"`
}
