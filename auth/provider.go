package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Provider names, used in routes (/api/v1/auth/{name}) and stored in
// customer_identities.provider.
const (
	ProviderApple  = "apple"
	ProviderGoogle = "google"
)

// errInvalidIDToken covers every reason an ID token is rejected. Details are
// logged, not returned to clients.
var errInvalidIDToken = errors.New("invalid id token")

// Provider verifies ID tokens issued by Apple or Google for this app.
type Provider struct {
	name      string
	issuers   []string
	clientIDs []string
	verifier  *oidc.IDTokenVerifier
}

// Identity is the verified result of an ID token.
type Identity struct {
	Provider      string
	Subject       string
	Email         string // lowercased; may be empty
	EmailVerified bool
}

// NewGoogle returns a Provider for Sign in with Google. clientIDs are the
// OAuth client IDs of the app (web, iOS, Android); a token must be issued to
// one of them.
func NewGoogle(ctx context.Context, clientIDs []string) *Provider {
	return NewProvider(ProviderGoogle,
		[]string{"https://accounts.google.com", "accounts.google.com"},
		clientIDs,
		oidc.NewRemoteKeySet(ctx, "https://www.googleapis.com/oauth2/v3/certs"))
}

// NewApple returns a Provider for Sign in with Apple. clientIDs are the app's
// bundle ID (native sign-in) and/or Services ID (web sign-in).
func NewApple(ctx context.Context, clientIDs []string) *Provider {
	return NewProvider(ProviderApple,
		[]string{"https://appleid.apple.com"},
		clientIDs,
		oidc.NewRemoteKeySet(ctx, "https://appleid.apple.com/auth/keys"))
}

// NewProvider returns a Provider that accepts tokens from any of issuers,
// issued to any of clientIDs and signed by keys. name must be ProviderApple
// or ProviderGoogle. NewApple and NewGoogle cover production use; this is
// exposed for tests.
func NewProvider(name string, issuers, clientIDs []string, keys oidc.KeySet) *Provider {
	return &Provider{
		name:      name,
		issuers:   issuers,
		clientIDs: clientIDs,
		// Issuer and audience are checked in Verify because there is more
		// than one valid value for each.
		verifier: oidc.NewVerifier("", keys, &oidc.Config{SkipIssuerCheck: true, SkipClientIDCheck: true}),
	}
}

// Name returns the provider name, e.g. "google".
func (p *Provider) Name() string { return p.name }

// Verify checks the token's signature, expiry, issuer, audience and (when
// nonce is non-empty) nonce, and returns the identity it asserts.
func (p *Provider) Verify(ctx context.Context, rawIDToken, nonce string) (Identity, error) {
	tok, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", errInvalidIDToken, err)
	}
	if !slices.Contains(p.issuers, tok.Issuer) {
		return Identity{}, fmt.Errorf("%w: unexpected issuer %q", errInvalidIDToken, tok.Issuer)
	}
	if !slices.ContainsFunc(tok.Audience, func(aud string) bool { return slices.Contains(p.clientIDs, aud) }) {
		return Identity{}, fmt.Errorf("%w: unexpected audience %v", errInvalidIDToken, tok.Audience)
	}
	if nonce != "" && !nonceMatches(tok.Nonce, nonce) {
		return Identity{}, fmt.Errorf("%w: nonce mismatch", errInvalidIDToken)
	}
	if tok.Subject == "" || len(tok.Subject) > 255 {
		return Identity{}, fmt.Errorf("%w: bad subject", errInvalidIDToken)
	}

	var claims struct {
		Email         string   `json:"email"`
		EmailVerified flexBool `json:"email_verified"`
	}
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims: %v", errInvalidIDToken, err)
	}
	email, err := normalizeEmail(&claims.Email)
	if err != nil {
		email = "" // missing or unusable; treat as no email
	}
	return Identity{
		Provider:      p.name,
		Subject:       tok.Subject,
		Email:         email,
		EmailVerified: email != "" && bool(claims.EmailVerified),
	}, nil
}

func nonceMatches(claim, raw string) bool {
	sum := sha256.Sum256([]byte(raw))
	hashed := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(claim), []byte(raw)) == 1 ||
		subtle.ConstantTimeCompare([]byte(claim), []byte(hashed)) == 1
}

// flexBool accepts true/false or "true"/"false"; Apple sends the latter.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*b = flexBool(v)
	case string:
		*b = v == "true"
	}
	return nil
}
