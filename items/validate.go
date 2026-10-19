package items

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// maxAmount is the largest value NUMERIC(14,2) can hold.
const maxAmount = 999_999_999_999.99

// ValidCategory reports whether c is a known category.
func ValidCategory(c string) bool {
	switch c {
	case CategoryIncome, CategoryInvestments, CategoryExpenses:
		return true
	}
	return false
}

// Validate checks a PUT body against the path id and returns the normalized
// item to store (name trimmed, amount rounded to cents, createdAt in UTC with
// millisecond precision). UpdatedAt is left zero; the store sets it.
// Errors are *fields.ValidationError.
func (in ItemInput) Validate(pathID string) (Item, error) {
	if err := fields.ValidateBodyID(in.ID, pathID); err != nil {
		return Item{}, err
	}

	if in.Category == nil || *in.Category == "" {
		return Item{}, fields.Invalid("category is required")
	}
	if !ValidCategory(*in.Category) {
		return Item{}, fields.Invalid("category must be one of: income, investments, expenses")
	}

	name, err := fields.Name(in.Name)
	if err != nil {
		return Item{}, err
	}

	if in.Amount == nil {
		return Item{}, fields.Invalid("amount is required")
	}
	amount := *in.Amount
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return Item{}, fields.Invalid("amount must be a number >= 0")
	}
	amount = math.Round(amount*100) / 100
	if amount > maxAmount {
		return Item{}, fields.Invalid("amount must be at most %.2f", maxAmount)
	}

	var frequency *string
	if *in.Category == CategoryInvestments {
		if in.Frequency != nil {
			return Item{}, fields.Invalid("frequency must be omitted or null for investments")
		}
	} else {
		if in.Frequency == nil || *in.Frequency == "" {
			return Item{}, fields.Invalid("frequency is required for %s", *in.Category)
		}
		allowed := allowedFrequencies[*in.Category]
		if !slices.Contains(allowed, *in.Frequency) {
			return Item{}, fields.Invalid("frequency for %s must be one of: %s", *in.Category, strings.Join(allowed, ", "))
		}
		f := *in.Frequency
		frequency = &f
	}

	var incomeType *string
	if in.IncomeType != nil {
		if *in.Category != CategoryIncome {
			return Item{}, fields.Invalid("incomeType must be omitted or null for %s", *in.Category)
		}
		if !slices.Contains(IncomeTypes, *in.IncomeType) {
			return Item{}, fields.Invalid("incomeType must be one of: %s", strings.Join(IncomeTypes, ", "))
		}
		t := *in.IncomeType
		incomeType = &t
	}

	// Only true is stored: false and absent both mean no.
	var selfEmployment *bool
	if in.SelfEmployment != nil && *in.SelfEmployment {
		if *in.Category != CategoryIncome {
			return Item{}, fields.Invalid("selfEmployment must be omitted, null or false for %s", *in.Category)
		}
		yes := true
		selfEmployment = &yes
	}

	// Only false is stored: true and absent both mean qualified.
	var qualifiedDividends *bool
	if in.QualifiedDividends != nil && !*in.QualifiedDividends {
		if incomeType == nil || *incomeType != "1099div" {
			return Item{}, fields.Invalid("qualifiedDividends must be omitted, null or true unless category is income and incomeType is 1099div")
		}
		no := false
		qualifiedDividends = &no
	}

	var benefits *Benefits
	if in.Benefits != nil {
		if incomeType == nil || *incomeType != "w2" {
			return Item{}, fields.Invalid("benefits must be omitted or null unless category is income and incomeType is w2")
		}
		if benefits, err = in.Benefits.validate(); err != nil {
			return Item{}, err
		}
	}

	var loan *Loan
	if in.Loan != nil {
		if *in.Category != CategoryExpenses || frequency == nil || *frequency != FrequencyMonthly {
			return Item{}, fields.Invalid("loan must be omitted or null unless category is expenses and frequency is monthly")
		}
		if loan, err = in.Loan.validate(amount); err != nil {
			return Item{}, err
		}
	}

	scenarioID, replaces, err := in.validateScenario(pathID)
	if err != nil {
		return Item{}, err
	}

	var account *string
	if in.Account != nil {
		if *in.Category != CategoryInvestments {
			return Item{}, fields.Invalid("account must be omitted or null for %s", *in.Category)
		}
		if !slices.Contains(Accounts, *in.Account) {
			return Item{}, fields.Invalid("account must be one of: %s", strings.Join(Accounts, ", "))
		}
		a := *in.Account
		account = &a
	}

	var workplace *Workplace
	if in.Workplace != nil {
		if *in.Category != CategoryInvestments {
			return Item{}, fields.Invalid("workplace must be omitted or null for %s", *in.Category)
		}
		if workplace, err = in.Workplace.validate(account); err != nil {
			return Item{}, err
		}
	}

	asOfYear, asOfMonth, returnRate, err := in.validateInvestmentValue()
	if err != nil {
		return Item{}, err
	}

	dates, err := in.validateDates()
	if err != nil {
		return Item{}, err
	}
	withdrawals, err := in.validateWithdrawals(account)
	if err != nil {
		return Item{}, err
	}
	asset, err := in.validateAssetClass(account)
	if err != nil {
		return Item{}, err
	}

	createdAt, err := fields.CreatedAt(in.CreatedAt)
	if err != nil {
		return Item{}, err
	}

	return Item{
		ID:                   pathID,
		Category:             *in.Category,
		Name:                 name,
		Amount:               amount,
		Frequency:            frequency,
		IncomeType:           incomeType,
		SelfEmployment:       selfEmployment,
		QualifiedDividends:   qualifiedDividends,
		Benefits:             benefits,
		Loan:                 loan,
		ScenarioID:           scenarioID,
		Replaces:             replaces,
		AsOfYear:             asOfYear,
		AsOfMonth:            asOfMonth,
		ReturnRate:           returnRate,
		StartYear:            dates.startYear,
		StartMonth:           dates.startMonth,
		EndYear:              dates.endYear,
		EndMonth:             dates.endMonth,
		WithdrawalStartYear:  withdrawals.year,
		WithdrawalStartMonth: withdrawals.month,
		WithdrawalRate:       withdrawals.rate,
		Account:              account,
		AssetClass:           asset.class,
		IncomeRate:           asset.incomeRate,
		TaxExempt:            asset.taxExempt,
		Workplace:            workplace,
		CreatedAt:            createdAt,
	}, nil
}

// maxReturnRate bounds expected yearly returns in either direction (50%).
const maxReturnRate = 0.5

// validateInvestmentValue checks an investment's optional as-of date and
// return. asOfYear runs from 1900 to next year (a device ahead of UTC can
// already be in the new year); asOfMonth needs asOfYear. Other categories
// must leave all three out.
func (in ItemInput) validateInvestmentValue() (asOfYear, asOfMonth *int, returnRate *float64, err error) {
	if *in.Category != CategoryInvestments {
		for name, set := range map[string]bool{
			"asOfYear":   in.AsOfYear != nil,
			"asOfMonth":  in.AsOfMonth != nil,
			"returnRate": in.ReturnRate != nil,
		} {
			if set {
				return nil, nil, nil, fields.Invalid("%s must be omitted or null for %s", name, *in.Category)
			}
		}
		return nil, nil, nil, nil
	}
	if in.AsOfYear != nil {
		maxYear := time.Now().UTC().Year() + 1
		if *in.AsOfYear < 1900 || *in.AsOfYear > maxYear {
			return nil, nil, nil, fields.Invalid("asOfYear must be between 1900 and %d", maxYear)
		}
		y := *in.AsOfYear
		asOfYear = &y
	}
	if in.AsOfMonth != nil {
		if asOfYear == nil {
			return nil, nil, nil, fields.Invalid("asOfMonth requires asOfYear")
		}
		if *in.AsOfMonth < 1 || *in.AsOfMonth > 12 {
			return nil, nil, nil, fields.Invalid("asOfMonth must be between 1 and 12")
		}
		m := *in.AsOfMonth
		asOfMonth = &m
	}
	if in.ReturnRate != nil {
		r := *in.ReturnRate
		if math.IsNaN(r) || r < -maxReturnRate || r > maxReturnRate {
			return nil, nil, nil, fields.Invalid("returnRate must be between %g and %g", -maxReturnRate, maxReturnRate)
		}
		returnRate = &r
	}
	return asOfYear, asOfMonth, returnRate, nil
}

// Year bounds for start/end dates and withdrawal starts.
const minDateYear, maxDateYear = 1900, 2150

// RetirementAccounts are the account types that can have a withdrawal plan.
var RetirementAccounts = []string{
	"traditionalIra", "rothIra", "traditional401k", "roth401k",
	"traditional403b", "roth403b", "traditional457b", "roth457b",
}

// monthYear checks an optional year (and its month, which needs the year).
func monthYear(year, month *int, name string) (*int, *int, error) {
	if year == nil {
		if month != nil {
			return nil, nil, fields.Invalid("%sMonth requires %sYear", name, name)
		}
		return nil, nil, nil
	}
	if *year < minDateYear || *year > maxDateYear {
		return nil, nil, fields.Invalid("%sYear must be between %d and %d", name, minDateYear, maxDateYear)
	}
	y := *year
	if month == nil {
		return &y, nil, nil
	}
	if *month < 1 || *month > 12 {
		return nil, nil, fields.Invalid("%sMonth must be between 1 and 12", name)
	}
	m := *month
	return &y, &m, nil
}

type effectiveDates struct{ startYear, startMonth, endYear, endMonth *int }

// validateDates checks the optional start and end of an income or expense
// (investments have none): the end (December when no month) not before the
// start (January when no month).
func (in ItemInput) validateDates() (effectiveDates, error) {
	if *in.Category == CategoryInvestments {
		if in.StartYear != nil || in.StartMonth != nil || in.EndYear != nil || in.EndMonth != nil {
			return effectiveDates{}, fields.Invalid("start and end dates must be omitted or null for %s", *in.Category)
		}
		return effectiveDates{}, nil
	}
	var d effectiveDates
	var err error
	if d.startYear, d.startMonth, err = monthYear(in.StartYear, in.StartMonth, "start"); err != nil {
		return effectiveDates{}, err
	}
	if d.endYear, d.endMonth, err = monthYear(in.EndYear, in.EndMonth, "end"); err != nil {
		return effectiveDates{}, err
	}
	// A one-time expense happens in its start month; it has no end.
	if in.Frequency != nil && *in.Frequency == FrequencyOnce && d.endYear != nil {
		return effectiveDates{}, fields.Invalid("end date must be omitted or null for one-time expenses")
	}
	if d.startYear != nil && d.endYear != nil {
		first := *d.startYear*12 + monthOr(d.startMonth, 1)
		last := *d.endYear*12 + monthOr(d.endMonth, 12)
		if last < first {
			return effectiveDates{}, fields.Invalid("end must not be before start")
		}
	}
	return d, nil
}

func monthOr(month *int, fallback int) int {
	if month == nil {
		return fallback
	}
	return *month
}

type withdrawalPlan struct {
	year, month *int
	rate        *float64
}

// validateWithdrawals checks a retirement account's optional withdrawal plan:
// start and rate together, rate above 0 and at most 1.
func (in ItemInput) validateWithdrawals(account *string) (withdrawalPlan, error) {
	if in.WithdrawalStartYear == nil && in.WithdrawalStartMonth == nil && in.WithdrawalRate == nil {
		return withdrawalPlan{}, nil
	}
	if account == nil || !slices.Contains(RetirementAccounts, *account) {
		return withdrawalPlan{}, fields.Invalid("withdrawals are only for investments in retirement accounts (%s)", strings.Join(RetirementAccounts, ", "))
	}
	if in.WithdrawalStartYear == nil || in.WithdrawalRate == nil {
		return withdrawalPlan{}, fields.Invalid("withdrawalStartYear and withdrawalRate must be given together")
	}
	year, month, err := monthYear(in.WithdrawalStartYear, in.WithdrawalStartMonth, "withdrawalStart")
	if err != nil {
		return withdrawalPlan{}, err
	}
	rate := *in.WithdrawalRate
	if math.IsNaN(rate) || rate <= 0 || rate > 1 {
		return withdrawalPlan{}, fields.Invalid("withdrawalRate must be above 0 and at most 1")
	}
	return withdrawalPlan{year: year, month: month, rate: &rate}, nil
}

// validateScenario checks which scenario the item belongs to (none: Main)
// and the Main item it replaces there: valid ids, and replaces only on a
// scenario's item and never the item itself.
func (in ItemInput) validateScenario(pathID string) (scenarioID, replaces *string, err error) {
	if in.ScenarioID != nil {
		if err := fields.ValidateID(*in.ScenarioID); err != nil {
			return nil, nil, fields.Invalid("scenarioId must be 1-64 characters of A-Z, a-z, 0-9, _ or -")
		}
		s := *in.ScenarioID
		scenarioID = &s
	}
	if in.Replaces != nil {
		if scenarioID == nil {
			return nil, nil, fields.Invalid("replaces requires scenarioId")
		}
		if err := fields.ValidateID(*in.Replaces); err != nil || *in.Replaces == pathID {
			return nil, nil, fields.Invalid("replaces must be the id of another item")
		}
		r := *in.Replaces
		replaces = &r
	}
	return scenarioID, replaces, nil
}
