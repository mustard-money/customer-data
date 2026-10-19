// Package openai implements llm.Client against the OpenAI Chat Completions
// API. It works with any server that speaks the same wire format (OpenAI,
// Azure-style proxies, vLLM, LM Studio, ...) by pointing BaseURL at it.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mustard-money/customer-data/llm"
)

// DefaultBaseURL is the OpenAI API root.
const DefaultBaseURL = "https://api.openai.com/v1"

// DefaultTimeout bounds a request when Config.HTTPClient is nil.
const DefaultTimeout = 2 * time.Minute

// maxErrorBody caps how much of an error response is read into APIError.
const maxErrorBody = 64 << 10

// Config configures a Client.
type Config struct {
	// BaseURL is the API root, e.g. "https://api.openai.com/v1". Defaults to
	// DefaultBaseURL.
	BaseURL string
	// APIKey is sent as a Bearer token. Leave empty for servers that do not
	// require auth.
	APIKey string
	// Model is used when a Request does not set one.
	Model string
	// HTTPClient defaults to a client with DefaultTimeout.
	HTTPClient *http.Client
}

// Client is an OpenAI-compatible chat completions client.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

var _ llm.Client = (*Client)(nil)

// New returns a Client for cfg.
func New(cfg Config) *Client {
	c := &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		http:    cfg.HTTPClient,
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: DefaultTimeout}
	}
	return c
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model,omitempty"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// responseFormat requests structured output. Strict is off: schemas with
// free-form objects aren't allowed in strict mode, and the caller validates
// the reply anyway.
type responseFormat struct {
	Type       string `json:"type"`
	JSONSchema struct {
		Name   string          `json:"name"`
		Strict bool            `json:"strict"`
		Schema json.RawMessage `json:"schema"`
	} `json:"json_schema"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type errorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends req to the /chat/completions endpoint.
func (c *Client) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if len(req.Messages) == 0 {
		return nil, errors.New("llm/openai: request has no messages")
	}

	body := chatRequest{
		Model:       req.Model,
		Messages:    make([]chatMessage, len(req.Messages)),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if body.Model == "" {
		body.Model = c.model
	}
	if req.ResponseSchema != nil {
		body.ResponseFormat = &responseFormat{Type: "json_schema"}
		body.ResponseFormat.JSONSchema.Name = req.ResponseSchema.Name
		body.ResponseFormat.JSONSchema.Schema = req.ResponseSchema.Schema
	}
	for i, m := range req.Messages {
		body.Messages[i] = chatMessage{Role: string(m.Role), Content: m.Content}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm/openai: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("llm/openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm/openai: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, apiError(resp)
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("llm/openai: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("llm/openai: response has no choices")
	}
	choice := out.Choices[0]
	return &llm.Response{
		Content:      choice.Message.Content,
		Model:        out.Model,
		FinishReason: choice.FinishReason,
		Usage: llm.Usage{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
		},
	}, nil
}

// apiError builds an *llm.APIError from a non-2xx response, preferring the
// OpenAI-style {"error": {"message": ...}} body and falling back to raw text.
func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	msg := strings.TrimSpace(string(raw))
	var er errorResponse
	if json.Unmarshal(raw, &er) == nil && er.Error.Message != "" {
		msg = er.Error.Message
	}
	return &llm.APIError{StatusCode: resp.StatusCode, Message: msg}
}
