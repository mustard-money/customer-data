package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/db"
)

// newTestServer connects to TEST_DATABASE_URL (skipping the test if unset),
// applies migrations, empties the items table and returns a running server.
func newTestServer(t *testing.T, authProviders ...*auth.Provider) *httptest.Server {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Running migrations twice must be a no-op.
	if err := db.Migrate(ctx, pool, logger); err != nil {
		t.Fatalf("migrate (second run): %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE items, profiles, account_profile, scenarios, customers CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	h := New(pool, Options{
		CORSAllowedOrigins: []string{"http://localhost:5173"},
		Logger:             logger,
		AuthProviders:      authProviders,
	})
	// Data routes need a signed-in customer. Requests without their own
	// Authorization header act as this default customer, unless they set
	// anonymousHeader to test unauthenticated access.
	sess, err := auth.NewStore(pool).SignUp(ctx, "default@example.com", "default-password")
	if err != nil {
		t.Fatalf("sign up default customer: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(anonymousHeader) != "" {
			r.Header.Del(anonymousHeader)
		} else if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+sess.Token)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// anonymousHeader makes a test request skip the default customer's token.
const anonymousHeader = "X-Test-Anonymous"

// signUp creates a customer and returns its Authorization header value.
func signUp(t *testing.T, srv *httptest.Server, email string) string {
	t.Helper()
	r := do(t, srv, "POST", "/api/v1/auth/signup", `{"email":"`+email+`","password":"hunter2222"}`)
	expectStatus(t, r, 201)
	token, _ := r.json(t)["token"].(string)
	return "Bearer " + token
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
	return m
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, headers ...string) response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: res.StatusCode, header: res.Header, body: b}
}

func expectStatus(t *testing.T, r response, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body = %s", r.status, want, r.body)
	}
}

func expectError(t *testing.T, r response, status int, contains string) {
	t.Helper()
	expectStatus(t, r, status)
	msg, _ := r.json(t)["error"].(string)
	if !strings.Contains(msg, contains) {
		t.Errorf("error = %q, want it to contain %q", msg, contains)
	}
}

// listIDs returns the ids in a list response ({"items":[...]} or
// {"profiles":[...]}, per key), failing if the array is missing or null. For
// "deletedIds", an array of plain ids, it returns them as-is.
func listIDs(t *testing.T, r response, key ...string) []string {
	t.Helper()
	k := "items"
	if len(key) > 0 {
		k = key[0]
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	raw := body[k]
	if raw == nil || string(raw) == "null" {
		t.Fatalf("%s must be an array, got %s", k, r.body)
	}
	var ids []string
	if json.Unmarshal(raw, &ids) == nil {
		return ids
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	ids = make([]string, len(records))
	for i, it := range records {
		ids[i] = it.ID
	}
	return ids
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	r := do(t, srv, "GET", "/healthz", "")
	expectStatus(t, r, 200)
	if string(r.body) != "{\"status\":\"ok\"}\n" {
		t.Errorf("body = %q", r.body)
	}
}

func TestItemLifecycle(t *testing.T) {
	srv := newTestServer(t)

	// Empty list is an empty array, not null.
	r := do(t, srv, "GET", "/api/v1/items", "")
	expectStatus(t, r, 200)
	if got := strings.TrimSpace(string(r.body)); got != `{"items":[],"deletedIds":[]}` {
		t.Errorf("empty list = %s", got)
	}

	// Create.
	before := time.Now().Add(-time.Second)
	r = do(t, srv, "PUT", "/api/v1/items/lz3k9abc12",
		`{"id":"lz3k9abc12","category":"income","name":" Salary ","amount":6500,"frequency":"monthly","incomeType":"w2","createdAt":"2026-09-27T06:20:39.030Z","updatedAt":"1999-01-01T00:00:00.000Z"}`)
	expectStatus(t, r, 201)
	created := r.json(t)
	if created["id"] != "lz3k9abc12" || created["name"] != "Salary" || created["amount"] != 6500.0 ||
		created["frequency"] != "monthly" || created["createdAt"] != "2026-09-27T06:20:39.030Z" {
		t.Errorf("created = %v", created)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, created["updatedAt"].(string))
	if err != nil || updatedAt.Before(before) {
		t.Errorf("updatedAt = %v (err %v); should be server time, not client value", created["updatedAt"], err)
	}

	// Get.
	r = do(t, srv, "GET", "/api/v1/items/lz3k9abc12", "")
	expectStatus(t, r, 200)
	if got := r.json(t); got["name"] != "Salary" || got["incomeType"] != "w2" || got["updatedAt"] != created["updatedAt"] {
		t.Errorf("get = %v", got)
	}

	// Full replace (no id in body): switch to investments, frequency dropped.
	time.Sleep(5 * time.Millisecond)
	r = do(t, srv, "PUT", "/api/v1/items/lz3k9abc12",
		`{"category":"investments","name":"Index fund","amount":1234.567,"frequency":null,"account":"roth401k","createdAt":"2026-09-27T06:20:39.030Z"}`)
	expectStatus(t, r, 200)
	updated := r.json(t)
	if updated["category"] != "investments" || updated["amount"] != 1234.57 || updated["name"] != "Index fund" || updated["account"] != "roth401k" {
		t.Errorf("updated = %v", updated)
	}
	if _, ok := updated["frequency"]; ok {
		t.Errorf("frequency should be omitted for investments: %s", r.body)
	}
	if _, ok := updated["incomeType"]; ok {
		t.Errorf("incomeType should be omitted for investments: %s", r.body)
	}
	if updated["updatedAt"] == created["updatedAt"] {
		t.Errorf("updatedAt did not change on update")
	}

	// More items to exercise ordering and filtering.
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/b-exp",
		`{"category":"expenses","name":"Rent","amount":2000,"frequency":"monthly","createdAt":"2026-01-01T00:00:00.000Z"}`), 201)
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/a-exp",
		`{"category":"expenses","name":"Insurance","amount":1200.5,"frequency":"yearly","createdAt":"2026-01-01T00:00:00.000Z"}`), 201)

	r = do(t, srv, "GET", "/api/v1/items", "")
	expectStatus(t, r, 200)
	if got := strings.Join(listIDs(t, r), ","); got != "a-exp,b-exp,lz3k9abc12" {
		t.Errorf("list order = %s", got)
	}

	r = do(t, srv, "GET", "/api/v1/items?category=expenses", "")
	expectStatus(t, r, 200)
	if got := strings.Join(listIDs(t, r), ","); got != "a-exp,b-exp" {
		t.Errorf("filtered list = %s", got)
	}
	r = do(t, srv, "GET", "/api/v1/items?category=income", "")
	expectStatus(t, r, 200)
	if got := listIDs(t, r); len(got) != 0 {
		t.Errorf("income list = %v", got)
	}
	expectError(t, do(t, srv, "GET", "/api/v1/items?category=savings", ""), 400, "category must be one of")

	// Delete, then it is gone.
	r = do(t, srv, "DELETE", "/api/v1/items/lz3k9abc12", "")
	expectStatus(t, r, 204)
	if len(r.body) != 0 {
		t.Errorf("204 body = %q", r.body)
	}
	expectError(t, do(t, srv, "GET", "/api/v1/items/lz3k9abc12", ""), 404, "not found")
	expectError(t, do(t, srv, "DELETE", "/api/v1/items/lz3k9abc12", ""), 404, "not found")
}

// TestDeletions checks that deleted records are remembered per collection:
// listed in deletedIds, and refused (410) if a device tries to save them again.
func TestDeletions(t *testing.T) {
	srv := newTestServer(t)
	bodies := map[string]string{
		"items":     `{"category":"income","name":"Salary","amount":10,"frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"}`,
		"profiles":  `{"name":"Alex","relationship":"self","createdAt":"2026-09-27T06:20:39.033Z"}`,
		"scenarios": `{"name":"Plan","itemIds":[],"events":[],` + scenarioParams + `,"createdAt":"2026-09-27T06:20:39.033Z"}`,
	}
	nouns := map[string]string{"items": "item", "profiles": "profile", "scenarios": "scenario"}
	for collection, body := range bodies {
		t.Run(collection, func(t *testing.T) {
			base := "/api/v1/" + collection
			expectStatus(t, do(t, srv, "PUT", base+"/kept", body), 201)
			expectStatus(t, do(t, srv, "PUT", base+"/gone", body), 201)
			expectStatus(t, do(t, srv, "DELETE", base+"/gone", ""), 204)
			// Deleting an id the server never had still records it (the record
			// may exist only on another device), but answers 404.
			expectError(t, do(t, srv, "DELETE", base+"/never-uploaded", ""), 404, "not found")

			r := do(t, srv, "GET", base, "")
			expectStatus(t, r, 200)
			if got := strings.Join(listIDs(t, r, collection), ","); got != "kept" {
				t.Errorf("ids = %s", got)
			}
			if got := strings.Join(listIDs(t, r, "deletedIds"), ","); got != "gone,never-uploaded" {
				t.Errorf("deletedIds = %s", got)
			}

			// A device still holding a deleted record can't bring it back.
			expectError(t, do(t, srv, "PUT", base+"/gone", body), 410, nouns[collection]+" was deleted")
			expectError(t, do(t, srv, "PUT", base+"/never-uploaded", body), 410, "was deleted")
			expectError(t, do(t, srv, "GET", base+"/gone", ""), 404, "not found")
			// Deleting again is harmless.
			expectError(t, do(t, srv, "DELETE", base+"/gone", ""), 404, "not found")
		})
	}
}

func TestInvestmentValueAndExpectedReturn(t *testing.T) {
	srv := newTestServer(t)
	r := do(t, srv, "PUT", "/api/v1/items/brokerage",
		`{"category":"investments","name":"Brokerage","amount":50000,"asOfYear":2025,"asOfMonth":3,"returnRate":0.06,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 201)
	got := r.json(t)
	if got["asOfYear"] != 2025.0 || got["asOfMonth"] != 3.0 || got["returnRate"] != 0.06 {
		t.Errorf("investment = %v", got)
	}
	// Saving without them clears them.
	r = do(t, srv, "PUT", "/api/v1/items/brokerage",
		`{"category":"investments","name":"Brokerage","amount":50000,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 200)
	for _, field := range []string{"asOfYear", "asOfMonth", "returnRate"} {
		if strings.Contains(string(r.body), field) {
			t.Errorf("%s should be gone: %s", field, r.body)
		}
	}

	r = do(t, srv, "PUT", "/api/v1/account-profile", `{"postalCode":"94103","expectedReturn":0.065,"inflation":0.025,"emergencyFund":30000.5}`)
	expectStatus(t, r, 201)
	if got := r.json(t); got["expectedReturn"] != 0.065 || got["inflation"] != 0.025 || got["emergencyFund"] != 30000.5 {
		t.Errorf("profile = %v", got)
	}
	r = do(t, srv, "PUT", "/api/v1/account-profile", `{"postalCode":"94103"}`)
	expectStatus(t, r, 200)
	for _, field := range []string{"expectedReturn", "inflation", "emergencyFund"} {
		if strings.Contains(string(r.body), field) {
			t.Errorf("%s should be cleared: %s", field, r.body)
		}
	}
	expectError(t, do(t, srv, "PUT", "/api/v1/account-profile", `{"inflation":0.5}`), 400, "inflation must be between")
	expectError(t, do(t, srv, "PUT", "/api/v1/account-profile", `{"expectedReturn":2}`), 400, "expectedReturn must be between")
}

func TestIncomeDatesAndWithdrawals(t *testing.T) {
	srv := newTestServer(t)
	r := do(t, srv, "PUT", "/api/v1/items/pension",
		`{"category":"income","name":"Pension","amount":2000,"frequency":"monthly","incomeType":"1099r","startYear":2040,"startMonth":7,"endYear":2070,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 201)
	if got := r.json(t); got["startYear"] != 2040.0 || got["startMonth"] != 7.0 || got["endYear"] != 2070.0 {
		t.Errorf("income = %v", got)
	}

	r = do(t, srv, "PUT", "/api/v1/items/ira",
		`{"category":"investments","name":"IRA","amount":300000,"account":"traditionalIra","withdrawalStartYear":2045,"withdrawalRate":0.04,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 201)
	if got := r.json(t); got["withdrawalStartYear"] != 2045.0 || got["withdrawalRate"] != 0.04 {
		t.Errorf("investment = %v", got)
	}
	// Saving without the plan clears it.
	r = do(t, srv, "PUT", "/api/v1/items/ira",
		`{"category":"investments","name":"IRA","amount":300000,"account":"traditionalIra","createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 200)
	if strings.Contains(string(r.body), "withdrawal") {
		t.Errorf("withdrawal plan should be gone: %s", r.body)
	}

	expectError(t, do(t, srv, "PUT", "/api/v1/items/bank",
		`{"category":"investments","name":"Bank","amount":1,"account":"bank","withdrawalStartYear":2045,"withdrawalRate":0.04,"createdAt":"2026-09-27T06:20:39.033Z"}`),
		400, "withdrawals are only for investments in retirement accounts")
}

func TestItemWorkplace(t *testing.T) {
	srv := newTestServer(t)
	r := do(t, srv, "PUT", "/api/v1/items/job-401k",
		`{"category":"investments","name":"Teacher: 403(b)","amount":80000,"account":"roth403b","workplace":{"incomeId":"job","plan":"retirement"},"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 201)
	r = do(t, srv, "GET", "/api/v1/items/job-401k", "")
	expectStatus(t, r, 200)
	if !strings.Contains(string(r.body), `"workplace":{"incomeId":"job","plan":"retirement"}`) {
		t.Errorf("workplace not kept: %s", r.body)
	}
	// Saving without the link (the plan was removed from the income) clears it.
	r = do(t, srv, "PUT", "/api/v1/items/job-401k",
		`{"category":"investments","name":"Teacher: 403(b)","amount":80000,"account":"roth403b","createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 200)
	if strings.Contains(string(r.body), "workplace") {
		t.Errorf("workplace should be gone: %s", r.body)
	}
	expectError(t, do(t, srv, "PUT", "/api/v1/items/job-hsa",
		`{"category":"investments","name":"HSA","amount":1,"account":"bank","workplace":{"incomeId":"job","plan":"hsa"},"createdAt":"2026-09-27T06:20:39.033Z"}`),
		400, "a hsa workplace account must have account hsa")
}

func TestItemBenefits(t *testing.T) {
	srv := newTestServer(t)
	const benefits = `"benefits":{"retirement":{"contributionRate":0.07,"roth":true,"matchRate":1,"matchUpTo":0.04,"balance":{"amount":80000,"asOfYear":2025,"asOfMonth":3}},"plan457":{"contributionRate":0.03,"balance":{"amount":1500.005}},"hsa":{"employeeAnnual":3000,"employerAnnual":500,"balance":{"amount":6000,"asOfYear":2026}}}`
	r := do(t, srv, "PUT", "/api/v1/items/job",
		`{"category":"income","name":"Teacher","amount":6000,"frequency":"monthly","incomeType":"w2",`+benefits+`,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 201)
	r = do(t, srv, "GET", "/api/v1/items/job", "")
	expectStatus(t, r, 200)
	var got struct {
		Benefits json.RawMessage `json:"benefits"`
	}
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatal(err)
	}
	if want := `{"retirement":{"contributionRate":0.07,"roth":true,"matchRate":1,"matchUpTo":0.04,"balance":{"amount":80000,"asOfYear":2025,"asOfMonth":3}},"plan457":{"contributionRate":0.03,"roth":false,"balance":{"amount":1500.01}},"hsa":{"employeeAnnual":3000,"employerAnnual":500,"family":false,"balance":{"amount":6000,"asOfYear":2026}}}`; string(got.Benefits) != want {
		t.Errorf("benefits = %s", got.Benefits)
	}

	// Saving without benefits removes them.
	r = do(t, srv, "PUT", "/api/v1/items/job",
		`{"category":"income","name":"Teacher","amount":6000,"frequency":"monthly","incomeType":"w2","createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 200)
	if strings.Contains(string(r.body), "benefits") {
		t.Errorf("benefits should be gone: %s", r.body)
	}

	expectError(t, do(t, srv, "PUT", "/api/v1/items/gig",
		`{"category":"income","name":"Gig","amount":1,"frequency":"monthly","incomeType":"1099nec",`+benefits+`,"createdAt":"2026-09-27T06:20:39.033Z"}`),
		400, "benefits must be omitted")
	expectError(t, do(t, srv, "PUT", "/api/v1/items/gig",
		`{"category":"income","name":"Gig","amount":1,"frequency":"monthly","incomeType":"w2","benefits":{"hsa":{"employeeAnnual":1,"employerAnnual":0,"extra":1}},"createdAt":"2026-09-27T06:20:39.033Z"}`),
		400, "unknown field")
}

func TestNotFound(t *testing.T) {
	srv := newTestServer(t)
	expectError(t, do(t, srv, "GET", "/api/v1/items/missing", ""), 404, "item not found")
	expectError(t, do(t, srv, "DELETE", "/api/v1/items/missing", ""), 404, "item not found")
	expectError(t, do(t, srv, "GET", "/api/v1/items/bad.id", ""), 404, "item not found")
	expectError(t, do(t, srv, "GET", "/nope", ""), 404, "not found")
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	r := do(t, srv, "POST", "/api/v1/items", `{}`)
	expectError(t, r, 405, "not allowed")
	if r.header.Get("Allow") == "" {
		t.Error("missing Allow header")
	}
	expectError(t, do(t, srv, "PATCH", "/api/v1/items/x", `{}`), 405, "not allowed")
}

func TestPutValidation(t *testing.T) {
	srv := newTestServer(t)
	const good = `"category":"income","name":"Salary","amount":10,"frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"`
	tests := []struct {
		name, path, body, want string
	}{
		{"id mismatch", "/api/v1/items/abc", `{"id":"xyz",` + good + `}`, "does not match"},
		{"invalid path id", "/api/v1/items/bad.id", `{` + good + `}`, "id must contain only"},
		{"malformed json", "/api/v1/items/abc", `{"category":`, "malformed JSON"},
		{"empty body", "/api/v1/items/abc", ``, "must not be empty"},
		{"unknown field", "/api/v1/items/abc", `{"color":"red",` + good + `}`, `unknown field "color"`},
		{"amount as string", "/api/v1/items/abc", `{"category":"income","name":"x","amount":"10","frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"}`, `"amount" must be a number`},
		{"bad category", "/api/v1/items/abc", `{"category":"savings","name":"x","amount":1,"createdAt":"2026-09-27T06:20:39.033Z"}`, "category must be one of"},
		{"blank name", "/api/v1/items/abc", `{"category":"income","name":"  ","amount":1,"frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"}`, "name must not be blank"},
		{"negative amount", "/api/v1/items/abc", `{"category":"income","name":"x","amount":-5,"frequency":"monthly","createdAt":"2026-09-27T06:20:39.033Z"}`, "amount must be"},
		{"missing frequency", "/api/v1/items/abc", `{"category":"expenses","name":"x","amount":1,"createdAt":"2026-09-27T06:20:39.033Z"}`, "frequency is required"},
		{"investments frequency", "/api/v1/items/abc", `{"category":"investments","name":"x","amount":1,"frequency":"yearly","createdAt":"2026-09-27T06:20:39.033Z"}`, "must be omitted or null"},
		{"bad incomeType", "/api/v1/items/abc", `{"category":"income","name":"x","amount":1,"frequency":"monthly","incomeType":"1040","createdAt":"2026-09-27T06:20:39.033Z"}`, "incomeType must be one of"},
		{"account on income", "/api/v1/items/abc", `{"category":"income","name":"x","amount":1,"frequency":"monthly","account":"rothIra","createdAt":"2026-09-27T06:20:39.033Z"}`, "account must be omitted or null for income"},
		{"bad account", "/api/v1/items/abc", `{"category":"investments","name":"x","amount":1,"account":"savings","createdAt":"2026-09-27T06:20:39.033Z"}`, "account must be one of"},
		{"missing createdAt", "/api/v1/items/abc", `{"category":"income","name":"x","amount":1,"frequency":"monthly"}`, "createdAt is required"},
		{"too large", "/api/v1/items/abc", `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, "must not be larger than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectError(t, do(t, srv, "PUT", tt.path, tt.body), 400, tt.want)
		})
	}
	// Nothing was stored.
	if ids := listIDs(t, do(t, srv, "GET", "/api/v1/items", "")); len(ids) != 0 {
		t.Errorf("items stored despite validation failures: %v", ids)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := newTestServer(t)

	r := do(t, srv, "OPTIONS", "/api/v1/items/abc", "",
		"Origin", "http://localhost:5173",
		"Access-Control-Request-Method", "PUT",
		"Access-Control-Request-Headers", "content-type")
	expectStatus(t, r, 204)
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":  "http://localhost:5173",
		"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// Actual request from an allowed origin carries the header.
	r = do(t, srv, "GET", "/api/v1/items", "", "Origin", "http://localhost:5173")
	expectStatus(t, r, 200)
	if got := r.header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Allow-Origin = %q", got)
	}

	// Disallowed origins get no CORS headers.
	r = do(t, srv, "OPTIONS", "/api/v1/items", "",
		"Origin", "https://evil.example", "Access-Control-Request-Method", "GET")
	expectStatus(t, r, 204)
	if got := r.header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin for disallowed origin = %q", got)
	}
}

func TestProfileLifecycle(t *testing.T) {
	srv := newTestServer(t)

	r := do(t, srv, "GET", "/api/v1/profiles", "")
	expectStatus(t, r, 200)
	if got := strings.TrimSpace(string(r.body)); got != `{"profiles":[],"deletedIds":[]}` {
		t.Errorf("empty list = %s", got)
	}

	// Create.
	before := time.Now().Add(-time.Second)
	r = do(t, srv, "PUT", "/api/v1/profiles/lz3k9abc12",
		`{"id":"lz3k9abc12","name":" Alex ","relationship":"self","birthYear":1988,"birthMonth":3,"createdAt":"2026-09-27T06:20:39.033Z","updatedAt":"1999-01-01T00:00:00.000Z"}`)
	expectStatus(t, r, 201)
	created := r.json(t)
	if created["id"] != "lz3k9abc12" || created["name"] != "Alex" || created["relationship"] != "self" ||
		created["birthYear"] != 1988.0 || created["birthMonth"] != 3.0 || created["createdAt"] != "2026-09-27T06:20:39.033Z" {
		t.Errorf("created = %v", created)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, created["updatedAt"].(string))
	if err != nil || updatedAt.Before(before) {
		t.Errorf("updatedAt = %v (err %v); should be server time", created["updatedAt"], err)
	}

	// Get.
	r = do(t, srv, "GET", "/api/v1/profiles/lz3k9abc12", "")
	expectStatus(t, r, 200)
	if got := r.json(t); got["name"] != "Alex" || got["birthYear"] != 1988.0 || got["birthMonth"] != 3.0 {
		t.Errorf("get = %v", got)
	}

	// Full replace clears birthYear and birthMonth (null/absent) and changes fields.
	time.Sleep(5 * time.Millisecond)
	r = do(t, srv, "PUT", "/api/v1/profiles/lz3k9abc12",
		`{"name":"Alex R.","relationship":"partner","birthYear":null,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 200)
	updated := r.json(t)
	if updated["name"] != "Alex R." || updated["relationship"] != "partner" {
		t.Errorf("updated = %v", updated)
	}
	for _, field := range []string{"birthYear", "birthMonth"} {
		if _, ok := updated[field]; ok {
			t.Errorf("%s should be omitted when unset: %s", field, r.body)
		}
	}
	if updated["updatedAt"] == created["updatedAt"] {
		t.Error("updatedAt did not change on update")
	}

	// Ordering by createdAt then id.
	expectStatus(t, do(t, srv, "PUT", "/api/v1/profiles/kid-b",
		`{"name":"Sam","relationship":"child","birthYear":2019,"createdAt":"2026-01-01T00:00:00.000Z"}`), 201)
	expectStatus(t, do(t, srv, "PUT", "/api/v1/profiles/kid-a",
		`{"name":"Jo","relationship":"child","createdAt":"2026-01-01T00:00:00.000Z"}`), 201)
	r = do(t, srv, "GET", "/api/v1/profiles", "")
	expectStatus(t, r, 200)
	if got := strings.Join(listIDs(t, r, "profiles"), ","); got != "kid-a,kid-b,lz3k9abc12" {
		t.Errorf("list order = %s", got)
	}

	// Profiles and items are independent resources.
	if ids := listIDs(t, do(t, srv, "GET", "/api/v1/items", "")); len(ids) != 0 {
		t.Errorf("items = %v", ids)
	}

	// Delete.
	expectStatus(t, do(t, srv, "DELETE", "/api/v1/profiles/lz3k9abc12", ""), 204)
	expectError(t, do(t, srv, "GET", "/api/v1/profiles/lz3k9abc12", ""), 404, "profile not found")
	expectError(t, do(t, srv, "DELETE", "/api/v1/profiles/lz3k9abc12", ""), 404, "profile not found")
}

func TestProfileErrors(t *testing.T) {
	srv := newTestServer(t)
	const ts = `"createdAt":"2026-09-27T06:20:39.033Z"`
	tests := []struct {
		name, path, body, want string
	}{
		{"id mismatch", "/api/v1/profiles/abc", `{"id":"xyz","name":"A","relationship":"self",` + ts + `}`, "does not match"},
		{"invalid path id", "/api/v1/profiles/bad.id", `{"name":"A","relationship":"self",` + ts + `}`, "id must contain only"},
		{"malformed json", "/api/v1/profiles/abc", `{"name":`, "malformed JSON"},
		{"unknown field", "/api/v1/profiles/abc", `{"age":3,"name":"A","relationship":"self",` + ts + `}`, `unknown field "age"`},
		{"blank name", "/api/v1/profiles/abc", `{"name":" ","relationship":"self",` + ts + `}`, "name must not be blank"},
		{"bad relationship", "/api/v1/profiles/abc", `{"name":"A","relationship":"pet",` + ts + `}`, "relationship must be one of"},
		{"missing relationship", "/api/v1/profiles/abc", `{"name":"A",` + ts + `}`, "relationship is required"},
		{"birthYear too old", "/api/v1/profiles/abc", `{"name":"A","relationship":"self","birthYear":1899,` + ts + `}`, "birthYear must be between 1900"},
		{"birthYear future", "/api/v1/profiles/abc", `{"name":"A","relationship":"self","birthYear":3000,` + ts + `}`, "birthYear must be between 1900"},
		{"birthYear fractional", "/api/v1/profiles/abc", `{"name":"A","relationship":"self","birthYear":1988.5,` + ts + `}`, `"birthYear" must be an integer`},
		{"birthMonth without year", "/api/v1/profiles/abc", `{"name":"A","relationship":"self","birthMonth":3,` + ts + `}`, "birthMonth requires birthYear"},
		{"birthMonth out of range", "/api/v1/profiles/abc", `{"name":"A","relationship":"self","birthYear":1988,"birthMonth":13,` + ts + `}`, "birthMonth must be between 1 and 12"},
		{"missing createdAt", "/api/v1/profiles/abc", `{"name":"A","relationship":"self"}`, "createdAt is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectError(t, do(t, srv, "PUT", tt.path, tt.body), 400, tt.want)
		})
	}
	if ids := listIDs(t, do(t, srv, "GET", "/api/v1/profiles", ""), "profiles"); len(ids) != 0 {
		t.Errorf("profiles stored despite validation failures: %v", ids)
	}

	expectError(t, do(t, srv, "GET", "/api/v1/profiles/missing", ""), 404, "profile not found")
	expectError(t, do(t, srv, "POST", "/api/v1/profiles", `{}`), 405, "not allowed")
	expectError(t, do(t, srv, "PATCH", "/api/v1/profiles/x", `{}`), 405, "not allowed")

	r := do(t, srv, "OPTIONS", "/api/v1/profiles/abc", "",
		"Origin", "http://localhost:5173", "Access-Control-Request-Method", "DELETE")
	expectStatus(t, r, 204)
	if got := r.header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Allow-Origin = %q", got)
	}
}

func TestIncomeFrequencies(t *testing.T) {
	srv := newTestServer(t)
	for _, f := range []string{"weekly", "biweekly", "semimonthly", "monthly", "quarterly", "semiannually", "yearly"} {
		r := do(t, srv, "PUT", "/api/v1/items/inc-"+f,
			`{"category":"income","name":"Pay","amount":100,"frequency":"`+f+`","createdAt":"2026-01-01T00:00:00.000Z"}`)
		expectStatus(t, r, 201)
		if got := r.json(t)["frequency"]; got != f {
			t.Errorf("frequency = %v, want %s", got, f)
		}
	}
	// Expenses have their own set, which includes daily, and can have start and end dates.
	for _, f := range []string{"daily", "weekly", "monthly", "yearly"} {
		r := do(t, srv, "PUT", "/api/v1/items/exp-"+f,
			`{"category":"expenses","name":"Food","amount":10,"frequency":"`+f+`","startYear":2027,"startMonth":3,"endYear":2030,"createdAt":"2026-01-01T00:00:00.000Z"}`)
		expectStatus(t, r, 201)
		got := r.json(t)
		if got["frequency"] != f || got["startYear"] != 2027.0 || got["startMonth"] != 3.0 || got["endYear"] != 2030.0 {
			t.Errorf("expense = %v", got)
		}
	}
	// A one-time expense happens in its start month and has no end.
	r := do(t, srv, "PUT", "/api/v1/items/exp-once",
		`{"category":"expenses","name":"New roof","amount":30000,"frequency":"once","startYear":2028,"startMonth":5,"createdAt":"2026-01-01T00:00:00.000Z"}`)
	expectStatus(t, r, 201)
	if got := r.json(t); got["frequency"] != "once" || got["startYear"] != 2028.0 || got["endYear"] != nil {
		t.Errorf("one-time expense = %v", got)
	}
	expectError(t, do(t, srv, "PUT", "/api/v1/items/exp-once-end",
		`{"category":"expenses","name":"New roof","amount":30000,"frequency":"once","startYear":2028,"endYear":2029,"createdAt":"2026-01-01T00:00:00.000Z"}`),
		400, "end date must be omitted or null for one-time expenses")
	expectError(t, do(t, srv, "PUT", "/api/v1/items/exp-biweekly",
		`{"category":"expenses","name":"Food","amount":100,"frequency":"biweekly","createdAt":"2026-01-01T00:00:00.000Z"}`),
		400, "frequency for expenses must be one of: once, daily, weekly, monthly, yearly")
	expectError(t, do(t, srv, "PUT", "/api/v1/items/inc-once",
		`{"category":"income","name":"Bonus","amount":100,"frequency":"once","createdAt":"2026-01-01T00:00:00.000Z"}`),
		400, "frequency for income must be one of:")
	expectError(t, do(t, srv, "PUT", "/api/v1/items/inc-daily",
		`{"category":"income","name":"Pay","amount":100,"frequency":"daily","createdAt":"2026-01-01T00:00:00.000Z"}`),
		400, "frequency for income must be one of:")
}

func TestAccountProfile(t *testing.T) {
	srv := newTestServer(t)

	expectError(t, do(t, srv, "GET", "/api/v1/account-profile", ""), 404, "account profile not set")

	// Create. A client-sent updatedAt is ignored; the ZIP code is trimmed.
	r := do(t, srv, "PUT", "/api/v1/account-profile",
		`{"country":"US","postalCode":" 94103 ","filingStatus":"marriedJoint","updatedAt":"1999-01-01T00:00:00.000Z"}`)
	expectStatus(t, r, 201)
	created := r.json(t)
	if created["country"] != "US" || created["postalCode"] != "94103" || created["filingStatus"] != "marriedJoint" ||
		created["updatedAt"] == "1999-01-01T00:00:00.000Z" {
		t.Errorf("created = %v", created)
	}

	r = do(t, srv, "GET", "/api/v1/account-profile", "")
	expectStatus(t, r, 200)
	if got := r.json(t); got["postalCode"] != "94103" {
		t.Errorf("get = %v", got)
	}

	// Full replace without a country: it defaults to US, filingStatus to single.
	time.Sleep(5 * time.Millisecond)
	r = do(t, srv, "PUT", "/api/v1/account-profile", `{"postalCode":"10001-1234"}`)
	expectStatus(t, r, 200)
	updated := r.json(t)
	if updated["country"] != "US" || updated["postalCode"] != "10001-1234" || updated["filingStatus"] != "single" {
		t.Errorf("updated = %v", updated)
	}
	if updated["updatedAt"] == created["updatedAt"] {
		t.Error("updatedAt did not change on update")
	}

	for _, tt := range []struct{ name, body, want string }{
		{"non-US country", `{"country":"HK"}`, "country must be US"},
		{"invalid ZIP code", `{"postalCode":"100-0001"}`, "not a valid US ZIP code"},
		{"bad filing status", `{"filingStatus":"married"}`, "filingStatus must be one of"},
		{"unknown field", `{"zip":"94103"}`, `unknown field "zip"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			expectError(t, do(t, srv, "PUT", "/api/v1/account-profile", tt.body), 400, tt.want)
		})
	}
	expectError(t, do(t, srv, "POST", "/api/v1/account-profile", `{}`), 405, "not allowed")
}

const scenarioParams = `"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0}`

func TestScenarioLifecycle(t *testing.T) {
	srv := newTestServer(t)

	r := do(t, srv, "GET", "/api/v1/scenarios", "")
	expectStatus(t, r, 200)
	if got := strings.TrimSpace(string(r.body)); got != `{"scenarios":[],"deletedIds":[]}` {
		t.Errorf("empty list = %s", got)
	}

	// Create. The client updatedAt is ignored; names are trimmed; amounts are
	// rounded to cents; null months/years on other kinds are fine.
	before := time.Now().Add(-time.Second)
	r = do(t, srv, "PUT", "/api/v1/scenarios/lz3k9abc12", `{
		"id":"lz3k9abc12","name":" Buy a house in 2028 ",
		"itemIds":["mujge6vxt1vsmn","mujgf43apl78jy"],
		"events":[
			{"id":"ev1","kind":"oneTimeCost","name":" Down payment ","year":2,"amount":250000,"months":null},
			{"id":"ev2","kind":"incomePause","name":"Sabbatical","year":3,"amount":0,"months":6},
			{"id":"ev3","kind":"recurringCost","name":"Childcare","year":4,"amount":30000.004,"years":5},
			{"id":"ev4","kind":"oneTimeIncome","name":"RSU sale","year":1,"amount":80000,"years":null}
		],
		"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0.1,"expenseAdjustment":-0.25},
		"createdAt":"2026-09-27T06:20:39.033Z","updatedAt":"1999-01-01T00:00:00.000Z"}`)
	expectStatus(t, r, 201)
	created := r.json(t)
	updatedAt, err := time.Parse(time.RFC3339Nano, created["updatedAt"].(string))
	if err != nil || updatedAt.Before(before) {
		t.Errorf("updatedAt = %v (err %v); should be server time", created["updatedAt"], err)
	}

	// The stored scenario round-trips exactly, with key order preserved and
	// months/years omitted where unset.
	wantBody := `{"id":"lz3k9abc12","name":"Buy a house in 2028","itemIds":["mujge6vxt1vsmn","mujgf43apl78jy"],"events":[` +
		`{"id":"ev1","kind":"oneTimeCost","name":"Down payment","year":2,"amount":250000},` +
		`{"id":"ev2","kind":"incomePause","name":"Sabbatical","year":3,"amount":0,"months":6},` +
		`{"id":"ev3","kind":"recurringCost","name":"Childcare","year":4,"amount":30000,"years":5},` +
		`{"id":"ev4","kind":"oneTimeIncome","name":"RSU sale","year":1,"amount":80000}],` +
		`"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0.1,"expenseAdjustment":-0.25},` +
		`"createdAt":"2026-09-27T06:20:39.033Z","updatedAt":"` + created["updatedAt"].(string) + `"}`
	if got := strings.TrimSpace(string(r.body)); got != wantBody {
		t.Errorf("created body\n got  %s\n want %s", got, wantBody)
	}
	r = do(t, srv, "GET", "/api/v1/scenarios/lz3k9abc12", "")
	expectStatus(t, r, 200)
	if got := strings.TrimSpace(string(r.body)); got != wantBody {
		t.Errorf("get body\n got  %s\n want %s", got, wantBody)
	}

	// Full replace (no id in body) with empty arrays.
	time.Sleep(5 * time.Millisecond)
	r = do(t, srv, "PUT", "/api/v1/scenarios/lz3k9abc12",
		`{"name":"Stay put","itemIds":[],"events":[],`+scenarioParams+`,"createdAt":"2026-09-27T06:20:39.033Z"}`)
	expectStatus(t, r, 200)
	updated := r.json(t)
	if updated["name"] != "Stay put" {
		t.Errorf("updated = %v", updated)
	}
	if !strings.Contains(string(r.body), `"itemIds":[],"events":[]`) {
		t.Errorf("empty arrays should render as []: %s", r.body)
	}
	if updated["updatedAt"] == created["updatedAt"] {
		t.Error("updatedAt did not change on update")
	}

	// Ordering by createdAt then id.
	for _, id := range []string{"s-b", "s-a"} {
		expectStatus(t, do(t, srv, "PUT", "/api/v1/scenarios/"+id,
			`{"name":"`+id+`","itemIds":[],"events":[],`+scenarioParams+`,"createdAt":"2026-01-01T00:00:00.000Z"}`), 201)
	}
	r = do(t, srv, "GET", "/api/v1/scenarios", "")
	expectStatus(t, r, 200)
	if got := strings.Join(listIDs(t, r, "scenarios"), ","); got != "s-a,s-b,lz3k9abc12" {
		t.Errorf("list order = %s", got)
	}

	// Delete.
	r = do(t, srv, "DELETE", "/api/v1/scenarios/lz3k9abc12", "")
	expectStatus(t, r, 204)
	if len(r.body) != 0 {
		t.Errorf("204 body = %q", r.body)
	}
	expectError(t, do(t, srv, "GET", "/api/v1/scenarios/lz3k9abc12", ""), 404, "scenario not found")
	expectError(t, do(t, srv, "DELETE", "/api/v1/scenarios/lz3k9abc12", ""), 404, "scenario not found")
}

func TestScenarioErrors(t *testing.T) {
	srv := newTestServer(t)
	const (
		ts   = `"createdAt":"2026-09-27T06:20:39.033Z"`
		base = `"name":"S","itemIds":[],` + scenarioParams + `,` + ts
	)
	ev := func(e string) string { return `{` + base + `,"events":[` + e + `]}` }
	tests := []struct {
		name, path, body, want string
	}{
		{"id mismatch", "/api/v1/scenarios/abc", `{"id":"xyz",` + base + `,"events":[]}`, "does not match"},
		{"invalid path id", "/api/v1/scenarios/bad.id", `{` + base + `,"events":[]}`, "id must contain only"},
		{"malformed json", "/api/v1/scenarios/abc", `{"name":`, "malformed JSON"},
		{"unknown field", "/api/v1/scenarios/abc", `{"color":"red",` + base + `,"events":[]}`, `unknown field "color"`},
		{"unknown parameter", "/api/v1/scenarios/abc",
			`{"name":"S","itemIds":[],"events":[],"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0,"taxRate":0.2},` + ts + `}`,
			`unknown field "taxRate"`},
		{"unknown event field", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"oneTimeCost","name":"x","year":1,"amount":1,"note":"n"}`), `unknown field "note"`},
		{"blank name", "/api/v1/scenarios/abc", `{"name":" ","itemIds":[],"events":[],` + scenarioParams + `,` + ts + `}`, "name must not be blank"},
		{"missing createdAt", "/api/v1/scenarios/abc", `{"name":"S","itemIds":[],"events":[],` + scenarioParams + `}`, "createdAt is required"},
		{"missing itemIds", "/api/v1/scenarios/abc", `{"name":"S","events":[],` + scenarioParams + `,` + ts + `}`, "itemIds is required"},
		{"null itemIds", "/api/v1/scenarios/abc", `{"name":"S","itemIds":null,"events":[],` + scenarioParams + `,` + ts + `}`, "itemIds is required"},
		{"itemIds not array", "/api/v1/scenarios/abc", `{"name":"S","itemIds":"a","events":[],` + scenarioParams + `,` + ts + `}`, `"itemIds"`},
		{"bad itemId", "/api/v1/scenarios/abc", `{"name":"S","itemIds":["ok","no way"],"events":[],` + scenarioParams + `,` + ts + `}`, "itemIds[1] must contain only"},
		{"duplicate itemIds", "/api/v1/scenarios/abc", `{"name":"S","itemIds":["a","a"],"events":[],` + scenarioParams + `,` + ts + `}`, "must not contain duplicates"},
		{"missing events", "/api/v1/scenarios/abc", `{` + base + `}`, "events is required"},
		{"missing parameters", "/api/v1/scenarios/abc", `{"name":"S","itemIds":[],"events":[],` + ts + `}`, "parameters is required"},
		{"partial parameters", "/api/v1/scenarios/abc", `{"name":"S","itemIds":[],"events":[],"parameters":{"years":20},` + ts + `}`, "parameters.returnRate is required"},
		{"returnRate out of range", "/api/v1/scenarios/abc",
			`{"name":"S","itemIds":[],"events":[],"parameters":{"years":20,"returnRate":0.6,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0},` + ts + `}`,
			"parameters.returnRate must be between -0.5 and 0.5"},
		{"fractional parameter years", "/api/v1/scenarios/abc",
			`{"name":"S","itemIds":[],"events":[],"parameters":{"years":20.5,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0},` + ts + `}`,
			"must be an integer"},
		{"bad event kind", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"lottery","name":"x","year":1,"amount":1}`), "events[0].kind must be one of"},
		{"event year out of range", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"oneTimeCost","name":"x","year":61,"amount":1}`), "events[0].year must be between 1 and 60"},
		{"event negative amount", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"oneTimeCost","name":"x","year":1,"amount":-1}`), "events[0].amount must be a number >= 0"},
		{"incomePause amount", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"incomePause","name":"x","year":1,"amount":5,"months":3}`), "amount must be 0 for incomePause"},
		{"incomePause no months", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"incomePause","name":"x","year":1,"amount":0}`), "events[0].months is required for incomePause"},
		{"recurringCost no years", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"recurringCost","name":"x","year":1,"amount":1}`), "events[0].years is required for recurringCost"},
		{"months on oneTimeCost", "/api/v1/scenarios/abc", ev(`{"id":"e","kind":"oneTimeCost","name":"x","year":1,"amount":1,"months":2}`), "months must be omitted or null for oneTimeCost"},
		{"duplicate event ids", "/api/v1/scenarios/abc",
			ev(`{"id":"e","kind":"oneTimeCost","name":"x","year":1,"amount":1},{"id":"e","kind":"oneTimeIncome","name":"y","year":2,"amount":1}`),
			"must not contain duplicate ids"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectError(t, do(t, srv, "PUT", tt.path, tt.body), 400, tt.want)
		})
	}
	if ids := listIDs(t, do(t, srv, "GET", "/api/v1/scenarios", ""), "scenarios"); len(ids) != 0 {
		t.Errorf("scenarios stored despite validation failures: %v", ids)
	}

	expectError(t, do(t, srv, "GET", "/api/v1/scenarios/missing", ""), 404, "scenario not found")
	expectError(t, do(t, srv, "DELETE", "/api/v1/scenarios/missing", ""), 404, "scenario not found")
	expectError(t, do(t, srv, "GET", "/api/v1/scenarios/bad.id", ""), 404, "scenario not found")
	r := do(t, srv, "POST", "/api/v1/scenarios", `{}`)
	expectError(t, r, 405, "not allowed")
	if got := r.header.Get("Allow"); got != "GET, OPTIONS" {
		t.Errorf("Allow = %q", got)
	}
	r = do(t, srv, "PATCH", "/api/v1/scenarios/x", `{}`)
	expectError(t, r, 405, "not allowed")
	if got := r.header.Get("Allow"); got != "GET, PUT, DELETE, OPTIONS" {
		t.Errorf("Allow = %q", got)
	}

	r = do(t, srv, "OPTIONS", "/api/v1/scenarios/abc", "",
		"Origin", "http://localhost:5173",
		"Access-Control-Request-Method", "PUT",
		"Access-Control-Request-Headers", "content-type")
	expectStatus(t, r, 204)
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":  "http://localhost:5173",
		"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestAssistantRoute(t *testing.T) {
	srv := newTestServer(t)
	const body = `{"messages":[{"role":"user","content":"hi"}],"context":{"today":"2026-10-02","items":[],"household":[],"profile":null}}`
	// Only signed-in customers can use the model.
	expectError(t, do(t, srv, "POST", "/api/v1/assistant/chat", body, anonymousHeader, "1"), 401, "missing bearer token")
	// The test server has no LLM configured.
	expectError(t, do(t, srv, "POST", "/api/v1/assistant/chat", body), 503, "not configured")
	expectError(t, do(t, srv, "GET", "/api/v1/assistant/chat", ""), 405, "not allowed")
}

func TestLoanRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	body := `{"category":"expenses","name":"Mortgage","amount":2500,"frequency":"monthly","startYear":2026,"startMonth":9,
		"loan":{"kind":"mortgage","balance":400000,"interestRate":0.065,"asOfYear":2026,"asOfMonth":9,"homeValue":750000},
		"createdAt":"2026-01-01T00:00:00.000Z"}`
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/home", body), 201)
	r := do(t, srv, "GET", "/api/v1/items/home", "")
	expectStatus(t, r, 200)
	loan, _ := r.json(t)["loan"].(map[string]any)
	if loan["kind"] != "mortgage" || loan["balance"] != 400000.0 || loan["interestRate"] != 0.065 ||
		loan["asOfMonth"] != 9.0 || loan["homeValue"] != 750000.0 {
		t.Errorf("loan = %v", loan)
	}
	// Turning it back into a plain expense clears the loan.
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/home",
		`{"category":"expenses","name":"Mortgage","amount":2500,"frequency":"monthly","createdAt":"2026-01-01T00:00:00.000Z"}`), 200)
	if got := do(t, srv, "GET", "/api/v1/items/home", "").json(t)["loan"]; got != nil {
		t.Errorf("loan after clearing = %v", got)
	}
}

func TestSnapshots(t *testing.T) {
	srv := newTestServer(t)
	body := `{"year":2026,"month":9,"netWorth":-12000.5,"assets":388000,"debts":400000.5,"monthlyIncome":11333.33,"monthlyExpenses":5800,"createdAt":"2026-09-01T00:00:00.000Z"}`
	expectStatus(t, do(t, srv, "PUT", "/api/v1/snapshots/2026-09", body), 201)
	expectStatus(t, do(t, srv, "PUT", "/api/v1/snapshots/2026-09", strings.Replace(body, "11333.33", "12000", 1)), 200)
	r := do(t, srv, "GET", "/api/v1/snapshots", "")
	expectStatus(t, r, 200)
	list := r.json(t)
	snaps, _ := list["snapshots"].([]any)
	if len(snaps) != 1 {
		t.Fatalf("list = %v", list)
	}
	if s := snaps[0].(map[string]any); s["id"] != "2026-09" || s["netWorth"] != -12000.5 || s["monthlyIncome"] != 12000.0 {
		t.Errorf("snapshot = %v", s)
	}
	expectError(t, do(t, srv, "PUT", "/api/v1/snapshots/2026-08", body), 400, "id must be the snapshot's month, 2026-09")

	// Deleted months stay deleted, like other synced records.
	expectStatus(t, do(t, srv, "DELETE", "/api/v1/snapshots/2026-09", ""), 204)
	expectError(t, do(t, srv, "PUT", "/api/v1/snapshots/2026-09", body), 410, "snapshot was deleted")
	if ids := do(t, srv, "GET", "/api/v1/snapshots", "").json(t)["deletedIds"]; len(ids.([]any)) != 1 {
		t.Errorf("deletedIds = %v", ids)
	}
}

func TestScenarioLayers(t *testing.T) {
	srv := newTestServer(t)
	// A scenario's copy of a Main item.
	expectStatus(t, do(t, srv, "PUT", "/api/v1/items/rent-b",
		`{"category":"expenses","name":"Rent","amount":1800,"frequency":"monthly","scenarioId":"plan-b","replaces":"rent","createdAt":"2026-01-01T00:00:00.000Z"}`), 201)
	got := do(t, srv, "GET", "/api/v1/items/rent-b", "").json(t)
	if got["scenarioId"] != "plan-b" || got["replaces"] != "rent" {
		t.Errorf("item = %v", got)
	}

	scenario := func(extra string) string {
		return `{"name":"Plan B","itemIds":[],` + extra + `"events":[],"parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0},"createdAt":"2026-01-01T00:00:00.000Z"}`
	}
	expectStatus(t, do(t, srv, "PUT", "/api/v1/scenarios/plan-b", scenario(`"excludedItemIds":["gym"],`)), 201)
	if ids := do(t, srv, "GET", "/api/v1/scenarios/plan-b", "").json(t)["excludedItemIds"]; fmt.Sprint(ids) != "[gym]" {
		t.Errorf("excludedItemIds = %v", ids)
	}
	// Empty stays empty; left out stays unset (a scenario from before layers).
	expectStatus(t, do(t, srv, "PUT", "/api/v1/scenarios/plan-b", scenario(`"excludedItemIds":[],`)), 200)
	if ids, ok := do(t, srv, "GET", "/api/v1/scenarios/plan-b", "").json(t)["excludedItemIds"]; !ok || fmt.Sprint(ids) != "[]" {
		t.Errorf("empty excludedItemIds = %v (present %v)", ids, ok)
	}
	expectStatus(t, do(t, srv, "PUT", "/api/v1/scenarios/plan-b", scenario(``)), 200)
	if ids, ok := do(t, srv, "GET", "/api/v1/scenarios/plan-b", "").json(t)["excludedItemIds"]; ok {
		t.Errorf("unset excludedItemIds came back as %v", ids)
	}
}
