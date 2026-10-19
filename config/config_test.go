package config

import (
	"reflect"
	"testing"
)

func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(envFunc(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != DefaultDatabaseURL {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.Port != 8080 || cfg.Addr() != ":8080" {
		t.Errorf("Port = %d, Addr = %q", cfg.Port, cfg.Addr())
	}
	want := []string{"http://localhost:5173", "http://localhost:3000", "http://localhost:8081"}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, want) {
		t.Errorf("CORSAllowedOrigins = %v", cfg.CORSAllowedOrigins)
	}
	if cfg.AppleClientIDs != nil || cfg.GoogleClientIDs != nil {
		t.Errorf("client IDs = %v, %v; want none", cfg.AppleClientIDs, cfg.GoogleClientIDs)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := load(envFunc(map[string]string{
		"DATABASE_URL":         "postgres://x@y/z",
		"PORT":                 "9090",
		"CORS_ALLOWED_ORIGINS": " https://a.example , ,https://b.example",
		"APPLE_CLIENT_IDS":     "money.mustard.app",
		"GOOGLE_CLIENT_IDS":    "web.apps.googleusercontent.com, ios.apps.googleusercontent.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://x@y/z" || cfg.Port != 9090 {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, []string{"https://a.example", "https://b.example"}) {
		t.Errorf("CORSAllowedOrigins = %v", cfg.CORSAllowedOrigins)
	}
	if !reflect.DeepEqual(cfg.AppleClientIDs, []string{"money.mustard.app"}) {
		t.Errorf("AppleClientIDs = %v", cfg.AppleClientIDs)
	}
	if !reflect.DeepEqual(cfg.GoogleClientIDs, []string{"web.apps.googleusercontent.com", "ios.apps.googleusercontent.com"}) {
		t.Errorf("GoogleClientIDs = %v", cfg.GoogleClientIDs)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	for _, p := range []string{"abc", "0", "70000"} {
		if _, err := load(envFunc(map[string]string{"PORT": p})); err == nil {
			t.Errorf("PORT=%q: expected error", p)
		}
	}
}

func TestLoadLLM(t *testing.T) {
	cfg, err := load(envFunc(map[string]string{"LLM_PROVIDER": " OpenAI ", "LLM_MODEL": "gpt-test", "LLM_API_KEY": "k"}))
	if err != nil || cfg.LLMProvider != "openai" || cfg.LLMModel != "gpt-test" || cfg.LLMAPIKey != "k" {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
	if cfg, err := load(envFunc(map[string]string{})); err != nil || cfg.LLMProvider != "" {
		t.Errorf("default: %+v, %v", cfg, err)
	}
	if _, err := load(envFunc(map[string]string{"LLM_PROVIDER": "lmstudio"})); err != nil {
		t.Errorf("lmstudio without a model: %v", err)
	}
	for name, env := range map[string]map[string]string{
		"unknown provider":     {"LLM_PROVIDER": "skynet"},
		"openai without model": {"LLM_PROVIDER": "openai"},
	} {
		if _, err := load(envFunc(env)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadTraceAssistantContent(t *testing.T) {
	if cfg, err := load(envFunc(nil)); err != nil || cfg.TraceAssistantContent {
		t.Errorf("default: cfg = %+v, err = %v (content tracing must be off unless asked for)", cfg, err)
	}
	if cfg, err := load(envFunc(map[string]string{"TRACE_ASSISTANT_CONTENT": "true"})); err != nil || !cfg.TraceAssistantContent {
		t.Errorf("true: cfg = %+v, err = %v", cfg, err)
	}
	if _, err := load(envFunc(map[string]string{"TRACE_ASSISTANT_CONTENT": "yes please"})); err == nil {
		t.Error("expected an error for an invalid value")
	}
}
