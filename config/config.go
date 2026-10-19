// Package config loads server configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultDatabaseURL        = "postgres://mustard:mustard@localhost:5432/mustard?sslmode=disable"
	DefaultPort               = 8080
	DefaultCORSAllowedOrigins = "http://localhost:5173,http://localhost:3000,http://localhost:8081"
)

// Config is the runtime configuration for the server.
type Config struct {
	DatabaseURL        string
	Port               int
	CORSAllowedOrigins []string
	// OAuth client IDs that Apple/Google ID tokens must be issued to. A
	// provider's sign-in endpoint is disabled when its list is empty.
	AppleClientIDs  []string
	GoogleClientIDs []string
	// The assistant's LLM. Provider is "openai" (any OpenAI-compatible API),
	// "lmstudio" (a local LM Studio server) or empty, which turns the
	// assistant off. Model is required for openai.
	LLMProvider string
	LLMBaseURL  string
	LLMAPIKey   string
	LLMModel    string
	// TraceAssistantContent (TRACE_ASSISTANT_CONTENT) adds the assistant's
	// prompts and replies, including the customer's messages and data, to
	// its trace spans. Off by default; for local debugging. Tracing itself is
	// configured with the standard OTEL_* variables (see package tracing).
	TraceAssistantContent bool
}

// Addr returns the listen address for the HTTP server.
func (c Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL: getenvDefault(getenv, "DATABASE_URL", DefaultDatabaseURL),
		Port:        DefaultPort,
	}

	if v := strings.TrimSpace(getenv("PORT")); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("invalid PORT %q: must be an integer between 1 and 65535", v)
		}
		cfg.Port = port
	}

	cfg.CORSAllowedOrigins = splitList(getenvDefault(getenv, "CORS_ALLOWED_ORIGINS", DefaultCORSAllowedOrigins))
	cfg.AppleClientIDs = splitList(getenv("APPLE_CLIENT_IDS"))
	cfg.GoogleClientIDs = splitList(getenv("GOOGLE_CLIENT_IDS"))
	cfg.LLMProvider = strings.ToLower(strings.TrimSpace(getenv("LLM_PROVIDER")))
	cfg.LLMBaseURL = strings.TrimSpace(getenv("LLM_BASE_URL"))
	cfg.LLMAPIKey = strings.TrimSpace(getenv("LLM_API_KEY"))
	cfg.LLMModel = strings.TrimSpace(getenv("LLM_MODEL"))
	switch cfg.LLMProvider {
	case "", "lmstudio":
	case "openai":
		if cfg.LLMModel == "" {
			return Config{}, fmt.Errorf("LLM_MODEL is required when LLM_PROVIDER is openai")
		}
	default:
		return Config{}, fmt.Errorf("LLM_PROVIDER must be openai, lmstudio or empty, got %q", cfg.LLMProvider)
	}
	if v := strings.TrimSpace(getenv("TRACE_ASSISTANT_CONTENT")); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid TRACE_ASSISTANT_CONTENT %q: must be true or false", v)
		}
		cfg.TraceAssistantContent = on
	}
	return cfg, nil
}

func getenvDefault(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
