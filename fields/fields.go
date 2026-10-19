// Package fields holds validation rules and formats shared by API resources
// (ids, names, timestamps) and the ValidationError type handlers map to 400.
package fields

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxIDLength   = 64
	MaxNameLength = 200
)

// TimeFormat is how timestamps are rendered in JSON: RFC3339, UTC, always
// with millisecond precision (matches JavaScript's Date.toISOString()).
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidationError is a client error whose message is safe to return as-is.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

// Invalid returns a *ValidationError with a formatted message.
func Invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// ValidateID checks that id is 1-64 chars of [A-Za-z0-9_-].
func ValidateID(id string) error {
	if id == "" {
		return Invalid("id is required")
	}
	if len(id) > MaxIDLength {
		return Invalid("id must be at most %d characters", MaxIDLength)
	}
	if !idPattern.MatchString(id) {
		return Invalid("id must contain only letters, digits, '_' or '-'")
	}
	return nil
}

// ValidateBodyID checks a PUT's path id and, if present, that the body id
// matches it.
func ValidateBodyID(bodyID *string, pathID string) error {
	if err := ValidateID(pathID); err != nil {
		return err
	}
	if bodyID != nil && *bodyID != pathID {
		return Invalid("id in body (%q) does not match id in path (%q)", *bodyID, pathID)
	}
	return nil
}

// Name validates a required display name: trimmed, 1-200 characters.
func Name(s *string) (string, error) {
	if s == nil {
		return "", Invalid("name is required")
	}
	name := strings.TrimSpace(*s)
	if name == "" {
		return "", Invalid("name must not be blank")
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", Invalid("name must be at most %d characters", MaxNameLength)
	}
	return name, nil
}

// CreatedAt parses the required client-supplied createdAt timestamp,
// normalized to UTC with millisecond precision.
func CreatedAt(s *string) (time.Time, error) {
	if s == nil || *s == "" {
		return time.Time{}, Invalid("createdAt is required")
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return time.Time{}, Invalid("createdAt must be an RFC3339 timestamp (e.g. 2026-09-27T06:20:39.033Z)")
	}
	return t.UTC().Truncate(time.Millisecond), nil
}

// FormatTime renders t in TimeFormat.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeFormat)
}
