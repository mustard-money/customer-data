// Package profiles implements the household-member profile resource: model,
// validation, Postgres store and HTTP handlers.
package profiles

import (
	"encoding/json"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// Relationship values.
const (
	RelationshipSelf    = "self"
	RelationshipPartner = "partner"
	RelationshipChild   = "child"
	RelationshipOther   = "other"
)

// MinBirthYear is the earliest accepted birthYear.
const MinBirthYear = 1900

// Profile is a household member.
type Profile struct {
	ID           string
	Name         string
	Relationship string
	BirthYear    *int // nil when unset
	BirthMonth   *int // 1–12; nil when unset; only set when BirthYear is
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type profileJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	BirthYear    *int   `json:"birthYear,omitempty"`
	BirthMonth   *int   `json:"birthMonth,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// MarshalJSON renders the profile in the API's camelCase shape.
func (p Profile) MarshalJSON() ([]byte, error) {
	return json.Marshal(profileJSON{
		ID:           p.ID,
		Name:         p.Name,
		Relationship: p.Relationship,
		BirthYear:    p.BirthYear,
		BirthMonth:   p.BirthMonth,
		CreatedAt:    fields.FormatTime(p.CreatedAt),
		UpdatedAt:    fields.FormatTime(p.UpdatedAt),
	})
}

// ProfileInput is the PUT request body. Pointer fields distinguish
// "absent/null" from zero values.
type ProfileInput struct {
	ID           *string `json:"id"`
	Name         *string `json:"name"`
	Relationship *string `json:"relationship"`
	BirthYear    *int    `json:"birthYear"`
	BirthMonth   *int    `json:"birthMonth"`
	CreatedAt    *string `json:"createdAt"`
	// UpdatedAt is accepted so clients can send back a full profile, but it
	// is ignored: the server sets updatedAt on every write.
	UpdatedAt *string `json:"updatedAt"`
}

// ValidRelationship reports whether r is a known relationship.
func ValidRelationship(r string) bool {
	switch r {
	case RelationshipSelf, RelationshipPartner, RelationshipChild, RelationshipOther:
		return true
	}
	return false
}

// latestDate is the latest calendar date anywhere on Earth at now (UTC+14),
// so a client whose local date is already the next month or year is not rejected.
func latestDate(now time.Time) time.Time {
	return now.UTC().Add(14 * time.Hour)
}

// Validate checks a PUT body against the path id and returns the normalized
// profile to store. now bounds birthYear. Errors are *fields.ValidationError.
func (in ProfileInput) Validate(pathID string, now time.Time) (Profile, error) {
	if err := fields.ValidateBodyID(in.ID, pathID); err != nil {
		return Profile{}, err
	}

	name, err := fields.Name(in.Name)
	if err != nil {
		return Profile{}, err
	}

	if in.Relationship == nil || *in.Relationship == "" {
		return Profile{}, fields.Invalid("relationship is required")
	}
	if !ValidRelationship(*in.Relationship) {
		return Profile{}, fields.Invalid("relationship must be one of: self, partner, child, other")
	}

	latest := latestDate(now)
	var birthYear *int
	if in.BirthYear != nil {
		y, maxYear := *in.BirthYear, latest.Year()
		if y < MinBirthYear || y > maxYear {
			return Profile{}, fields.Invalid("birthYear must be between %d and %d", MinBirthYear, maxYear)
		}
		birthYear = &y
	}

	var birthMonth *int
	if in.BirthMonth != nil {
		m := *in.BirthMonth
		if birthYear == nil {
			return Profile{}, fields.Invalid("birthMonth requires birthYear")
		}
		if m < 1 || m > 12 {
			return Profile{}, fields.Invalid("birthMonth must be between 1 and 12")
		}
		if *birthYear == latest.Year() && m > int(latest.Month()) {
			return Profile{}, fields.Invalid("birth date must not be in the future")
		}
		birthMonth = &m
	}

	createdAt, err := fields.CreatedAt(in.CreatedAt)
	if err != nil {
		return Profile{}, err
	}

	return Profile{
		ID:           pathID,
		Name:         name,
		Relationship: *in.Relationship,
		BirthYear:    birthYear,
		BirthMonth:   birthMonth,
		CreatedAt:    createdAt,
	}, nil
}
