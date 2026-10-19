package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mustard-money/customer-data/llm"
)

// fakeLLM returns canned replies in order and records the requests. It
// fails its first `failures` calls, or every call when err is set.
type fakeLLM struct {
	replies  []string
	err      error
	failures int
	requests []llm.Request
}

func (f *fakeLLM) Chat(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.failures > 0 {
		f.failures--
		return nil, errors.New("output didn't match the expected format")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return &llm.Response{Content: reply}, nil
}

var testContext = Context{
	Today: "2026-10-02",
	Items: []map[string]any{
		{"id": "salary", "category": "income", "name": "Salary", "amount": 6000.0, "frequency": "monthly",
			"incomeType": "w2", "endYear": 2040.0, "createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:00:00.000Z"},
		{"id": "gym", "category": "expenses", "name": "Gym", "amount": 50.0, "frequency": "monthly",
			"createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:00:00.000Z"},
	},
	Household: []map[string]any{
		{"id": "me", "name": "Alex", "relationship": "self", "createdAt": "2026-01-01T00:00:00.000Z"},
	},
	Profile: map[string]any{"country": "US", "postalCode": "94103", "filingStatus": "single", "updatedAt": "2026-01-01T00:00:00.000Z"},
}

func chat(t *testing.T, replies ...string) (Response, *fakeLLM) {
	t.Helper()
	fake := &fakeLLM{replies: replies}
	resp, err := New(fake).Chat(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "help"}},
		Context:  testContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp, fake
}

func TestChatProposesValidatedActions(t *testing.T) {
	resp, fake := chat(t, "```json\n"+`{"reply": " Added rent. ", "actions": [
		{"type": "createItem", "fields": {"category": "expenses", "name": " Rent ", "amount": 2000.004, "frequency": "monthly"}},
		{"type": "updateItem", "id": "salary", "fields": {"amount": 6500, "endYear": null}},
		{"type": "deleteItem", "id": "gym"},
		{"type": "createMember", "fields": {"name": "Sam", "relationship": "child", "birthYear": 2020}},
		{"type": "updateProfile", "fields": {"expectedReturn": 0.06}}
	]}`+"\n```")
	if resp.Reply != "Added rent." || resp.Dropped != 0 || len(resp.Actions) != 5 {
		t.Fatalf("resp = %+v", resp)
	}

	create := resp.Actions[0].Fields
	if create["name"] != "Rent" || create["amount"] != 2000.0 || create["id"] != nil || create["createdAt"] != nil {
		t.Errorf("create = %v", create)
	}
	// Updates return the whole record; null removed endYear.
	update := resp.Actions[1]
	if update.ID != "salary" || update.Fields["amount"] != 6500.0 || update.Fields["incomeType"] != "w2" {
		t.Errorf("update = %+v", update)
	}
	if _, ok := update.Fields["endYear"]; ok {
		t.Errorf("endYear should be removed: %v", update.Fields)
	}
	if resp.Actions[2].ID != "gym" || resp.Actions[3].Fields["relationship"] != "child" {
		t.Errorf("actions = %+v", resp.Actions)
	}
	profile := resp.Actions[4].Fields
	if profile["expectedReturn"] != 0.06 || profile["postalCode"] != "94103" || profile["country"] != nil {
		t.Errorf("profile = %v", profile)
	}

	// The data and conversation reach the model.
	system := fake.requests[0].Messages[0].Content
	if !strings.Contains(system, `"name":"Salary"`) || !strings.Contains(system, "2026-10-02") {
		t.Errorf("system prompt lacks the user's data")
	}
	if last := fake.requests[0].Messages[1]; last.Role != llm.RoleUser || last.Content != "help" {
		t.Errorf("user message = %+v", last)
	}
}

func TestChatDropsInvalidActions(t *testing.T) {
	resp, _ := chat(t, `{"reply": "Done.", "actions": [
		{"type": "createItem", "fields": {"category": "savings", "name": "x", "amount": 1}},
		{"type": "createItem", "fields": {"category": "expenses", "name": "x", "amount": 1, "frequency": "monthly", "color": "red"}},
		{"type": "updateItem", "id": "missing", "fields": {"amount": 1}},
		{"type": "deleteMember", "id": "nobody"},
		{"type": "updateProfile", "fields": {"postalCode": "ABCDE"}},
		{"type": "launchRocket"},
		{"type": "deleteItem", "id": "gym"}
	]}`)
	if resp.Dropped != 6 || len(resp.Actions) != 1 || resp.Actions[0].Type != DeleteItem {
		t.Errorf("resp = %+v", resp)
	}
}

func TestChatRetriesOnceWhenNotJSON(t *testing.T) {
	resp, fake := chat(t, "Sure! Here's the plan.", `{"reply": "Here it is.", "actions": []}`)
	if resp.Reply != "Here it is." || len(fake.requests) != 2 {
		t.Errorf("resp = %+v, calls = %d", resp, len(fake.requests))
	}
	resp, _ = chat(t, "not json", "still not json")
	if resp.Reply != "still not json" || len(resp.Actions) != 0 {
		t.Errorf("fallback = %+v", resp)
	}
}

func TestChatRetriesProviderErrorsOnce(t *testing.T) {
	fake := &fakeLLM{failures: 1, replies: []string{`{"reply": "Hi.", "actions": []}`}}
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}, Context: testContext}
	resp, err := New(fake).Chat(context.Background(), req)
	if err != nil || resp.Reply != "Hi." || len(fake.requests) != 2 {
		t.Errorf("resp = %+v, err = %v, calls = %d", resp, err, len(fake.requests))
	}
	// Requests ask for structured output.
	if fake.requests[0].ResponseSchema == nil || fake.requests[0].ResponseSchema.Name != "assistant_reply" {
		t.Errorf("schema = %+v", fake.requests[0].ResponseSchema)
	}
	// Two failures in a row are reported.
	fake = &fakeLLM{failures: 2}
	if _, err := New(fake).Chat(context.Background(), req); err == nil || len(fake.requests) != 2 {
		t.Errorf("err = %v, calls = %d", err, len(fake.requests))
	}
}

func TestRequestValidate(t *testing.T) {
	ok := Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", MaxMessageLength+1)
	for name, req := range map[string]Request{
		"no messages":     {},
		"bad role":        {Messages: []Message{{Role: "system", Content: "hi"}}},
		"blank":           {Messages: []Message{{Role: "user", Content: " "}}},
		"too long":        {Messages: []Message{{Role: "user", Content: long}}},
		"ends with reply": {Messages: []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yo"}}},
	} {
		if err := req.Validate(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestHandler(t *testing.T) {
	serve := func(h *Handler, body string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		h.Register(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/assistant/chat", strings.NewReader(body)))
		return w
	}
	const body = `{"messages":[{"role":"user","content":"hi"}],"context":{"today":"2026-10-02","items":[],"household":[],"profile":null}}`

	if w := serve(NewHandler(nil), body); w.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured: %d", w.Code)
	}
	if w := serve(NewHandler(New(&fakeLLM{err: errors.New("down")})), body); w.Code != http.StatusBadGateway {
		t.Errorf("model down: %d", w.Code)
	}
	if w := serve(NewHandler(New(&fakeLLM{})), `{"messages":[]}`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid: %d", w.Code)
	}
	w := serve(NewHandler(New(&fakeLLM{replies: []string{`{"reply":"Hello!","actions":[]}`}})), body)
	var got Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Reply != "Hello!" || got.Actions == nil {
		t.Errorf("ok: %d %s", w.Code, w.Body)
	}
}
