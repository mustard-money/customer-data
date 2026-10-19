// Package assistant answers questions about a customer's finances and
// proposes changes to them, using an LLM. The app sends the conversation and
// a snapshot of the data on the device; the model replies with text plus
// proposed actions, which are validated with the same rules as the data
// endpoints. Nothing is stored or changed here: the app shows the actions
// and applies the ones the user accepts.
package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mustard-money/customer-data/account"
	"github.com/mustard-money/customer-data/fields"
	"github.com/mustard-money/customer-data/items"
	"github.com/mustard-money/customer-data/llm"
	"github.com/mustard-money/customer-data/profiles"
)

// Limits on a chat request.
const (
	MaxMessages       = 40
	MaxMessageLength  = 4000
	MaxContextRecords = 500
)

// Action types.
const (
	CreateItem    = "createItem"
	UpdateItem    = "updateItem"
	DeleteItem    = "deleteItem"
	CreateMember  = "createMember"
	UpdateMember  = "updateMember"
	DeleteMember  = "deleteMember"
	UpdateProfile = "updateProfile"
)

// Message is one turn of the conversation as the app sends it.
type Message struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// Context is the customer's data on the device when they asked.
type Context struct {
	// Today is the device's date (YYYY-MM-DD), for questions about "now".
	Today string `json:"today"`
	// Items are income, investments and expenses; Household are household
	// members; Profile is the account profile. Records use the API's shapes.
	Items     []map[string]any `json:"items"`
	Household []map[string]any `json:"household"`
	Profile   map[string]any   `json:"profile"`
}

// Request is the chat request body.
type Request struct {
	Messages []Message `json:"messages"`
	Context  Context   `json:"context"`
}

// Action is a proposed change. Fields are complete and validated: for
// creates and updates, the whole record as it would be saved (without id or
// timestamps); updates list every field, so missing ones should be cleared.
type Action struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Response is the chat response body.
type Response struct {
	Reply   string   `json:"reply"`
	Actions []Action `json:"actions"`
	// Dropped counts proposed actions that failed validation and were left out.
	Dropped int `json:"dropped"`
}

// Assistant runs chats against an LLM.
type Assistant struct {
	client llm.Client
	now    func() time.Time
	// For traces (see trace.go).
	provider, model string
	traceContent    bool
}

// New returns an Assistant using client.
func New(client llm.Client, opts ...Option) *Assistant {
	a := &Assistant{client: client, now: time.Now}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Validate checks the request's shape and limits. Errors are *fields.ValidationError.
func (r Request) Validate() error {
	if len(r.Messages) == 0 {
		return fields.Invalid("messages is required")
	}
	if len(r.Messages) > MaxMessages {
		return fields.Invalid("messages must have at most %d entries", MaxMessages)
	}
	for i, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return fields.Invalid("messages[%d].role must be user or assistant", i)
		}
		if strings.TrimSpace(m.Content) == "" {
			return fields.Invalid("messages[%d].content must not be blank", i)
		}
		if len([]rune(m.Content)) > MaxMessageLength {
			return fields.Invalid("messages[%d].content must be at most %d characters", i, MaxMessageLength)
		}
	}
	if r.Messages[len(r.Messages)-1].Role != "user" {
		return fields.Invalid("the last message must be from the user")
	}
	if len(r.Context.Items) > MaxContextRecords || len(r.Context.Household) > MaxContextRecords {
		return fields.Invalid("context must have at most %d items and household members", MaxContextRecords)
	}
	return nil
}

// Chat asks the model for a reply and proposed actions. If the model's
// output isn't the expected JSON, it's asked once more; after that its text
// is returned as the reply with no actions.
func (a *Assistant) Chat(ctx context.Context, req Request) (resp Response, err error) {
	ctx, span := tracer().Start(ctx, "assistant.chat", trace.WithAttributes(
		attrMessages.Int(len(req.Messages)),
		attrItems.Int(len(req.Context.Items)),
		attrHousehold.Int(len(req.Context.Household)),
		attrHasProfile.Bool(req.Context.Profile != nil),
	))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "chat failed")
		} else {
			span.SetAttributes(attrActions.Int(len(resp.Actions)), attrDropped.Int(resp.Dropped))
		}
		span.End()
	}()

	contextJSON, err := json.Marshal(req.Context)
	if err != nil {
		return Response{}, err
	}
	msgs := []llm.Message{llm.System(systemPrompt + "\n\nThe user's data (JSON):\n" + string(contextJSON))}
	for _, m := range req.Messages {
		if m.Role == "user" {
			msgs = append(msgs, llm.User(m.Content))
		} else {
			msgs = append(msgs, llm.Assistant(m.Content))
		}
	}

	temperature := 0.2
	var raw string
	for attempt := 1; attempt <= 2; attempt++ {
		span.SetAttributes(attrAttempts.Int(attempt))
		model, err := a.chat(ctx, llm.Request{
			Messages:       msgs,
			Temperature:    &temperature,
			MaxTokens:      2000,
			ResponseSchema: &replySchema,
		})
		if err != nil {
			return Response{}, err
		}
		raw = model.Content
		if out, ok := parseOutput(raw); ok {
			return a.check(ctx, out, req.Context), nil
		}
		span.AddEvent("reply wasn't valid JSON; asking again", trace.WithAttributes(attrAttempts.Int(attempt)))
		msgs = append(msgs,
			llm.Assistant(raw),
			llm.User("That wasn't valid JSON in the required format. Reply again with only the JSON object."))
	}
	span.SetAttributes(attrFallback.Bool(true))
	return Response{Reply: strings.TrimSpace(raw), Actions: []Action{}}, nil
}

// chat calls the model, retrying once if the provider returns an error: local
// servers sometimes fail to parse a model's output on one attempt and succeed
// on the next. Each call gets its own span.
func (a *Assistant) chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	resp, err := a.call(ctx, req, false)
	if err == nil || ctx.Err() != nil {
		return resp, err
	}
	return a.call(ctx, req, true)
}

func (a *Assistant) call(ctx context.Context, req llm.Request, providerRetry bool) (*llm.Response, error) {
	ctx, span := a.startModelSpan(ctx, req, providerRetry)
	resp, err := a.client.Chat(ctx, req)
	a.endModelSpan(span, resp, err)
	return resp, err
}

// replySchema is the shape of modelOutput, sent as structured output so
// providers that support it constrain the model to valid JSON.
var replySchema = llm.Schema{Name: "assistant_reply", Schema: json.RawMessage(`{
	"type": "object",
	"properties": {
		"reply": {"type": "string"},
		"actions": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"type": {"type": "string", "enum": ["createItem", "updateItem", "deleteItem", "createMember", "updateMember", "deleteMember", "updateProfile"]},
					"id": {"type": "string"},
					"fields": {"type": "object"}
				},
				"required": ["type"]
			}
		}
	},
	"required": ["reply", "actions"]
}`)}

// modelOutput is the JSON the model is asked to produce.
type modelOutput struct {
	Reply   string `json:"reply"`
	Actions []struct {
		Type   string         `json:"type"`
		ID     string         `json:"id"`
		Fields map[string]any `json:"fields"`
	} `json:"actions"`
}

// parseOutput extracts the JSON object from the model's text, tolerating
// code fences or text around it.
func parseOutput(text string) (modelOutput, bool) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return modelOutput{}, false
	}
	var out modelOutput
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil || strings.TrimSpace(out.Reply) == "" {
		return modelOutput{}, false
	}
	return out, true
}

// check validates each proposed action against the context, keeping the
// valid ones in a normalized form.
func (a *Assistant) check(ctx context.Context, out modelOutput, c Context) Response {
	resp := Response{Reply: strings.TrimSpace(out.Reply), Actions: []Action{}}
	for i, proposed := range out.Actions {
		action, err := a.validate(proposed.Type, proposed.ID, proposed.Fields, c)
		a.traceAction(ctx, i, proposed.Type, proposed.ID, proposed.Fields, err)
		if err != nil {
			resp.Dropped++
			continue
		}
		resp.Actions = append(resp.Actions, action)
	}
	return resp
}

var errInvalid = errors.New("invalid action")

func (a *Assistant) validate(kind, id string, changes map[string]any, c Context) (Action, error) {
	switch kind {
	case CreateItem:
		saved, err := validateRecord(nil, changes, true, func(body []byte) (any, error) {
			var in items.ItemInput
			if err := strictDecode(body, &in); err != nil {
				return nil, err
			}
			return in.Validate("new")
		})
		return Action{Type: kind, Fields: saved}, err
	case UpdateItem:
		existing := find(c.Items, id)
		if existing == nil {
			return Action{}, errInvalid
		}
		saved, err := validateRecord(existing, changes, true, func(body []byte) (any, error) {
			var in items.ItemInput
			if err := strictDecode(body, &in); err != nil {
				return nil, err
			}
			return in.Validate(id)
		})
		return Action{Type: kind, ID: id, Fields: saved}, err
	case DeleteItem:
		if find(c.Items, id) == nil {
			return Action{}, errInvalid
		}
		return Action{Type: kind, ID: id}, nil
	case CreateMember, UpdateMember:
		var existing map[string]any
		pathID := "new"
		if kind == UpdateMember {
			if existing = find(c.Household, id); existing == nil {
				return Action{}, errInvalid
			}
			pathID = id
		} else {
			id = ""
		}
		saved, err := validateRecord(existing, changes, true, func(body []byte) (any, error) {
			var in profiles.ProfileInput
			if err := strictDecode(body, &in); err != nil {
				return nil, err
			}
			return in.Validate(pathID, a.now())
		})
		return Action{Type: kind, ID: id, Fields: saved}, err
	case DeleteMember:
		if find(c.Household, id) == nil {
			return Action{}, errInvalid
		}
		return Action{Type: kind, ID: id}, nil
	case UpdateProfile:
		saved, err := validateRecord(c.Profile, changes, false, func(body []byte) (any, error) {
			var in account.ProfileInput
			if err := strictDecode(body, &in); err != nil {
				return nil, err
			}
			return in.Validate()
		})
		delete(saved, "country")
		return Action{Type: kind, Fields: saved}, err
	}
	return Action{}, errInvalid
}

// Keys the app manages itself; the model may not set them.
var managedKeys = []string{"id", "createdAt", "updatedAt"}

// validateRecord overlays changes (null removes a field) on the existing
// record, validates the result with check, and returns the saved record's
// fields without the keys the app manages. Synced records (withCreatedAt)
// keep their createdAt, or get today's for a new one.
func validateRecord(
	existing, changes map[string]any,
	withCreatedAt bool,
	check func([]byte) (any, error),
) (map[string]any, error) {
	merged := map[string]any{}
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range changes {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}
	for _, k := range managedKeys {
		delete(merged, k)
	}
	if withCreatedAt {
		if createdAt, ok := existing["createdAt"]; ok {
			merged["createdAt"] = createdAt
		} else {
			merged["createdAt"] = time.Now().UTC().Format(time.RFC3339)
		}
	}
	body, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	saved, err := check(body)
	if err != nil {
		return nil, err
	}
	// Render the validated record the way the API does, then drop managed keys.
	rendered, err := json.Marshal(saved)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(rendered, &out); err != nil {
		return nil, err
	}
	for _, k := range managedKeys {
		delete(out, k)
	}
	return out, nil
}

func find(records []map[string]any, id string) map[string]any {
	for _, r := range records {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func strictDecode(body []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
