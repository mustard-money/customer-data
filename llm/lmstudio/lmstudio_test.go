package lmstudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mustard-money/customer-data/llm"
)

func TestChat(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected Authorization header")
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"model": "qwen3-8b", "choices": [{"message": {"content": "local hi"}, "finish_reason": "stop"}]}`))
	}))
	defer srv.Close()

	resp, err := New(Config{BaseURL: srv.URL + "/v1"}).Chat(context.Background(), llm.Request{Messages: []llm.Message{llm.User("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := body["model"]; ok {
		t.Errorf("model sent although none configured: %v", body["model"])
	}
	if resp.Content != "local hi" || resp.Model != "qwen3-8b" {
		t.Errorf("response = %+v", resp)
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	// The inner client is opaque, so check the default by pointing a
	// transport at it and recording the URL it dials.
	var gotURL string
	c := New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return nil, context.Canceled
	})}})
	c.Chat(context.Background(), llm.Request{Messages: []llm.Message{llm.User("x")}})
	if gotURL != DefaultBaseURL+"/chat/completions" {
		t.Errorf("URL = %q", gotURL)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
