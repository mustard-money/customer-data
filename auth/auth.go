// Package auth implements customer sign-up and sign-in (email + password, or
// an Apple/Google ID token), opaque bearer sessions, and middleware that
// resolves the signed-in customer from a request.
package auth

import (
	"encoding/json"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mustard-money/customer-data/fields"
)

// SessionTTL is how long a session token stays valid after sign-in.
const SessionTTL = 30 * 24 * time.Hour

const (
	maxEmailLength    = 254
	minPasswordLength = 8   // characters
	maxPasswordLength = 256 // bytes; bounds hashing cost
)

// Customer is a signed-up customer.
type Customer struct {
	ID        string
	Email     *string // nil when signed up via a provider that shared no verified email
	CreatedAt time.Time
}

type customerJSON struct {
	ID        string  `json:"id"`
	Email     *string `json:"email"`
	CreatedAt string  `json:"createdAt"`
}

// MarshalJSON renders the customer in the API's camelCase shape.
func (c Customer) MarshalJSON() ([]byte, error) {
	return json.Marshal(customerJSON{ID: c.ID, Email: c.Email, CreatedAt: fields.FormatTime(c.CreatedAt)})
}

// Session is returned by every sign-in endpoint. Token is only ever shown
// here; the server keeps just its hash.
type Session struct {
	Token     string
	ExpiresAt time.Time
	Customer  Customer
}

type sessionJSON struct {
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expiresAt"`
	Customer  Customer `json:"customer"`
}

// MarshalJSON renders the session in the API's camelCase shape.
func (s Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(sessionJSON{Token: s.Token, ExpiresAt: fields.FormatTime(s.ExpiresAt), Customer: s.Customer})
}

// PasswordInput is the body of signup and login.
type PasswordInput struct {
	Email    *string `json:"email"`
	Password *string `json:"password"`
}

// validateSignup normalizes the email and enforces password rules.
func (in PasswordInput) validateSignup() (email, password string, err error) {
	if email, err = normalizeEmail(in.Email); err != nil {
		return "", "", err
	}
	if in.Password == nil || *in.Password == "" {
		return "", "", fields.Invalid("password is required")
	}
	password = *in.Password
	if utf8.RuneCountInString(password) < minPasswordLength {
		return "", "", fields.Invalid("password must be at least %d characters", minPasswordLength)
	}
	if len(password) > maxPasswordLength {
		return "", "", fields.Invalid("password must be at most %d bytes", maxPasswordLength)
	}
	return email, password, nil
}

// validateLogin only checks presence: rules may have changed since signup,
// and a wrong-but-well-formed password must get the same 401 as any other.
func (in PasswordInput) validateLogin() (email, password string, err error) {
	if email, err = normalizeEmail(in.Email); err != nil {
		return "", "", err
	}
	if in.Password == nil || *in.Password == "" {
		return "", "", fields.Invalid("password is required")
	}
	if len(*in.Password) > maxPasswordLength {
		return "", "", fields.Invalid("password must be at most %d bytes", maxPasswordLength)
	}
	return email, *in.Password, nil
}

// IDTokenInput is the body of the Apple/Google sign-in endpoints.
type IDTokenInput struct {
	IDToken *string `json:"idToken"`
	// Nonce is the raw nonce the app generated for this sign-in, if any. The
	// token's nonce claim must equal it or its hex SHA-256 (Apple's native
	// flow hashes it).
	Nonce *string `json:"nonce"`
}

func (in IDTokenInput) validate() (idToken, nonce string, err error) {
	if in.IDToken == nil || strings.TrimSpace(*in.IDToken) == "" {
		return "", "", fields.Invalid("idToken is required")
	}
	if in.Nonce != nil {
		nonce = *in.Nonce
	}
	return strings.TrimSpace(*in.IDToken), nonce, nil
}

// normalizeEmail trims and lowercases a required email address and rejects
// anything that is not a bare addr-spec.
func normalizeEmail(s *string) (string, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return "", fields.Invalid("email is required")
	}
	email := strings.ToLower(strings.TrimSpace(*s))
	if len(email) > maxEmailLength {
		return "", fields.Invalid("email must be at most %d characters", maxEmailLength)
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" {
		return "", fields.Invalid("email is not a valid email address")
	}
	return email, nil
}
