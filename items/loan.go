package items

import (
	"math"
	"slices"
	"strings"

	"github.com/mustard-money/customer-data/fields"
)

// LoanKinds lists, in display order, the kinds of debt an expense can be the
// payment on. Must match LoanKinds in the website's src/data/loans.ts.
var LoanKinds = []string{"mortgage", "autoLoan", "studentLoan", "otherLoan"}

// Loan makes an expense the monthly payment on a debt: the item's amount is
// the payment, and the loan records what's still owed (Balance, as of the
// as-of month, or today) and its yearly interest rate (0.065 is 6.5% APR),
// from which the app works out the payoff month. A mortgage can also record
// the home's value, which counts toward net worth. Stored as-is in the
// items.loan jsonb column.
type Loan struct {
	Kind         string   `json:"kind"`
	Balance      float64  `json:"balance"`
	InterestRate float64  `json:"interestRate"`
	AsOfYear     *int     `json:"asOfYear,omitempty"`
	AsOfMonth    *int     `json:"asOfMonth,omitempty"`
	HomeValue    *float64 `json:"homeValue,omitempty"`
}

// LoanInput is a loan in a request body.
type LoanInput struct {
	Kind         *string  `json:"kind"`
	Balance      *float64 `json:"balance"`
	InterestRate *float64 `json:"interestRate"`
	AsOfYear     *int     `json:"asOfYear"`
	AsOfMonth    *int     `json:"asOfMonth"`
	HomeValue    *float64 `json:"homeValue"`
}

// maxInterestRate bounds a loan's APR (100%, beyond any real loan).
const maxInterestRate = 1.0

// validate checks the loan of an expense whose monthly payment is payment.
func (in *LoanInput) validate(payment float64) (*Loan, error) {
	if in.Kind == nil || !slices.Contains(LoanKinds, *in.Kind) {
		return nil, fields.Invalid("loan.kind must be one of: %s", strings.Join(LoanKinds, ", "))
	}
	balance, err := dollars(in.Balance, "loan.balance")
	if err != nil {
		return nil, err
	}
	if in.InterestRate == nil {
		return nil, fields.Invalid("loan.interestRate is required")
	}
	rate := *in.InterestRate
	if math.IsNaN(rate) || rate < 0 || rate > maxInterestRate {
		return nil, fields.Invalid("loan.interestRate must be between 0 and %g", maxInterestRate)
	}
	// A payment that doesn't cover the interest never pays the loan off.
	if balance > 0 && payment <= math.Round(balance*rate/12*100)/100 {
		return nil, fields.Invalid("amount (the monthly payment) must be more than the loan's monthly interest")
	}
	asOf, err := (&BalanceInput{Amount: &balance, AsOfYear: in.AsOfYear, AsOfMonth: in.AsOfMonth}).validate("loan")
	if err != nil {
		return nil, err
	}
	loan := &Loan{Kind: *in.Kind, Balance: balance, InterestRate: rate, AsOfYear: asOf.AsOfYear, AsOfMonth: asOf.AsOfMonth}
	if in.HomeValue != nil {
		if loan.Kind != "mortgage" {
			return nil, fields.Invalid("loan.homeValue is only for mortgages")
		}
		home, err := dollars(in.HomeValue, "loan.homeValue")
		if err != nil {
			return nil, err
		}
		loan.HomeValue = &home
	}
	return loan, nil
}
