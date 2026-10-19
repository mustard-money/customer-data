package profiles

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

func ptr[T any](v T) *T { return &v }

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func validInput() ProfileInput {
	return ProfileInput{
		Name:         ptr(" Alex "),
		Relationship: ptr("self"),
		BirthYear:    ptr(1988),
		CreatedAt:    ptr("2026-09-27T06:20:39.033Z"),
	}
}

func TestValidateOK(t *testing.T) {
	in := validInput()
	in.ID = ptr("lz3k9abc12")
	in.UpdatedAt = ptr("ignored")
	p, err := in.Validate("lz3k9abc12", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "lz3k9abc12" || p.Name != "Alex" || p.Relationship != "self" || *p.BirthYear != 1988 {
		t.Errorf("unexpected profile: %+v", p)
	}
	if fields.FormatTime(p.CreatedAt) != "2026-09-27T06:20:39.033Z" {
		t.Errorf("CreatedAt = %v", p.CreatedAt)
	}
}

func TestValidateOptionalBirthYear(t *testing.T) {
	in := validInput()
	in.BirthYear = nil
	p, err := in.Validate("a", now)
	if err != nil || p.BirthYear != nil {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
	for _, y := range []int{1900, 2026} {
		in.BirthYear = ptr(y)
		if _, err := in.Validate("a", now); err != nil {
			t.Errorf("birthYear %d rejected: %v", y, err)
		}
	}
}

func TestValidateBirthMonth(t *testing.T) {
	in := validInput()
	p, err := in.Validate("a", now)
	if err != nil || p.BirthMonth != nil {
		t.Fatalf("absent birthMonth: p = %+v, err = %v", p, err)
	}
	for _, tc := range []struct{ year, month int }{{1988, 1}, {1988, 12}, {2026, 9}} {
		in.BirthYear, in.BirthMonth = ptr(tc.year), ptr(tc.month)
		p, err := in.Validate("a", now)
		if err != nil || *p.BirthMonth != tc.month {
			t.Errorf("%d-%02d: p = %+v, err = %v", tc.year, tc.month, p, err)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		pathID  string
		mutate  func(*ProfileInput)
		wantMsg string
	}{
		{"id mismatch", "a", func(in *ProfileInput) { in.ID = ptr("b") }, "does not match"},
		{"bad path id", "a b", func(*ProfileInput) {}, "id must contain only"},
		{"missing name", "a", func(in *ProfileInput) { in.Name = nil }, "name is required"},
		{"blank name", "a", func(in *ProfileInput) { in.Name = ptr(" ") }, "must not be blank"},
		{"long name", "a", func(in *ProfileInput) { in.Name = ptr(strings.Repeat("x", 201)) }, "at most 200"},
		{"missing relationship", "a", func(in *ProfileInput) { in.Relationship = nil }, "relationship is required"},
		{"bad relationship", "a", func(in *ProfileInput) { in.Relationship = ptr("pet") }, "relationship must be one of"},
		{"birthYear too old", "a", func(in *ProfileInput) { in.BirthYear = ptr(1899) }, "birthYear must be between 1900 and 2026"},
		{"birthYear in future", "a", func(in *ProfileInput) { in.BirthYear = ptr(2027) }, "birthYear must be between"},
		{"birthMonth without year", "a", func(in *ProfileInput) { in.BirthYear, in.BirthMonth = nil, ptr(3) }, "birthMonth requires birthYear"},
		{"birthMonth zero", "a", func(in *ProfileInput) { in.BirthMonth = ptr(0) }, "birthMonth must be between 1 and 12"},
		{"birthMonth 13", "a", func(in *ProfileInput) { in.BirthMonth = ptr(13) }, "birthMonth must be between 1 and 12"},
		{"birth month in future", "a", func(in *ProfileInput) { in.BirthYear, in.BirthMonth = ptr(2026), ptr(10) }, "must not be in the future"},
		{"missing createdAt", "a", func(in *ProfileInput) { in.CreatedAt = nil }, "createdAt is required"},
		{"bad createdAt", "a", func(in *ProfileInput) { in.CreatedAt = ptr("nope") }, "createdAt must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.mutate(&in)
			_, err := in.Validate(tt.pathID, now)
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

func TestLatestDateAllowsEarliestTimezone(t *testing.T) {
	// 11:00 UTC on Dec 31 is already Jan 1 in UTC+14.
	got := latestDate(time.Date(2026, 12, 31, 11, 0, 0, 0, time.UTC))
	if got.Year() != 2027 || got.Month() != time.January {
		t.Errorf("latestDate = %v, want January 2027", got)
	}
}

func TestProfileMarshalJSON(t *testing.T) {
	created := time.Date(2026, 9, 27, 6, 20, 39, 33_000_000, time.UTC)
	p := Profile{ID: "x", Name: "Sam", Relationship: "child", CreatedAt: created, UpdatedAt: created}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"x","name":"Sam","relationship":"child","createdAt":"2026-09-27T06:20:39.033Z","updatedAt":"2026-09-27T06:20:39.033Z"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	p.BirthYear = ptr(2019)
	b, _ = json.Marshal(p)
	if !strings.Contains(string(b), `"birthYear":2019`) {
		t.Errorf("birthYear missing: %s", b)
	}
}
