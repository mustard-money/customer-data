// Package account implements the account profile: a single record per
// customer holding where they live in the US (ZIP code) and their filing
// status, which the app uses to choose federal and state tax tables.
package account

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// Filing status values. Only US tax tables distinguish them.
const (
	FilingSingle       = "single"
	FilingMarriedJoint = "marriedJoint"
)

// The service is US-only: the country is always "US" and the postal code is
// a ZIP code. Keep in sync with the app's src/data/tax/us.ts isValidZip.
const CountryUS = "US"

var zipPattern = regexp.MustCompile(`^\d{5}(-\d{4})?$`)

// Profile is the customer's account profile.
type Profile struct {
	Country      string
	PostalCode   string // may be empty until the customer enters one
	FilingStatus string
	// ExpectedReturn is the default yearly investment return (0.07 is 7%);
	// nil until the customer sets one.
	ExpectedReturn *float64
	// Inflation is the expected yearly inflation (0.03 is 3%); nil until set.
	Inflation *float64
	// CashReturn is the yearly interest bank accounts and CDs earn when they
	// have no rate of their own (0.04 is 4%); nil until set.
	CashReturn *float64
	// EmergencyFund is the cash (dollars) to keep on hand; cash beyond it is
	// invested in a taxable account. Nil until set.
	EmergencyFund *float64
	UpdatedAt     time.Time
}

type profileJSON struct {
	Country        string   `json:"country"`
	PostalCode     string   `json:"postalCode"`
	FilingStatus   string   `json:"filingStatus"`
	ExpectedReturn *float64 `json:"expectedReturn,omitempty"`
	Inflation      *float64 `json:"inflation,omitempty"`
	CashReturn     *float64 `json:"cashReturn,omitempty"`
	EmergencyFund  *float64 `json:"emergencyFund,omitempty"`
	UpdatedAt      string   `json:"updatedAt"`
}

// MarshalJSON renders the profile in the API's camelCase shape.
func (p Profile) MarshalJSON() ([]byte, error) {
	return json.Marshal(profileJSON{
		Country:        p.Country,
		PostalCode:     p.PostalCode,
		FilingStatus:   p.FilingStatus,
		ExpectedReturn: p.ExpectedReturn,
		Inflation:      p.Inflation,
		CashReturn:     p.CashReturn,
		EmergencyFund:  p.EmergencyFund,
		UpdatedAt:      fields.FormatTime(p.UpdatedAt),
	})
}

// ProfileInput is the PUT request body.
type ProfileInput struct {
	Country      *string `json:"country"`
	PostalCode   *string `json:"postalCode"`
	FilingStatus *string `json:"filingStatus"`
	// ExpectedReturn is optional; omitted or null clears it.
	ExpectedReturn *float64 `json:"expectedReturn"`
	// Inflation is optional; omitted or null clears it.
	Inflation *float64 `json:"inflation"`
	// CashReturn is optional; omitted or null clears it.
	CashReturn *float64 `json:"cashReturn"`
	// EmergencyFund is optional (dollars, rounded to cents); omitted or null clears it.
	EmergencyFund *float64 `json:"emergencyFund"`
	// UpdatedAt is accepted so clients can send back the full profile, but it
	// is ignored: the server sets updatedAt on every write.
	UpdatedAt *string `json:"updatedAt"`
}

// Validate checks a PUT body and returns the normalized profile to store.
// Errors are *fields.ValidationError.
func (in ProfileInput) Validate() (Profile, error) {
	// country is optional (the service is US-only) but must be "US" if sent.
	if in.Country != nil && *in.Country != CountryUS {
		return Profile{}, fields.Invalid("country must be %s (the only supported country)", CountryUS)
	}

	postalCode := ""
	if in.PostalCode != nil {
		postalCode = strings.TrimSpace(*in.PostalCode)
	}
	if postalCode != "" && !zipPattern.MatchString(postalCode) {
		return Profile{}, fields.Invalid("postalCode %q is not a valid US ZIP code (12345 or 12345-6789)", postalCode)
	}

	filingStatus := FilingSingle
	if in.FilingStatus != nil {
		filingStatus = *in.FilingStatus
	}
	if filingStatus != FilingSingle && filingStatus != FilingMarriedJoint {
		return Profile{}, fields.Invalid("filingStatus must be one of: single, marriedJoint")
	}

	var expectedReturn *float64
	if in.ExpectedReturn != nil {
		r := *in.ExpectedReturn
		if math.IsNaN(r) || r < -0.5 || r > 0.5 {
			return Profile{}, fields.Invalid("expectedReturn must be between -0.5 and 0.5")
		}
		expectedReturn = &r
	}

	var inflation *float64
	if in.Inflation != nil {
		r := *in.Inflation
		if math.IsNaN(r) || r < -0.1 || r > 0.2 {
			return Profile{}, fields.Invalid("inflation must be between -0.1 and 0.2")
		}
		inflation = &r
	}

	var cashReturn *float64
	if in.CashReturn != nil {
		r := *in.CashReturn
		if math.IsNaN(r) || r < 0 || r > 0.2 {
			return Profile{}, fields.Invalid("cashReturn must be between 0 and 0.2")
		}
		cashReturn = &r
	}

	var emergencyFund *float64
	if in.EmergencyFund != nil {
		v := *in.EmergencyFund
		if math.IsNaN(v) || v < 0 || v > 1e12 {
			return Profile{}, fields.Invalid("emergencyFund must be from 0 to 1000000000000")
		}
		v = math.Round(v*100) / 100
		emergencyFund = &v
	}

	return Profile{
		Country:        CountryUS,
		PostalCode:     postalCode,
		FilingStatus:   filingStatus,
		ExpectedReturn: expectedReturn,
		Inflation:      inflation,
		CashReturn:     cashReturn,
		EmergencyFund:  emergencyFund,
	}, nil
}
