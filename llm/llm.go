// Package llm defines a provider-neutral interface for chat-style LLM calls.
// Concrete clients live in subpackages (openai, lmstudio); callers should
// depend on Client so the provider can be swapped by configuration.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Role identifies the author of a Message.
type Role string

// Role values.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single turn in a conversation.
type Message struct {
	Role    Role
	Content string
}

// System, User and Assistant build messages with the corresponding role.
func System(content string) Message    { return Message{Role: RoleSystem, Content: content} }
func User(content string) Message      { return Message{Role: RoleUser, Content: content} }
func Assistant(content string) Message { return Message{Role: RoleAssistant, Content: content} }

// Request is a chat completion request.
type Request struct {
	// Model overrides the client's default model when non-empty.
	Model    string
	Messages []Message
	// Optional sampling parameters; nil/zero means "provider default".
	Temperature *float64
	MaxTokens   int
	// ResponseSchema, when set, asks the provider to constrain the reply to
	// this JSON Schema (structured output). Providers that support it reply
	// with JSON matching the schema; the caller should still validate it.
	ResponseSchema *Schema
}

// Schema is a named JSON Schema for structured output.
type Schema struct {
	Name   string
	Schema json.RawMessage
}

// Usage reports token counts, when the provider returns them.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is the result of a chat completion.
type Response struct {
	// Content is the assistant's reply text.
	Content string
	// Model is the model that actually served the request.
	Model string
	// FinishReason is the provider's stop reason, e.g. "stop" or "length".
	FinishReason string
	Usage        Usage
}

// Client sends chat completion requests to an LLM provider.
type Client interface {
	Chat(ctx context.Context, req Request) (*Response, error)
}

// APIError is returned when the provider responds with a non-2xx status.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("llm: provider returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("llm: provider returned status %d: %s", e.StatusCode, e.Message)
}
