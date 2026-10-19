package items

import (
	"slices"
	"strings"

	"github.com/mustard-money/customer-data/fields"
)

// Workplace links an investment to the W-2 income item whose workplace plan
// it is: the income holds the plan's contributions (Benefits), the
// investment its balance. The app keeps the two matched (the website's
// data/workplace-links.ts). IncomeID is an id only, like ScenarioID: items
// sync independently, so it isn't checked against the customer's items.
type Workplace struct {
	IncomeID string `json:"incomeId"`
	Plan     string `json:"plan"`
}

// WorkplaceAccounts are the account types each workplace plan can be.
var WorkplaceAccounts = map[string][]string{
	"retirement": {"traditional401k", "roth401k", "traditional403b", "roth403b"},
	"plan457":    {"traditional457b", "roth457b"},
	"hsa":        {"hsa"},
}

// WorkplacePlans are the plans a workplace link can name, in display order.
var WorkplacePlans = []string{"retirement", "plan457", "hsa"}

// WorkplaceInput is the request shape of Workplace.
type WorkplaceInput struct {
	IncomeID *string `json:"incomeId"`
	Plan     *string `json:"plan"`
}

// validate checks the link: a valid income id, a known plan, and an
// account type that plan can be.
func (in *WorkplaceInput) validate(account *string) (*Workplace, error) {
	if in.IncomeID == nil {
		return nil, fields.Invalid("workplace.incomeId is required")
	}
	if err := fields.ValidateID(*in.IncomeID); err != nil {
		return nil, fields.Invalid("workplace.incomeId must be an item id")
	}
	if in.Plan == nil || !slices.Contains(WorkplacePlans, *in.Plan) {
		return nil, fields.Invalid("workplace.plan must be one of: %s", strings.Join(WorkplacePlans, ", "))
	}
	allowed := WorkplaceAccounts[*in.Plan]
	if account == nil || !slices.Contains(allowed, *account) {
		return nil, fields.Invalid("a %s workplace account must have account %s", *in.Plan, strings.Join(allowed, ", "))
	}
	return &Workplace{IncomeID: *in.IncomeID, Plan: *in.Plan}, nil
}
