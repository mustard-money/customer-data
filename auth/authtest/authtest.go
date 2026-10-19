// Package authtest mints RS256-signed ID tokens for tests, standing in for
// Apple's and Google's signing keys.
package authtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Signer signs ID tokens with a freshly generated RSA key.
type Signer struct {
	key *rsa.PrivateKey
}

// NewSigner returns a Signer with a new 2048-bit key.
func NewSigner(t testing.TB) *Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &Signer{key: key}
}

// KeySet returns a key set that trusts this signer.
func (s *Signer) KeySet() oidc.KeySet {
	return &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&s.key.PublicKey}}
}

// Sign returns a compact JWS of claims.
func (s *Signer) Sign(t testing.TB, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + enc.EncodeToString(sig)
}
