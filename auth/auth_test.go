package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mustard-money/customer-data/auth/authtest"
	"github.com/mustard-money/customer-data/fields"
)

func ptr[T any](v T) *T { return &v }

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q", h)
	}
	if ok, err := verifyPassword("correct horse", h); err != nil || !ok {
		t.Errorf("verify correct = %v, %v", ok, err)
	}
	if ok, err := verifyPassword("wrong horse", h); err != nil || ok {
		t.Errorf("verify wrong = %v, %v", ok, err)
	}
	if h2, _ := hashPassword("correct horse"); h2 == h {
		t.Error("hashes of the same password must differ (salt)")
	}
	if _, err := verifyPassword("x", "$bcrypt$whatever"); err == nil {
		t.Error("expected error for unsupported format")
	}
}

func TestValidateSignup(t *testing.T) {
	email, pw, err := PasswordInput{Email: ptr("  Alex@Example.COM "), Password: ptr("hunter22")}.validateSignup()
	if err != nil || email != "alex@example.com" || pw != "hunter22" {
		t.Errorf("got %q, %q, %v", email, pw, err)
	}

	for _, in := range []PasswordInput{
		{Password: ptr("hunter22")},
		{Email: ptr("not-an-email"), Password: ptr("hunter22")},
		{Email: ptr("Alex <alex@example.com>"), Password: ptr("hunter22")},
		{Email: ptr(strings.Repeat("a", 250) + "@x.io"), Password: ptr("hunter22")},
		{Email: ptr("alex@example.com")},
		{Email: ptr("alex@example.com"), Password: ptr("short")},
		{Email: ptr("alex@example.com"), Password: ptr(strings.Repeat("p", 257))},
	} {
		var ve *fields.ValidationError
		if _, _, err := in.validateSignup(); !errors.As(err, &ve) {
			t.Errorf("validateSignup(%+v) = %v, want ValidationError", in, err)
		}
	}

	// Login does not enforce the minimum length.
	if _, _, err := (PasswordInput{Email: ptr("a@b.co"), Password: ptr("short")}).validateLogin(); err != nil {
		t.Errorf("validateLogin short password = %v", err)
	}
}

func TestBearerToken(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer abc":   "abc",
		"bearer  abc ": "abc",
		"Basic abc":    "",
		"Bearer ":      "",
		"":             "",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		got, ok := bearerToken(r)
		if got != want || ok != (want != "") {
			t.Errorf("bearerToken(%q) = %q, %v", header, got, ok)
		}
	}
}

func TestProviderVerify(t *testing.T) {
	signer := authtest.NewSigner(t)
	other := authtest.NewSigner(t)
	p := NewProvider(ProviderGoogle,
		[]string{"https://accounts.google.com", "accounts.google.com"},
		[]string{"web-client", "ios-client"},
		signer.KeySet())
	ctx := context.Background()

	claims := func(overrides map[string]any) map[string]any {
		c := map[string]any{
			"iss":            "https://accounts.google.com",
			"aud":            "ios-client",
			"sub":            "10769150350006150715113082367",
			"email":          "Alex@Example.com",
			"email_verified": true,
			"iat":            time.Now().Unix(),
			"exp":            time.Now().Add(time.Hour).Unix(),
		}
		for k, v := range overrides {
			if v == nil {
				delete(c, k)
			} else {
				c[k] = v
			}
		}
		return c
	}

	id, err := p.Verify(ctx, signer.Sign(t, claims(nil)), "")
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{Provider: "google", Subject: "10769150350006150715113082367", Email: "alex@example.com", EmailVerified: true}
	if id != want {
		t.Errorf("identity = %+v, want %+v", id, want)
	}

	// Apple-style string email_verified, schemeless Google issuer, no email.
	id, err = p.Verify(ctx, signer.Sign(t, claims(map[string]any{"iss": "accounts.google.com", "email_verified": "true"})), "")
	if err != nil || !id.EmailVerified {
		t.Errorf("string email_verified: %+v, %v", id, err)
	}
	id, err = p.Verify(ctx, signer.Sign(t, claims(map[string]any{"email": nil, "email_verified": nil})), "")
	if err != nil || id.Email != "" || id.EmailVerified {
		t.Errorf("no email: %+v, %v", id, err)
	}
	id, _ = p.Verify(ctx, signer.Sign(t, claims(map[string]any{"email_verified": false})), "")
	if id.EmailVerified {
		t.Error("email_verified false must not verify")
	}

	// Nonce: raw or SHA-256 hex in the token both match the raw nonce.
	sum := sha256.Sum256([]byte("n0nce"))
	for _, claim := range []string{"n0nce", hex.EncodeToString(sum[:])} {
		if _, err := p.Verify(ctx, signer.Sign(t, claims(map[string]any{"nonce": claim})), "n0nce"); err != nil {
			t.Errorf("nonce claim %q: %v", claim, err)
		}
	}

	for name, tok := range map[string]string{
		"wrong key":      other.Sign(t, claims(nil)),
		"wrong issuer":   signer.Sign(t, claims(map[string]any{"iss": "https://evil.example"})),
		"wrong audience": signer.Sign(t, claims(map[string]any{"aud": "someone-else"})),
		"expired":        signer.Sign(t, claims(map[string]any{"exp": time.Now().Add(-time.Minute).Unix()})),
		"no subject":     signer.Sign(t, claims(map[string]any{"sub": nil})),
		"garbage":        "not.a.jwt",
	} {
		if _, err := p.Verify(ctx, tok, ""); !errors.Is(err, errInvalidIDToken) {
			t.Errorf("%s: err = %v, want errInvalidIDToken", name, err)
		}
	}
	if _, err := p.Verify(ctx, signer.Sign(t, claims(map[string]any{"nonce": "other"})), "n0nce"); !errors.Is(err, errInvalidIDToken) {
		t.Errorf("nonce mismatch: err = %v", err)
	}
}
