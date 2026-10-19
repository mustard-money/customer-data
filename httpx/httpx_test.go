package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := CORS([]string{"http://localhost:5173"})(next)

	t.Run("preflight allowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/items/x", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "PUT")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Errorf("Allow-Origin = %q", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, DELETE, OPTIONS" {
			t.Errorf("Allow-Methods = %q", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
			t.Errorf("Allow-Headers = %q", got)
		}
	})

	t.Run("preflight disallowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/items", nil)
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Allow-Origin = %q, want empty", got)
		}
	})

	t.Run("simple request passes through", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Errorf("Allow-Origin = %q", got)
		}
	})
}

func TestDecodeJSON(t *testing.T) {
	type body struct {
		Name   *string  `json:"name"`
		Amount *float64 `json:"amount"`
	}
	tests := []struct {
		in      string
		wantErr string
	}{
		{`{"name":"a","amount":1}`, ""},
		{``, "must not be empty"},
		{`{"name":`, "malformed JSON"},
		{`{"name":"a",}`, "malformed JSON"},
		{`{"nope":1}`, `unknown field "nope"`},
		{`{"amount":"1"}`, `field "amount" must be a number`},
		{`[]`, "must be a JSON object"},
		{`{} {}`, "single JSON object"},
		{`{"name":"` + strings.Repeat("a", 200) + `"}`, "must not be larger than 100 bytes"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tt.in))
		req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 100)
		var b body
		err := DecodeJSON(req, &b)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%q: unexpected error %v", tt.in, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%q: err = %v, want %q", tt.in, err, tt.wantErr)
		}
	}
}
