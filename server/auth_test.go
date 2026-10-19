package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/auth/authtest"
)

type sessionBody struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
	Customer  struct {
		ID        string  `json:"id"`
		Email     *string `json:"email"`
		CreatedAt string  `json:"createdAt"`
	} `json:"customer"`
}

func decodeSession(t *testing.T, r response) sessionBody {
	t.Helper()
	var s sessionBody
	if err := json.Unmarshal(r.body, &s); err != nil {
		t.Fatal(err)
	}
	if s.Token == "" || s.Customer.ID == "" {
		t.Fatalf("incomplete session: %s", r.body)
	}
	return s
}

func TestPasswordAuth(t *testing.T) {
	srv := newTestServer(t)

	r := do(t, srv, "POST", "/api/v1/auth/signup", `{"email":" Alex@Example.com ","password":"hunter2222"}`)
	expectStatus(t, r, 201)
	signup := decodeSession(t, r)
	if signup.Customer.Email == nil || *signup.Customer.Email != "alex@example.com" {
		t.Errorf("email = %v", signup.Customer.Email)
	}
	exp, err := time.Parse(time.RFC3339, signup.ExpiresAt)
	if err != nil || exp.Before(time.Now().Add(29*24*time.Hour)) {
		t.Errorf("expiresAt = %q", signup.ExpiresAt)
	}

	expectError(t, do(t, srv, "POST", "/api/v1/auth/signup", `{"email":"alex@example.com","password":"another-pw"}`), 409, "already exists")
	expectError(t, do(t, srv, "POST", "/api/v1/auth/signup", `{"email":"b@example.com","password":"short"}`), 400, "at least 8")
	expectError(t, do(t, srv, "POST", "/api/v1/auth/signup", `{"email":"b@example.com","password":"hunter2222","name":"x"}`), 400, "unknown field")

	expectError(t, do(t, srv, "POST", "/api/v1/auth/login", `{"email":"alex@example.com","password":"wrong-password"}`), 401, "invalid email or password")
	expectError(t, do(t, srv, "POST", "/api/v1/auth/login", `{"email":"nobody@example.com","password":"hunter2222"}`), 401, "invalid email or password")

	r = do(t, srv, "POST", "/api/v1/auth/login", `{"email":"ALEX@example.com","password":"hunter2222"}`)
	expectStatus(t, r, 200)
	login := decodeSession(t, r)
	if login.Customer.ID != signup.Customer.ID || login.Token == signup.Token {
		t.Errorf("login = %+v, signup = %+v", login, signup)
	}

	r = do(t, srv, "GET", "/api/v1/auth/me", "", "Authorization", "Bearer "+login.Token)
	expectStatus(t, r, 200)
	if id := r.json(t)["customer"].(map[string]any)["id"]; id != signup.Customer.ID {
		t.Errorf("me id = %v", id)
	}

	// Logging out one session leaves the other valid.
	expectStatus(t, do(t, srv, "POST", "/api/v1/auth/logout", "", "Authorization", "Bearer "+login.Token), 204)
	r = do(t, srv, "GET", "/api/v1/auth/me", "", "Authorization", "Bearer "+login.Token)
	expectError(t, r, 401, "invalid or expired session")
	if r.header.Get("WWW-Authenticate") == "" {
		t.Error("401 without WWW-Authenticate")
	}
	expectStatus(t, do(t, srv, "GET", "/api/v1/auth/me", "", "Authorization", "Bearer "+signup.Token), 200)

	expectError(t, do(t, srv, "GET", "/api/v1/auth/me", "", anonymousHeader, "1"), 401, "missing bearer token")
	expectError(t, do(t, srv, "POST", "/api/v1/auth/logout", "", anonymousHeader, "1"), 401, "missing bearer token")
	r = do(t, srv, "GET", "/api/v1/auth/login", "")
	expectError(t, r, 405, "not allowed")
	if r.header.Get("Allow") != "POST, OPTIONS" {
		t.Errorf("Allow = %q", r.header.Get("Allow"))
	}
}

func TestProviderAuth(t *testing.T) {
	signer := authtest.NewSigner(t)
	google := auth.NewProvider(auth.ProviderGoogle, []string{"https://accounts.google.com"}, []string{"client"}, signer.KeySet())
	srv := newTestServer(t, google)

	idToken := func(sub, email string) string {
		return signer.Sign(t, map[string]any{
			"iss": "https://accounts.google.com", "aud": "client", "sub": sub,
			"email": email, "email_verified": true,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
	}
	body := func(tok string) string { return `{"idToken":"` + tok + `"}` }

	r := do(t, srv, "POST", "/api/v1/auth/google", body(idToken("g-1", "Sam@Example.com")))
	expectStatus(t, r, 201)
	first := decodeSession(t, r)
	if first.Customer.Email == nil || *first.Customer.Email != "sam@example.com" {
		t.Errorf("email = %v", first.Customer.Email)
	}

	r = do(t, srv, "POST", "/api/v1/auth/google", body(idToken("g-1", "sam@example.com")))
	expectStatus(t, r, 200)
	if again := decodeSession(t, r); again.Customer.ID != first.Customer.ID {
		t.Errorf("second sign-in created a new customer: %s vs %s", again.Customer.ID, first.Customer.ID)
	}
	expectStatus(t, do(t, srv, "GET", "/api/v1/auth/me", "", "Authorization", "Bearer "+first.Token), 200)

	// A password account's email is never auto-linked to a new identity.
	expectStatus(t, do(t, srv, "POST", "/api/v1/auth/signup", `{"email":"pat@example.com","password":"hunter2222"}`), 201)
	expectError(t, do(t, srv, "POST", "/api/v1/auth/google", body(idToken("g-2", "pat@example.com"))), 409, "already exists")
	// ...and a provider-created customer has no password to log in with.
	expectError(t, do(t, srv, "POST", "/api/v1/auth/login", `{"email":"sam@example.com","password":"hunter2222"}`), 401, "invalid email or password")

	expectError(t, do(t, srv, "POST", "/api/v1/auth/google", body("not.a.jwt")), 401, "invalid id token")
	expectError(t, do(t, srv, "POST", "/api/v1/auth/google", `{}`), 400, "idToken is required")
	expectError(t, do(t, srv, "POST", "/api/v1/auth/apple", body(idToken("a-1", "x@example.com"))), 404, "not enabled")
}

func TestDataRequiresAuth(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/api/v1/items", "/api/v1/profiles", "/api/v1/scenarios", "/api/v1/account-profile", "/api/v1/nope"} {
		r := do(t, srv, "GET", path, "", anonymousHeader, "1")
		expectError(t, r, 401, "missing bearer token")
		if r.header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: 401 without WWW-Authenticate", path)
		}
	}
	expectError(t, do(t, srv, "GET", "/api/v1/items", "", "Authorization", "Bearer nope"), 401, "invalid or expired session")
	// Public routes stay public; unknown auth routes are 404, not 401.
	expectStatus(t, do(t, srv, "GET", "/healthz", "", anonymousHeader, "1"), 200)
	expectError(t, do(t, srv, "GET", "/api/v1/auth/nope", "", anonymousHeader, "1"), 404, "not found")
	// CORS preflights never carry credentials and must not be rejected.
	expectStatus(t, do(t, srv, "OPTIONS", "/api/v1/items", "", anonymousHeader, "1",
		"Origin", "http://localhost:5173", "Access-Control-Request-Method", "GET"), 204)
}

func TestDataIsScopedPerCustomer(t *testing.T) {
	srv := newTestServer(t)
	alice := signUp(t, srv, "alice@example.com")
	bob := signUp(t, srv, "bob@example.com")

	item := `{"category":"income","name":"Salary","amount":100,"frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"}`
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/same-id", item, "Authorization", alice), 201)
	// The same id is a separate record for another customer.
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/same-id",
		`{"category":"expenses","name":"Rent","amount":50,"frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"}`,
		"Authorization", bob), 201)

	r := do(t, srv, "GET", "/api/v1/items/same-id", "", "Authorization", alice)
	expectStatus(t, r, 200)
	if name := r.json(t)["name"]; name != "Salary" {
		t.Errorf("alice's item name = %v", name)
	}
	if ids := listIDs(t, do(t, srv, "GET", "/api/v1/items", "", "Authorization", bob)); len(ids) != 1 {
		t.Errorf("bob's items = %v", ids)
	}
	expectStatus(t, do(t, srv, "DELETE", "/api/v1/items/same-id", "", "Authorization", bob), 204)
	expectStatus(t, do(t, srv, "GET", "/api/v1/items/same-id", "", "Authorization", alice), 200)

	expectStatus(t, do(t, srv, "PUT", "/api/v1/account-profile", `{"postalCode":"94103"}`, "Authorization", alice), 201)
	expectError(t, do(t, srv, "GET", "/api/v1/account-profile", "", "Authorization", bob), 404, "not set")
	expectStatus(t, do(t, srv, "PUT", "/api/v1/account-profile", `{"postalCode":"10001"}`, "Authorization", bob), 201)
	if zip := do(t, srv, "GET", "/api/v1/account-profile", "", "Authorization", alice).json(t)["postalCode"]; zip != "94103" {
		t.Errorf("alice's postalCode = %v", zip)
	}

	for _, path := range []string{"/api/v1/profiles", "/api/v1/scenarios"} {
		if ids := listIDs(t, do(t, srv, "GET", path, "", "Authorization", bob), path[len("/api/v1/"):]); len(ids) != 0 {
			t.Errorf("%s for bob = %v", path, ids)
		}
	}
}
