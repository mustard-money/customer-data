package account

import (
	"errors"
	"strings"
	"testing"

	"github.com/mustard-money/customer-data/fields"
)

func ptr[T any](v T) *T { return &v }

func TestValidateZipCodes(t *testing.T) {
	for _, zip := range []string{"94103", "94103-1234", " 10001 ", ""} {
		p, err := ProfileInput{PostalCode: ptr(zip)}.Validate()
		if err != nil {
			t.Errorf("%q rejected: %v", zip, err)
		}
		if p.PostalCode != strings.TrimSpace(zip) {
			t.Errorf("%q: PostalCode = %q, want trimmed", zip, p.PostalCode)
		}
	}
	for _, zip := range []string{"9410", "941031", "94103-12", "100-0001", "28001X"} {
		_, err := ProfileInput{PostalCode: ptr(zip)}.Validate()
		var ve *fields.ValidationError
		if !errors.As(err, &ve) || !strings.Contains(ve.Msg, "not a valid US ZIP code") {
			t.Errorf("%q: err = %v, want invalid ZIP code", zip, err)
		}
	}
}

func TestValidateCountry(t *testing.T) {
	// Omitted or "US" are both fine and stored as "US".
	for _, in := range []ProfileInput{{}, {Country: ptr("US")}} {
		p, err := in.Validate()
		if err != nil || p.Country != CountryUS {
			t.Errorf("in = %+v: p = %+v, err = %v", in, p, err)
		}
	}
	for _, country := range []string{"HK", "JP", "us", ""} {
		_, err := ProfileInput{Country: ptr(country)}.Validate()
		if err == nil || !strings.Contains(err.Error(), "country must be US") {
			t.Errorf("country %q: err = %v, want rejection", country, err)
		}
	}
}

func TestValidateDefaultsAndErrors(t *testing.T) {
	p, err := ProfileInput{}.Validate()
	if err != nil || p.FilingStatus != FilingSingle || p.PostalCode != "" {
		t.Fatalf("defaults: p = %+v, err = %v", p, err)
	}
	_, err = ProfileInput{FilingStatus: ptr("joint")}.Validate()
	if err == nil || !strings.Contains(err.Error(), "filingStatus must be one of") {
		t.Errorf("bad filing status: err = %v", err)
	}
}

func TestValidateExpectedReturn(t *testing.T) {
	for _, r := range []float64{-0.5, 0, 0.07, 0.5} {
		p, err := ProfileInput{ExpectedReturn: &r}.Validate()
		if err != nil || p.ExpectedReturn == nil || *p.ExpectedReturn != r {
			t.Errorf("%v: %+v, %v", r, p, err)
		}
	}
	for _, r := range []float64{-0.51, 0.6} {
		if _, err := (ProfileInput{ExpectedReturn: &r}).Validate(); err == nil || !strings.Contains(err.Error(), "expectedReturn must be between") {
			t.Errorf("%v: err = %v", r, err)
		}
	}
	if p, err := (ProfileInput{}).Validate(); err != nil || p.ExpectedReturn != nil {
		t.Errorf("unset: %+v, %v", p, err)
	}
}

func TestValidateInflation(t *testing.T) {
	for _, r := range []float64{-0.1, 0.025, 0.2} {
		p, err := ProfileInput{Inflation: &r}.Validate()
		if err != nil || p.Inflation == nil || *p.Inflation != r {
			t.Errorf("%v: %+v, %v", r, p, err)
		}
	}
	for _, r := range []float64{-0.11, 0.25} {
		if _, err := (ProfileInput{Inflation: &r}).Validate(); err == nil || !strings.Contains(err.Error(), "inflation must be between") {
			t.Errorf("%v: err = %v", r, err)
		}
	}
}

func TestValidateEmergencyFund(t *testing.T) {
	v := 25_000.004
	p, err := ProfileInput{EmergencyFund: &v}.Validate()
	if err != nil || p.EmergencyFund == nil || *p.EmergencyFund != 25_000 {
		t.Fatalf("got %+v %v", p.EmergencyFund, err)
	}
	if p, _ := (ProfileInput{}).Validate(); p.EmergencyFund != nil {
		t.Error("unset stays unset")
	}
	for _, bad := range []float64{-1, 2e12} {
		if _, err := (ProfileInput{EmergencyFund: &bad}).Validate(); err == nil || !strings.Contains(err.Error(), "emergencyFund must be") {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

func TestValidateCashReturn(t *testing.T) {
	v := 0.04
	p, err := ProfileInput{CashReturn: &v}.Validate()
	if err != nil || p.CashReturn == nil || *p.CashReturn != 0.04 {
		t.Fatalf("got %+v %v", p.CashReturn, err)
	}
	if p, _ := (ProfileInput{}).Validate(); p.CashReturn != nil {
		t.Errorf("unset should stay nil, got %v", *p.CashReturn)
	}
	for _, bad := range []float64{-0.01, 0.21} {
		if _, err := (ProfileInput{CashReturn: &bad}).Validate(); err == nil {
			t.Errorf("cashReturn %v should be rejected", bad)
		}
	}
}
