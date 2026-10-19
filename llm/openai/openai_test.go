package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mustard-money/customer-data/llm"
)

func ptr[T any](v T) *T { return &v }

func TestChat(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "Bearer sk-test" {
			t.Errorf("Authorization = %q", h)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"model": "gpt-test-2026",
			"choices": [{"message": {"role": "assistant", "content": "hi there"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 7, "completion_tokens": 2, "total_tokens": 9}
		}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL + "/v1/", APIKey: "sk-test", Model: "gpt-test"})
	resp, err := c.Chat(context.Background(), llm.Request{
		Messages:    []llm.Message{llm.System("be brief"), llm.User("hello")},
		Temperature: ptr(0.2),
		MaxTokens:   50,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Model != "gpt-test" || got.MaxTokens != 50 || got.Temperature == nil || *got.Temperature != 0.2 {
		t.Errorf("request body = %+v", got)
	}
	if len(got.Messages) != 2 || got.Messages[0] != (chatMessage{"system", "be brief"}) || got.Messages[1] != (chatMessage{"user", "hello"}) {
		t.Errorf("messages = %+v", got.Messages)
	}
	want := llm.Response{Content: "hi there", Model: "gpt-test-2026", FinishReason: "stop", Usage: llm.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}}
	if *resp != want {
		t.Errorf("response = %+v, want %+v", *resp, want)
	}
}

func TestChatRequestModelOverridesDefault(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization header sent without API key")
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices": [{"message": {"content": "ok"}}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "default"})
	if _, err := c.Chat(context.Background(), llm.Request{Model: "override", Messages: []llm.Message{llm.User("x")}}); err != nil {
		t.Fatal(err)
	}
	if got.Model != "override" {
		t.Errorf("model = %q", got.Model)
	}
}

func TestChatAPIError(t *testing.T) {
	for _, tc := range []struct {
		body, want string
	}{
		{`{"error": {"message": "invalid api key", "type": "auth"}}`, "invalid api key"},
		{"upstream unavailable\n", "upstream unavailable"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(tc.body))
		}))
		_, err := New(Config{BaseURL: srv.URL}).Chat(context.Background(), llm.Request{Messages: []llm.Message{llm.User("x")}})
		srv.Close()

		var apiErr *llm.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *llm.APIError", err)
		}
		if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != tc.want {
			t.Errorf("APIError = %+v, want message %q", apiErr, tc.want)
		}
	}
}

func TestChatNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices": []}`))
	}))
	defer srv.Close()

	if _, err := New(Config{BaseURL: srv.URL}).Chat(context.Background(), llm.Request{Messages: []llm.Message{llm.User("x")}}); err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestChatNoMessages(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://unused.invalid"}).Chat(context.Background(), llm.Request{}); err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestNewDefaults(t *testing.T) {
	c := New(Config{})
	if c.baseURL != DefaultBaseURL || c.http == nil || c.http.Timeout != DefaultTimeout {
		t.Errorf("defaults not applied: baseURL=%q http=%v", c.baseURL, c.http)
	}
}

func TestChatResponseSchema(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices": [{"message": {"content": "{}"}}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), llm.Request{Messages: []llm.Message{llm.User("x")}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["response_format"]; ok {
		t.Errorf("response_format sent without a schema: %v", got)
	}

	schema := &llm.Schema{Name: "reply", Schema: json.RawMessage(`{"type":"object"}`)}
	if _, err := c.Chat(context.Background(), llm.Request{Messages: []llm.Message{llm.User("x")}, ResponseSchema: schema}); err != nil {
		t.Fatal(err)
	}
	rf, _ := json.Marshal(got["response_format"])
	if string(rf) != `{"json_schema":{"name":"reply","schema":{"type":"object"},"strict":false},"type":"json_schema"}` {
		t.Errorf("response_format = %s", rf)
	}
}
