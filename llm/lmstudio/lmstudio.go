// Package lmstudio implements llm.Client for a local LM Studio server.
//
// LM Studio serves an OpenAI-compatible API, so chat calls are delegated to
// the openai package with LM Studio's defaults: a localhost base URL and no
// API key. If Model is empty, LM Studio answers with the currently loaded
// model.
package lmstudio

import (
	"context"
	"net/http"

	"github.com/mustard-money/customer-data/llm"
	"github.com/mustard-money/customer-data/llm/openai"
)

// DefaultBaseURL is where LM Studio's local server listens by default.
const DefaultBaseURL = "http://localhost:1234/v1"

// Config configures a Client.
type Config struct {
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// APIKey is only needed if the LM Studio server has auth enabled.
	APIKey string
	// Model is used when a Request does not set one.
	Model string
	// HTTPClient defaults to a client with openai.DefaultTimeout.
	HTTPClient *http.Client
}

// Client is an LM Studio chat client.
type Client struct {
	inner *openai.Client
}

var _ llm.Client = (*Client)(nil)

// New returns a Client for cfg.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	return &Client{inner: openai.New(openai.Config{
		BaseURL:    cfg.BaseURL,
		APIKey:     cfg.APIKey,
		Model:      cfg.Model,
		HTTPClient: cfg.HTTPClient,
	})}
}

// Chat sends req to LM Studio's chat completions endpoint.
func (c *Client) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return c.inner.Chat(ctx, req)
}
