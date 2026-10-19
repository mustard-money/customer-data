package fields

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestValidateID(t *testing.T) {
	for _, id := range []string{"a", "lz3k9abc12", "A_b-9", strings.Repeat("z", 64)} {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v", id, err)
		}
	}
	for _, id := range []string{"", "a/b", "a.b", "has space", "ü", strings.Repeat("z", 65)} {
		var ve *ValidationError
		if err := ValidateID(id); !errors.As(err, &ve) {
			t.Errorf("ValidateID(%q) = %v, want ValidationError", id, err)
		}
	}
}

func TestValidateBodyID(t *testing.T) {
	if err := ValidateBodyID(nil, "abc"); err != nil {
		t.Error(err)
	}
	if err := ValidateBodyID(ptr("abc"), "abc"); err != nil {
		t.Error(err)
	}
	if err := ValidateBodyID(ptr("xyz"), "abc"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("mismatch: err = %v", err)
	}
	if err := ValidateBodyID(nil, "bad.id"); err == nil {
		t.Error("invalid path id accepted")
	}
}

func TestName(t *testing.T) {
	if n, err := Name(ptr("  Alex \n")); err != nil || n != "Alex" {
		t.Errorf("Name = %q, %v", n, err)
	}
	if n, err := Name(ptr(strings.Repeat("é", 200))); err != nil || n == "" {
		t.Errorf("200 runes rejected: %v", err)
	}
	for in, want := range map[*string]string{
		nil:                           "name is required",
		ptr("   "):                    "must not be blank",
		ptr(strings.Repeat("x", 201)): "at most 200",
	} {
		if _, err := Name(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func TestCreatedAt(t *testing.T) {
	got, err := CreatedAt(ptr("2026-09-27T08:20:39.0339+02:00"))
	if err != nil {
		t.Fatal(err)
	}
	if FormatTime(got) != "2026-09-27T06:20:39.033Z" || got.Location() != time.UTC {
		t.Errorf("CreatedAt = %s", FormatTime(got))
	}
	if FormatTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) != "2026-01-02T03:04:05.000Z" {
		t.Error("FormatTime must always include milliseconds")
	}
	for _, in := range []*string{nil, ptr(""), ptr("yesterday"), ptr("2026-09-27")} {
		if _, err := CreatedAt(in); err == nil {
			t.Errorf("CreatedAt(%v) accepted", in)
		}
	}
}
