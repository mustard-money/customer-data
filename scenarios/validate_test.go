package scenarios

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

func ptr[T any](v T) *T { return &v }

func validParams() *ParametersInput {
	return &ParametersInput{
		Years:             ptr(20),
		ReturnRate:        ptr(0.07),
		Inflation:         ptr(0.03),
		IncomeGrowth:      ptr(0.03),
		IncomeAdjustment:  ptr(0.0),
		ExpenseAdjustment: ptr(0.0),
	}
}

func validInput() ScenarioInput {
	return ScenarioInput{
		Name:    ptr("  Buy a house in 2028  "),
		ItemIDs: &[]string{"mujge6vxt1vsmn", "mujgf43apl78jy"},
		Events: &[]EventInput{
			{ID: ptr("ev1"), Kind: ptr("oneTimeCost"), Name: ptr(" Down payment "), Year: ptr(2), Amount: ptr(250000.0)},
			{ID: ptr("ev2"), Kind: ptr("incomePause"), Name: ptr("Sabbatical"), Year: ptr(3), Amount: ptr(0.0), Months: ptr(6)},
			{ID: ptr("ev3"), Kind: ptr("recurringCost"), Name: ptr("Childcare"), Year: ptr(4), Amount: ptr(30000.0), Years: ptr(5)},
			{ID: ptr("ev4"), Kind: ptr("oneTimeIncome"), Name: ptr("RSU sale"), Year: ptr(1), Amount: ptr(80000.0)},
		},
		Parameters: validParams(),
		CreatedAt:  ptr("2026-09-27T06:20:39.033Z"),
	}
}

// event returns a pointer to the i-th event of in for mutation.
func event(in *ScenarioInput, i int) *EventInput { return &(*in.Events)[i] }

func TestValidateOK(t *testing.T) {
	in := validInput()
	in.ID = ptr("lz3k9abc12")
	in.UpdatedAt = ptr("ignored")
	s, err := in.Validate("lz3k9abc12")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "lz3k9abc12" || s.Name != "Buy a house in 2028" || len(s.ItemIDs) != 2 || len(s.Events) != 4 {
		t.Errorf("unexpected scenario: %+v", s)
	}
	if e := s.Events[0]; e.Name != "Down payment" || e.Amount != 250000 || e.Months != nil || e.Years != nil {
		t.Errorf("event 0 = %+v", e)
	}
	if e := s.Events[1]; e.Months == nil || *e.Months != 6 || e.Years != nil {
		t.Errorf("event 1 = %+v", e)
	}
	if e := s.Events[2]; e.Years == nil || *e.Years != 5 || e.Months != nil {
		t.Errorf("event 2 = %+v", e)
	}
	want := Parameters{Years: 20, ReturnRate: 0.07, Inflation: 0.03, IncomeGrowth: 0.03}
	if s.Parameters != want {
		t.Errorf("parameters = %+v", s.Parameters)
	}
	if got := fields.FormatTime(s.CreatedAt); got != "2026-09-27T06:20:39.033Z" {
		t.Errorf("CreatedAt = %s", got)
	}
}

func TestValidateEmptyArrays(t *testing.T) {
	in := validInput()
	in.ItemIDs = &[]string{}
	in.Events = &[]EventInput{}
	s, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.ItemIDs == nil || len(s.ItemIDs) != 0 || s.Events == nil || len(s.Events) != 0 {
		t.Errorf("want empty non-nil slices: %+v", s)
	}
}

func TestValidateAmountRounding(t *testing.T) {
	in := validInput()
	event(&in, 0).Amount = ptr(0.1 + 0.2)
	s, err := in.Validate("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.Events[0].Amount != 0.3 {
		t.Errorf("Amount = %v, want 0.3", s.Events[0].Amount)
	}
}

func TestValidateBoundaries(t *testing.T) {
	in := validInput()
	in.ItemIDs = new([]string)
	for i := range MaxItemIDs {
		*in.ItemIDs = append(*in.ItemIDs, fmt.Sprintf("item%d", i))
	}
	events := make([]EventInput, MaxEvents)
	for i := range events {
		events[i] = EventInput{ID: ptr(fmt.Sprintf("ev%d", i)), Kind: ptr("oneTimeCost"), Name: ptr("x"), Year: ptr(1), Amount: ptr(0.0)}
	}
	events[0].Year = ptr(60)
	events[1].Amount = ptr(maxAmount)
	events[2] = EventInput{ID: ptr("p"), Kind: ptr("incomePause"), Name: ptr("x"), Year: ptr(1), Amount: ptr(0.0), Months: ptr(60)}
	events[3] = EventInput{ID: ptr("r"), Kind: ptr("recurringCost"), Name: ptr("x"), Year: ptr(1), Amount: ptr(1.0), Years: ptr(1)}
	in.Events = &events
	in.Parameters = &ParametersInput{
		Years: ptr(60), ReturnRate: ptr(-0.5), Inflation: ptr(0.2), IncomeGrowth: ptr(-0.2),
		IncomeAdjustment: ptr(2.0), ExpenseAdjustment: ptr(-0.9),
	}
	if _, err := in.Validate("a"); err != nil {
		t.Fatalf("upper/lower bounds rejected: %v", err)
	}
	in.Parameters = &ParametersInput{
		Years: ptr(1), ReturnRate: ptr(0.5), Inflation: ptr(-0.1), IncomeGrowth: ptr(0.3),
		IncomeAdjustment: ptr(-1.0), ExpenseAdjustment: ptr(2.0),
	}
	if _, err := in.Validate("a"); err != nil {
		t.Fatalf("other bounds rejected: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		pathID  string
		mutate  func(*ScenarioInput)
		wantMsg string
	}{
		// id, name, createdAt
		{"id mismatch", "a", func(in *ScenarioInput) { in.ID = ptr("b") }, "does not match"},
		{"bad path id", "a b", func(*ScenarioInput) {}, "id must contain only"},
		{"path id too long", strings.Repeat("a", 65), func(*ScenarioInput) {}, "at most 64"},
		{"missing name", "a", func(in *ScenarioInput) { in.Name = nil }, "name is required"},
		{"blank name", "a", func(in *ScenarioInput) { in.Name = ptr(" ") }, "name must not be blank"},
		{"long name", "a", func(in *ScenarioInput) { in.Name = ptr(strings.Repeat("x", 201)) }, "name must be at most 200"},
		{"missing createdAt", "a", func(in *ScenarioInput) { in.CreatedAt = nil }, "createdAt is required"},
		{"bad createdAt", "a", func(in *ScenarioInput) { in.CreatedAt = ptr("nope") }, "createdAt must be"},

		// itemIds
		{"missing itemIds", "a", func(in *ScenarioInput) { in.ItemIDs = nil }, "itemIds is required"},
		{"too many itemIds", "a", func(in *ScenarioInput) {
			ids := make([]string, MaxItemIDs+1)
			for i := range ids {
				ids[i] = fmt.Sprintf("i%d", i)
			}
			in.ItemIDs = &ids
		}, "itemIds must have at most 500 entries"},
		{"empty itemId", "a", func(in *ScenarioInput) { in.ItemIDs = &[]string{"ok", ""} }, "itemIds[1] is required"},
		{"bad itemId", "a", func(in *ScenarioInput) { in.ItemIDs = &[]string{"bad.id"} }, "itemIds[0] must contain only"},
		{"long itemId", "a", func(in *ScenarioInput) { in.ItemIDs = &[]string{strings.Repeat("x", 65)} }, "itemIds[0] must be at most 64"},
		{"duplicate itemIds", "a", func(in *ScenarioInput) { in.ItemIDs = &[]string{"x", "y", "x"} }, `itemIds must not contain duplicates ("x")`},

		// events
		{"missing events", "a", func(in *ScenarioInput) { in.Events = nil }, "events is required"},
		{"too many events", "a", func(in *ScenarioInput) {
			evs := make([]EventInput, MaxEvents+1)
			for i := range evs {
				evs[i] = EventInput{ID: ptr(fmt.Sprintf("e%d", i)), Kind: ptr("oneTimeCost"), Name: ptr("x"), Year: ptr(1), Amount: ptr(1.0)}
			}
			in.Events = &evs
		}, "events must have at most 100 entries"},
		{"event missing id", "a", func(in *ScenarioInput) { event(in, 1).ID = nil }, "events[1].id is required"},
		{"event bad id", "a", func(in *ScenarioInput) { event(in, 0).ID = ptr("a b") }, "events[0].id must contain only"},
		{"event duplicate id", "a", func(in *ScenarioInput) { event(in, 3).ID = ptr("ev1") }, `events must not contain duplicate ids ("ev1")`},
		{"event missing kind", "a", func(in *ScenarioInput) { event(in, 0).Kind = nil }, "events[0].kind is required"},
		{"event bad kind", "a", func(in *ScenarioInput) { event(in, 0).Kind = ptr("windfall") }, "events[0].kind must be one of: oneTimeCost, oneTimeIncome, incomePause, recurringCost"},
		{"event missing name", "a", func(in *ScenarioInput) { event(in, 0).Name = nil }, "events[0].name is required"},
		{"event blank name", "a", func(in *ScenarioInput) { event(in, 0).Name = ptr("  ") }, "events[0].name must not be blank"},
		{"event long name", "a", func(in *ScenarioInput) { event(in, 0).Name = ptr(strings.Repeat("x", 201)) }, "events[0].name must be at most 200"},
		{"event missing year", "a", func(in *ScenarioInput) { event(in, 0).Year = nil }, "events[0].year is required"},
		{"event year zero", "a", func(in *ScenarioInput) { event(in, 0).Year = ptr(0) }, "events[0].year must be between 1 and 60"},
		{"event year 61", "a", func(in *ScenarioInput) { event(in, 0).Year = ptr(61) }, "events[0].year must be between 1 and 60"},
		{"event missing amount", "a", func(in *ScenarioInput) { event(in, 0).Amount = nil }, "events[0].amount is required"},
		{"event negative amount", "a", func(in *ScenarioInput) { event(in, 0).Amount = ptr(-0.01) }, "events[0].amount must be a number >= 0"},
		{"event huge amount", "a", func(in *ScenarioInput) { event(in, 0).Amount = ptr(1e12) }, "events[0].amount must be at most 999999999999.99"},
		{"incomePause nonzero amount", "a", func(in *ScenarioInput) { event(in, 1).Amount = ptr(100.0) }, "events[1].amount must be 0 for incomePause"},
		{"incomePause missing months", "a", func(in *ScenarioInput) { event(in, 1).Months = nil }, "events[1].months is required for incomePause"},
		{"incomePause months zero", "a", func(in *ScenarioInput) { event(in, 1).Months = ptr(0) }, "events[1].months must be between 1 and 60"},
		{"incomePause months 61", "a", func(in *ScenarioInput) { event(in, 1).Months = ptr(61) }, "events[1].months must be between 1 and 60"},
		{"months on oneTimeCost", "a", func(in *ScenarioInput) { event(in, 0).Months = ptr(3) }, "events[0].months must be omitted or null for oneTimeCost"},
		{"months on recurringCost", "a", func(in *ScenarioInput) { event(in, 2).Months = ptr(3) }, "events[2].months must be omitted or null for recurringCost"},
		{"recurringCost missing years", "a", func(in *ScenarioInput) { event(in, 2).Years = nil }, "events[2].years is required for recurringCost"},
		{"recurringCost years zero", "a", func(in *ScenarioInput) { event(in, 2).Years = ptr(0) }, "events[2].years must be between 1 and 60"},
		{"recurringCost years 61", "a", func(in *ScenarioInput) { event(in, 2).Years = ptr(61) }, "events[2].years must be between 1 and 60"},
		{"years on oneTimeIncome", "a", func(in *ScenarioInput) { event(in, 3).Years = ptr(2) }, "events[3].years must be omitted or null for oneTimeIncome"},
		{"years on incomePause", "a", func(in *ScenarioInput) { event(in, 1).Years = ptr(2) }, "events[1].years must be omitted or null for incomePause"},

		// parameters
		{"missing parameters", "a", func(in *ScenarioInput) { in.Parameters = nil }, "parameters is required"},
		{"missing years", "a", func(in *ScenarioInput) { in.Parameters.Years = nil }, "parameters.years is required"},
		{"years zero", "a", func(in *ScenarioInput) { in.Parameters.Years = ptr(0) }, "parameters.years must be between 1 and 60"},
		{"years 61", "a", func(in *ScenarioInput) { in.Parameters.Years = ptr(61) }, "parameters.years must be between 1 and 60"},
		{"missing returnRate", "a", func(in *ScenarioInput) { in.Parameters.ReturnRate = nil }, "parameters.returnRate is required"},
		{"returnRate low", "a", func(in *ScenarioInput) { in.Parameters.ReturnRate = ptr(-0.51) }, "parameters.returnRate must be between -0.5 and 0.5"},
		{"returnRate high", "a", func(in *ScenarioInput) { in.Parameters.ReturnRate = ptr(0.51) }, "parameters.returnRate must be between -0.5 and 0.5"},
		{"missing inflation", "a", func(in *ScenarioInput) { in.Parameters.Inflation = nil }, "parameters.inflation is required"},
		{"inflation low", "a", func(in *ScenarioInput) { in.Parameters.Inflation = ptr(-0.11) }, "parameters.inflation must be between -0.1 and 0.2"},
		{"inflation high", "a", func(in *ScenarioInput) { in.Parameters.Inflation = ptr(0.21) }, "parameters.inflation must be between -0.1 and 0.2"},
		{"missing incomeGrowth", "a", func(in *ScenarioInput) { in.Parameters.IncomeGrowth = nil }, "parameters.incomeGrowth is required"},
		{"incomeGrowth low", "a", func(in *ScenarioInput) { in.Parameters.IncomeGrowth = ptr(-0.21) }, "parameters.incomeGrowth must be between -0.2 and 0.3"},
		{"incomeGrowth high", "a", func(in *ScenarioInput) { in.Parameters.IncomeGrowth = ptr(0.31) }, "parameters.incomeGrowth must be between -0.2 and 0.3"},
		{"missing incomeAdjustment", "a", func(in *ScenarioInput) { in.Parameters.IncomeAdjustment = nil }, "parameters.incomeAdjustment is required"},
		{"incomeAdjustment low", "a", func(in *ScenarioInput) { in.Parameters.IncomeAdjustment = ptr(-1.01) }, "parameters.incomeAdjustment must be between -1 and 2"},
		{"incomeAdjustment high", "a", func(in *ScenarioInput) { in.Parameters.IncomeAdjustment = ptr(2.01) }, "parameters.incomeAdjustment must be between -1 and 2"},
		{"missing expenseAdjustment", "a", func(in *ScenarioInput) { in.Parameters.ExpenseAdjustment = nil }, "parameters.expenseAdjustment is required"},
		{"expenseAdjustment low", "a", func(in *ScenarioInput) { in.Parameters.ExpenseAdjustment = ptr(-0.91) }, "parameters.expenseAdjustment must be between -0.9 and 2"},
		{"expenseAdjustment high", "a", func(in *ScenarioInput) { in.Parameters.ExpenseAdjustment = ptr(2.01) }, "parameters.expenseAdjustment must be between -0.9 and 2"},
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

// Unknown keys inside events and parameters are rejected by the decoder
// (httpx.DecodeJSON uses DisallowUnknownFields, which applies to nested
// structs); this pins that the input types have no catch-all fields.
func TestUnknownNestedFieldsRejected(t *testing.T) {
	for _, body := range []string{
		`{"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0,"extra":1}}`,
		`{"events":[{"id":"e","kind":"oneTimeCost","name":"x","year":1,"amount":1,"note":"hi"}]}`,
	} {
		dec := json.NewDecoder(strings.NewReader(body))
		dec.DisallowUnknownFields()
		var in ScenarioInput
		if err := dec.Decode(&in); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("%s: err = %v, want unknown field", body, err)
		}
	}
}

func TestScenarioMarshalJSON(t *testing.T) {
	created := time.Date(2026, 9, 27, 6, 20, 39, 33_000_000, time.UTC)
	s := Scenario{
		ID: "x", Name: "Plan",
		Events: []Event{
			{ID: "e1", Kind: KindOneTimeCost, Name: "Car", Year: 2, Amount: 1234.5},
			{ID: "e2", Kind: KindIncomePause, Name: "Break", Year: 3, Months: ptr(6)},
			{ID: "e3", Kind: KindRecurringCost, Name: "Care", Year: 4, Amount: 100, Years: ptr(5)},
		},
		Parameters: Parameters{Years: 20, ReturnRate: 0.07, Inflation: 0.03, IncomeGrowth: 0.03, IncomeAdjustment: 0.1, ExpenseAdjustment: -0.2},
		CreatedAt:  created, UpdatedAt: created,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"x","name":"Plan","itemIds":[],"events":[` +
		`{"id":"e1","kind":"oneTimeCost","name":"Car","year":2,"amount":1234.5},` +
		`{"id":"e2","kind":"incomePause","name":"Break","year":3,"amount":0,"months":6},` +
		`{"id":"e3","kind":"recurringCost","name":"Care","year":4,"amount":100,"years":5}],` +
		`"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0.1,"expenseAdjustment":-0.2},` +
		`"createdAt":"2026-09-27T06:20:39.033Z","updatedAt":"2026-09-27T06:20:39.033Z"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}

	s.Events = nil
	b, _ = json.Marshal(s)
	if !strings.Contains(string(b), `"events":[]`) {
		t.Errorf("nil events should render as []: %s", b)
	}
}

func TestValidateRetirement(t *testing.T) {
	in := validInput()
	in.Parameters.RetirementYear, in.Parameters.RetirementMonth = ptr(2045), ptr(6)
	sc, err := in.Validate("s1")
	if err != nil {
		t.Fatal(err)
	}
	if *sc.Parameters.RetirementYear != 2045 || *sc.Parameters.RetirementMonth != 6 {
		t.Errorf("parameters = %+v", sc.Parameters)
	}
	b, _ := json.Marshal(sc.Parameters)
	if !strings.Contains(string(b), `"retirementYear":2045,"retirementMonth":6`) {
		t.Errorf("JSON = %s", b)
	}
	// Optional: without it, the JSON leaves it out.
	sc, err = validInput().Validate("s1")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(sc.Parameters); strings.Contains(string(b), "retirement") {
		t.Errorf("JSON = %s", b)
	}

	for _, c := range []struct {
		name        string
		year, month *int
		want        string
	}{
		{"month without year", nil, ptr(6), "retirementMonth requires parameters.retirementYear"},
		{"year too far", ptr(2500), nil, "retirementYear must be from 1900 to 2200"},
		{"bad month", ptr(2045), ptr(13), "retirementMonth must be from 1 to 12"},
	} {
		in := validInput()
		in.Parameters.RetirementYear, in.Parameters.RetirementMonth = c.year, c.month
		if _, err := in.Validate("s1"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
