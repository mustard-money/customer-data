package items

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

func ptr[T any](v T) *T { return &v }

func validInput() ItemInput {
	return ItemInput{
		Category:  ptr("income"),
		Name:      ptr("  Salary  "),
		Amount:    ptr(6500.0),
		Frequency: ptr("monthly"),
		CreatedAt: ptr("2026-09-27T06:20:39.033Z"),
	}
}

func TestValidateOK(t *testing.T) {
	it, err := validInput().Validate("lz3k9abc12")
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "lz3k9abc12" || it.Name != "Salary" || it.Amount != 6500 || *it.Frequency != "monthly" {
		t.Errorf("unexpected item: %+v", it)
	}
	want := time.Date(2026, 9, 27, 6, 20, 39, 33_000_000, time.UTC)
	if !it.CreatedAt.Equal(want) || it.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt = %v, want %v", it.CreatedAt, want)
	}
}

func TestValidateNormalization(t *testing.T) {
	in := validInput()
	in.ID = ptr("abc")
	in.Amount = ptr(0.1 + 0.2)
	in.CreatedAt = ptr("2026-09-27T08:20:39.0339+02:00")
	in.UpdatedAt = ptr("ignored")
	it, err := in.Validate("abc")
	if err != nil {
		t.Fatal(err)
	}
	if it.Amount != 0.3 {
		t.Errorf("Amount = %v, want 0.3", it.Amount)
	}
	if got := fields.FormatTime(it.CreatedAt); got != "2026-09-27T06:20:39.033Z" {
		t.Errorf("CreatedAt = %s", got)
	}
}

func TestValidateInvestments(t *testing.T) {
	in := validInput()
	in.Category = ptr("investments")
	in.Frequency = nil
	it, err := in.Validate("inv1")
	if err != nil {
		t.Fatal(err)
	}
	if it.Frequency != nil {
		t.Errorf("Frequency = %v, want nil", *it.Frequency)
	}
	// Investments saved before accounts existed have none.
	if it.Account != nil {
		t.Errorf("Account = %v, want nil", *it.Account)
	}
}

func TestValidateIncomeTypes(t *testing.T) {
	// Income saved before income types existed has none.
	it, err := validInput().Validate("a")
	if err != nil || it.IncomeType != nil {
		t.Errorf("no income type: it = %+v, err = %v", it, err)
	}
	for _, ty := range IncomeTypes {
		in := validInput()
		in.IncomeType = ptr(ty)
		it, err := in.Validate("a")
		if err != nil || it.IncomeType == nil || *it.IncomeType != ty {
			t.Errorf("income type %s: it = %+v, err = %v", ty, it, err)
		}
	}
}

func validBenefits() *BenefitsInput {
	return &BenefitsInput{
		Retirement: &RetirementInput{ContributionRate: ptr(0.1), Roth: ptr(true), MatchRate: ptr(0.5), MatchUpTo: ptr(0.06)},
		Plan457:    &Plan457Input{ContributionRate: ptr(0.05)},
		HSA:        &HSAInput{EmployeeAnnual: ptr(4000.004), EmployerAnnual: ptr(1000.0), Family: ptr(true)},
	}
}

func TestValidateBenefits(t *testing.T) {
	in := validInput()
	in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
	it, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	b := it.Benefits
	if b == nil || !b.Retirement.Roth || b.Retirement.MatchUpTo != 0.06 || b.Plan457.ContributionRate != 0.05 || b.Plan457.Roth || !b.HSA.Family || b.HSA.EmployeeAnnual != 4000 {
		t.Errorf("benefits = %+v", b)
	}

	// An empty benefits object is the same as none.
	in.Benefits = &BenefitsInput{}
	if it, err := in.Validate("a"); err != nil || it.Benefits != nil {
		t.Errorf("empty benefits: %+v, %v", it.Benefits, err)
	}
}

func TestValidateInvestmentValue(t *testing.T) {
	in := validInput()
	in.Category, in.Frequency = ptr("investments"), nil
	in.AsOfYear, in.AsOfMonth, in.ReturnRate = ptr(2025), ptr(3), ptr(-0.02)
	it, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	if *it.AsOfYear != 2025 || *it.AsOfMonth != 3 || *it.ReturnRate != -0.02 {
		t.Errorf("item = %+v", it)
	}
}

func TestValidateIncomeDates(t *testing.T) {
	in := validInput()
	in.StartYear, in.StartMonth, in.EndYear = ptr(2026), ptr(9), ptr(2026)
	it, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	if *it.StartYear != 2026 || *it.StartMonth != 9 || *it.EndYear != 2026 || it.EndMonth != nil {
		t.Errorf("item = %+v", it)
	}
}

func TestValidateExpenseDates(t *testing.T) {
	in := validInput()
	in.Category, in.Frequency = ptr("expenses"), ptr("weekly")
	in.StartYear, in.EndYear, in.EndMonth = ptr(2027), ptr(2029), ptr(6)
	it, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	if *it.StartYear != 2027 || it.StartMonth != nil || *it.EndYear != 2029 || *it.EndMonth != 6 {
		t.Errorf("item = %+v", it)
	}
}

func TestValidateExpenseFrequencies(t *testing.T) {
	for _, f := range []string{"once", "daily", "weekly", "monthly", "yearly"} {
		in := validInput()
		in.Category, in.Frequency = ptr("expenses"), ptr(f)
		it, err := in.Validate("a")
		if err != nil || *it.Frequency != f {
			t.Errorf("expenses %s: it = %+v, err = %v", f, it, err)
		}
	}
}

func TestValidateWithdrawals(t *testing.T) {
	in := validInput()
	in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("roth401k")
	in.WithdrawalStartYear, in.WithdrawalStartMonth, in.WithdrawalRate = ptr(2045), ptr(7), ptr(0.04)
	it, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	if *it.WithdrawalStartYear != 2045 || *it.WithdrawalStartMonth != 7 || *it.WithdrawalRate != 0.04 {
		t.Errorf("item = %+v", it)
	}
}

func TestValidateInvestmentAccounts(t *testing.T) {
	for _, a := range Accounts {
		in := validInput()
		in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr(a)
		it, err := in.Validate("inv1")
		if err != nil || it.Account == nil || *it.Account != a {
			t.Errorf("account %s: it = %+v, err = %v", a, it, err)
		}
	}
}

func TestValidateIncomeFrequencies(t *testing.T) {
	for _, f := range []string{"weekly", "biweekly", "semimonthly", "monthly", "quarterly", "semiannually", "yearly"} {
		in := validInput()
		in.Frequency = ptr(f)
		it, err := in.Validate("a")
		if err != nil || *it.Frequency != f {
			t.Errorf("income %s: it = %+v, err = %v", f, it, err)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		pathID  string
		mutate  func(*ItemInput)
		wantMsg string
	}{
		{"id mismatch", "a", func(in *ItemInput) { in.ID = ptr("b") }, "does not match"},
		{"bad path id", "has space", func(*ItemInput) {}, "id must contain only"},
		{"path id too long", strings.Repeat("a", 65), func(*ItemInput) {}, "at most 64"},
		{"missing category", "a", func(in *ItemInput) { in.Category = nil }, "category is required"},
		{"bad category", "a", func(in *ItemInput) { in.Category = ptr("savings") }, "category must be one of"},
		{"missing name", "a", func(in *ItemInput) { in.Name = nil }, "name is required"},
		{"blank name", "a", func(in *ItemInput) { in.Name = ptr("   ") }, "must not be blank"},
		{"long name", "a", func(in *ItemInput) { in.Name = ptr(strings.Repeat("x", 201)) }, "at most 200"},
		{"missing amount", "a", func(in *ItemInput) { in.Amount = nil }, "amount is required"},
		{"negative amount", "a", func(in *ItemInput) { in.Amount = ptr(-1.0) }, "amount must be"},
		{"huge amount", "a", func(in *ItemInput) { in.Amount = ptr(1e13) }, "amount must be at most"},
		{"missing frequency", "a", func(in *ItemInput) { in.Frequency = nil }, "frequency is required"},
		{"bad frequency", "a", func(in *ItemInput) { in.Frequency = ptr("daily") }, "frequency for income must be one of: weekly, biweekly, semimonthly, monthly, quarterly, semiannually, yearly"},
		{"income-only frequency on expense", "a", func(in *ItemInput) { in.Category, in.Frequency = ptr("expenses"), ptr("biweekly") }, "frequency for expenses must be one of: once, daily, weekly, monthly, yearly"},
		{"one-time income", "a", func(in *ItemInput) { in.Frequency = ptr("once") }, "frequency for income must be one of:"},
		{"one-time expense with an end", "a", func(in *ItemInput) {
			in.Category, in.Frequency, in.StartYear, in.EndYear = ptr("expenses"), ptr("once"), ptr(2027), ptr(2028)
		}, "end date must be omitted or null for one-time expenses"},
		{"investments with frequency", "a", func(in *ItemInput) { in.Category = ptr("investments") }, "must be omitted or null"},
		{"benefits without w2", "a", func(in *ItemInput) { in.IncomeType, in.Benefits = ptr("1099nec"), validBenefits() }, "benefits must be omitted or null unless category is income and incomeType is w2"},
		{"benefits without income type", "a", func(in *ItemInput) { in.Benefits = validBenefits() }, "benefits must be omitted"},
		{"missing match limit", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.Retirement.MatchUpTo = nil
		}, "benefits.retirement.matchUpTo is required"},
		{"contribution over 100%", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.Retirement.ContributionRate = ptr(1.5)
		}, "benefits.retirement.contributionRate must be between 0 and 1"},
		{"match over 200%", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.Retirement.MatchRate = ptr(2.5)
		}, "benefits.retirement.matchRate must be between 0 and 2"},
		{"missing 457 contribution", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.Plan457.ContributionRate = nil
		}, "benefits.plan457.contributionRate is required"},
		{"balance missing amount", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.HSA.Balance = &BalanceInput{AsOfYear: ptr(2025)}
		}, "benefits.hsa.balance.amount is required"},
		{"balance month without year", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.Retirement.Balance = &BalanceInput{Amount: ptr(1.0), AsOfMonth: ptr(3)}
		}, "benefits.retirement.balance.asOfMonth requires asOfYear"},
		{"balance far future", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.Plan457.Balance = &BalanceInput{Amount: ptr(1.0), AsOfYear: ptr(3000)}
		}, "benefits.plan457.balance.asOfYear must be between 1900 and"},
		{"negative hsa", "a", func(in *ItemInput) {
			in.IncomeType, in.Benefits = ptr("w2"), validBenefits()
			in.Benefits.HSA.EmployerAnnual = ptr(-1.0)
		}, "benefits.hsa.employerAnnual must be between 0"},
		{"asOfYear on income", "a", func(in *ItemInput) { in.AsOfYear = ptr(2025) }, "asOfYear must be omitted or null for income"},
		{"returnRate on expenses", "a", func(in *ItemInput) { in.Category, in.ReturnRate = ptr("expenses"), ptr(0.05) }, "returnRate must be omitted or null for expenses"},
		{"asOfYear too early", "a", func(in *ItemInput) { in.Category, in.Frequency, in.AsOfYear = ptr("investments"), nil, ptr(1899) }, "asOfYear must be between 1900 and"},
		{"asOfYear far future", "a", func(in *ItemInput) { in.Category, in.Frequency, in.AsOfYear = ptr("investments"), nil, ptr(3000) }, "asOfYear must be between 1900 and"},
		{"asOfMonth without year", "a", func(in *ItemInput) { in.Category, in.Frequency, in.AsOfMonth = ptr("investments"), nil, ptr(3) }, "asOfMonth requires asOfYear"},
		{"asOfMonth 13", "a", func(in *ItemInput) {
			in.Category, in.Frequency, in.AsOfYear, in.AsOfMonth = ptr("investments"), nil, ptr(2025), ptr(13)
		}, "asOfMonth must be between 1 and 12"},
		{"returnRate too high", "a", func(in *ItemInput) { in.Category, in.Frequency, in.ReturnRate = ptr("investments"), nil, ptr(0.75) }, "returnRate must be between -0.5 and 0.5"},
		{"dates on investments", "a", func(in *ItemInput) { in.Category, in.Frequency, in.StartYear = ptr("investments"), nil, ptr(2026) }, "start and end dates must be omitted or null for investments"},
		{"expense ends before it starts", "a", func(in *ItemInput) {
			in.Category, in.StartYear, in.EndYear = ptr("expenses"), ptr(2030), ptr(2029)
		}, "end must not be before start"},
		{"start month without year", "a", func(in *ItemInput) { in.StartMonth = ptr(3) }, "startMonth requires startYear"},
		{"end month 13", "a", func(in *ItemInput) { in.EndYear, in.EndMonth = ptr(2030), ptr(13) }, "endMonth must be between 1 and 12"},
		{"end before start", "a", func(in *ItemInput) {
			in.StartYear, in.StartMonth, in.EndYear, in.EndMonth = ptr(2030), ptr(6), ptr(2030), ptr(5)
		}, "end must not be before start"},
		{"withdrawals on taxable", "a", func(in *ItemInput) {
			in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("taxable")
			in.WithdrawalStartYear, in.WithdrawalRate = ptr(2040), ptr(0.04)
		}, "withdrawals are only for investments in retirement accounts"},
		{"withdrawal rate without start", "a", func(in *ItemInput) {
			in.Category, in.Frequency, in.Account, in.WithdrawalRate = ptr("investments"), nil, ptr("rothIra"), ptr(0.04)
		}, "withdrawalStartYear and withdrawalRate must be given together"},
		{"withdrawal rate zero", "a", func(in *ItemInput) {
			in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("rothIra")
			in.WithdrawalStartYear, in.WithdrawalRate = ptr(2040), ptr(0.0)
		}, "withdrawalRate must be above 0 and at most 1"},
		{"incomeType on expenses", "a", func(in *ItemInput) { in.Category, in.IncomeType = ptr("expenses"), ptr("w2") }, "incomeType must be omitted or null for expenses"},
		{"incomeType on investments", "a", func(in *ItemInput) { in.Category, in.Frequency, in.IncomeType = ptr("investments"), nil, ptr("w2") }, "incomeType must be omitted or null for investments"},
		{"bad incomeType", "a", func(in *ItemInput) { in.IncomeType = ptr("1040") }, "incomeType must be one of: w2, 1099nec, 1099misc, 1099k, k1, 1099int, 1099div, 1099b, 1099r, ssa1099, 1099g, other"},
		{"empty incomeType", "a", func(in *ItemInput) { in.IncomeType = ptr("") }, "incomeType must be one of"},
		{"account on income", "a", func(in *ItemInput) { in.Account = ptr("rothIra") }, "account must be omitted or null for income"},
		{"account on expenses", "a", func(in *ItemInput) { in.Category, in.Account = ptr("expenses"), ptr("hsa") }, "account must be omitted or null for expenses"},
		{"bad account", "a", func(in *ItemInput) { in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("savings") }, "account must be one of: taxable, bank, cd, hsa, traditionalIra, rothIra, traditional401k, roth401k, traditional403b, roth403b, traditional457b, roth457b"},
		{"empty account", "a", func(in *ItemInput) { in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("") }, "account must be one of"},
		{"missing createdAt", "a", func(in *ItemInput) { in.CreatedAt = nil }, "createdAt is required"},
		{"bad createdAt", "a", func(in *ItemInput) { in.CreatedAt = ptr("yesterday") }, "createdAt must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.mutate(&in)
			_, err := in.Validate(tt.pathID)
			var ve *fields.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if !strings.Contains(ve.Msg, tt.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", ve.Msg, tt.wantMsg)
			}
		})
	}
}

func TestItemMarshalJSON(t *testing.T) {
	created := time.Date(2026, 9, 27, 6, 20, 39, 30_000_000, time.UTC)
	it := Item{ID: "x", Category: "investments", Name: "ETF", Amount: 12.5, CreatedAt: created, UpdatedAt: created.Add(70 * time.Millisecond)}
	b, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"x","category":"investments","name":"ETF","amount":12.5,"createdAt":"2026-09-27T06:20:39.030Z","updatedAt":"2026-09-27T06:20:39.100Z"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func mortgageInput() ItemInput {
	in := validInput()
	in.Category, in.Frequency, in.Amount = ptr("expenses"), ptr("monthly"), ptr(2500.0)
	in.Loan = &LoanInput{Kind: ptr("mortgage"), Balance: ptr(400000.0), InterestRate: ptr(0.065),
		AsOfYear: ptr(2026), AsOfMonth: ptr(9), HomeValue: ptr(750000.0)}
	return in
}

func TestValidateLoan(t *testing.T) {
	it, err := mortgageInput().Validate("m")
	if err != nil {
		t.Fatal(err)
	}
	l := it.Loan
	if l.Kind != "mortgage" || l.Balance != 400000 || l.InterestRate != 0.065 || *l.AsOfYear != 2026 ||
		*l.AsOfMonth != 9 || *l.HomeValue != 750000 {
		t.Errorf("loan = %+v", l)
	}
	// A paid-off balance needs no particular payment.
	in := mortgageInput()
	in.Loan.Balance, in.Amount = ptr(0.0), ptr(0.0)
	if _, err := in.Validate("m"); err != nil {
		t.Errorf("zero balance: %v", err)
	}
}

func TestValidateLoanErrors(t *testing.T) {
	cases := []struct {
		name   string
		modify func(in *ItemInput)
		want   string
	}{
		{"on income", func(in *ItemInput) { in.Category = ptr("income") }, "loan must be omitted or null unless category is expenses and frequency is monthly"},
		{"yearly", func(in *ItemInput) { in.Frequency = ptr("yearly") }, "loan must be omitted or null unless"},
		{"bad kind", func(in *ItemInput) { in.Loan.Kind = ptr("boat") }, "loan.kind must be one of: mortgage, autoLoan, studentLoan, otherLoan"},
		{"no balance", func(in *ItemInput) { in.Loan.Balance = nil }, "loan.balance is required"},
		{"no rate", func(in *ItemInput) { in.Loan.InterestRate = nil }, "loan.interestRate is required"},
		{"rate over 100%", func(in *ItemInput) { in.Loan.InterestRate = ptr(1.5) }, "loan.interestRate must be between 0 and 1"},
		// 6.5% of $400,000 is $2,166.67 a month in interest.
		{"payment below interest", func(in *ItemInput) { in.Amount = ptr(2100.0) }, "must be more than the loan's monthly interest"},
		{"home value on a car loan", func(in *ItemInput) { in.Loan.Kind = ptr("autoLoan") }, "loan.homeValue is only for mortgages"},
		{"month without year", func(in *ItemInput) { in.Loan.AsOfYear = nil }, "loan.asOfMonth requires asOfYear"},
	}
	for _, c := range cases {
		in := mortgageInput()
		c.modify(&in)
		if _, err := in.Validate("m"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestValidateScenarioFields(t *testing.T) {
	in := validInput()
	in.ScenarioID, in.Replaces = ptr("plan-b"), ptr("rent")
	it, err := in.Validate("rent-b")
	if err != nil {
		t.Fatal(err)
	}
	if *it.ScenarioID != "plan-b" || *it.Replaces != "rent" {
		t.Errorf("item = %+v", it)
	}
	for _, c := range []struct {
		name   string
		modify func(in *ItemInput)
		want   string
	}{
		{"replaces without scenario", func(in *ItemInput) { in.ScenarioID = nil }, "replaces requires scenarioId"},
		{"replaces itself", func(in *ItemInput) { in.Replaces = ptr("rent-b") }, "replaces must be the id of another item"},
		{"bad scenario id", func(in *ItemInput) { in.ScenarioID = ptr("plan b!") }, "scenarioId must be"},
	} {
		in := validInput()
		in.ScenarioID, in.Replaces = ptr("plan-b"), ptr("rent")
		c.modify(&in)
		if _, err := in.Validate("rent-b"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestValidateWorkplace(t *testing.T) {
	workplaceInput := func() ItemInput {
		in := validInput()
		in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("roth401k")
		in.Workplace = &WorkplaceInput{IncomeID: ptr("salary"), Plan: ptr("retirement")}
		return in
	}
	it, err := workplaceInput().Validate("salary-401k")
	if err != nil {
		t.Fatal(err)
	}
	if it.Workplace == nil || it.Workplace.IncomeID != "salary" || it.Workplace.Plan != "retirement" {
		t.Errorf("Workplace = %+v", it.Workplace)
	}
	b, _ := json.Marshal(it)
	if !strings.Contains(string(b), `"workplace":{"incomeId":"salary","plan":"retirement"}`) {
		t.Errorf("JSON = %s", b)
	}

	for _, c := range []struct {
		name   string
		modify func(in *ItemInput)
		want   string
	}{
		{"not an investment", func(in *ItemInput) { in.Category, in.Frequency, in.Account = ptr("income"), ptr("monthly"), nil }, "workplace must be omitted"},
		{"no income", func(in *ItemInput) { in.Workplace.IncomeID = nil }, "workplace.incomeId is required"},
		{"bad income id", func(in *ItemInput) { in.Workplace.IncomeID = ptr("not an id!") }, "workplace.incomeId must be"},
		{"unknown plan", func(in *ItemInput) { in.Workplace.Plan = ptr("pension") }, "workplace.plan must be one of"},
		{"account doesn't match the plan", func(in *ItemInput) { in.Account = ptr("traditional457b") }, "a retirement workplace account must have account"},
		{"no account", func(in *ItemInput) { in.Account = nil }, "a retirement workplace account must have account"},
	} {
		in := workplaceInput()
		c.modify(&in)
		if _, err := in.Validate("salary-401k"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestValidateSelfEmployment(t *testing.T) {
	in := validInput()
	in.SelfEmployment = ptr(true)
	it, err := in.Validate("a")
	if err != nil || it.SelfEmployment == nil || !*it.SelfEmployment {
		t.Fatalf("true: %+v %v", it.SelfEmployment, err)
	}
	// False is the same as absent.
	in.SelfEmployment = ptr(false)
	if it, err := in.Validate("a"); err != nil || it.SelfEmployment != nil {
		t.Errorf("false: %+v %v", it.SelfEmployment, err)
	}
	in = validInput()
	in.Category, in.SelfEmployment = ptr("expenses"), ptr(true)
	if _, err := in.Validate("a"); err == nil || !strings.Contains(err.Error(), "selfEmployment must be omitted") {
		t.Errorf("expenses: %v", err)
	}
	in.SelfEmployment = ptr(false)
	if _, err := in.Validate("a"); err != nil {
		t.Errorf("false on expenses: %v", err)
	}
}

func TestValidateQualifiedDividends(t *testing.T) {
	in := validInput()
	in.IncomeType, in.QualifiedDividends = ptr("1099div"), ptr(false)
	it, err := in.Validate("a")
	if err != nil || it.QualifiedDividends == nil || *it.QualifiedDividends {
		t.Fatalf("false: %+v %v", it.QualifiedDividends, err)
	}
	// True is the default, the same as absent.
	in.QualifiedDividends = ptr(true)
	if it, err := in.Validate("a"); err != nil || it.QualifiedDividends != nil {
		t.Errorf("true: %+v %v", it.QualifiedDividends, err)
	}
	in.IncomeType, in.QualifiedDividends = ptr("w2"), ptr(false)
	if _, err := in.Validate("a"); err == nil || !strings.Contains(err.Error(), "qualifiedDividends must be omitted") {
		t.Errorf("w2: %v", err)
	}
}

func TestValidateAssetClass(t *testing.T) {
	brokerage := func() ItemInput {
		in := validInput()
		in.Category, in.Frequency, in.Account = ptr("investments"), nil, ptr("taxable")
		return in
	}
	in := brokerage()
	in.AssetClass, in.IncomeRate = ptr("fixedIncome"), ptr(0.045)
	in.TaxExempt = &TaxExempt{Federal: true}
	it, err := in.Validate("a")
	if err != nil || *it.AssetClass != "fixedIncome" || *it.IncomeRate != 0.045 || !it.TaxExempt.Federal {
		t.Fatalf("fixed income: %+v %v", it, err)
	}
	// Equities are the default: not stored.
	in = brokerage()
	in.AssetClass = ptr("equities")
	if it, err := in.Validate("a"); err != nil || it.AssetClass != nil {
		t.Errorf("equities: %+v %v", it.AssetClass, err)
	}
	// No exemptions ticked: nothing stored.
	in = brokerage()
	in.AssetClass, in.IncomeRate, in.TaxExempt = ptr("fixedIncome"), ptr(0.04), &TaxExempt{}
	if it, err := in.Validate("a"); err != nil || it.TaxExempt != nil {
		t.Errorf("no exemptions: %+v %v", it.TaxExempt, err)
	}
	for name, c := range map[string]struct {
		edit func(*ItemInput)
		want string
	}{
		"not a brokerage account": {func(in *ItemInput) {
			in.Account, in.AssetClass, in.IncomeRate = ptr("rothIra"), ptr("fixedIncome"), ptr(0.04)
		}, "assetClass must be omitted"},
		"no income rate": {func(in *ItemInput) { in.AssetClass = ptr("fixedIncome") }, "incomeRate is required"},
		"with a return": {func(in *ItemInput) {
			in.AssetClass, in.IncomeRate, in.ReturnRate = ptr("fixedIncome"), ptr(0.04), ptr(0.07)
		}, "returnRate must be omitted"},
		"rate too high":          {func(in *ItemInput) { in.AssetClass, in.IncomeRate = ptr("fixedIncome"), ptr(0.9) }, "incomeRate is required"},
		"equities with income":   {func(in *ItemInput) { in.AssetClass, in.IncomeRate = ptr("equities"), ptr(0.04) }, "need assetClass fixedIncome"},
		"income without a class": {func(in *ItemInput) { in.IncomeRate = ptr(0.04) }, "need assetClass fixedIncome"},
		"unknown class":          {func(in *ItemInput) { in.AssetClass = ptr("crypto") }, "assetClass must be one of"},
	} {
		t.Run(name, func(t *testing.T) {
			in := brokerage()
			c.edit(&in)
			if _, err := in.Validate("a"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}
