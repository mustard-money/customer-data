package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mustard-money/customer-data/llm"
)

// Spans for a chat (all no-ops unless the tracing package installed a provider):
//
//	POST /api/v1/assistant/chat     the request: status code, validation errors
//	  assistant.chat                message and data counts, re-asks, actions proposed/dropped,
//	                                and a "model proposed <type>" event per action the model
//	                                asked for: target id, accepted or why it was rejected
//	    chat <model>                each model call (GenAI conventions): model, tokens,
//	                                finish reason, provider retry, errors
//
// Spans never hold the conversation or the customer's data unless content
// tracing is turned on (WithContentTracing, TRACE_ASSISTANT_CONTENT): then
// each model call also records its prompt (system prompt with the data
// snapshot, and the messages) and the model's reply.
//
// The tracer is looked up on each use: one kept from package init would stay
// bound to the first provider installed.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/mustard-money/customer-data/assistant")
}

// OpenTelemetry GenAI attribute names (the Go semconv package doesn't have
// them yet; see https://opentelemetry.io/docs/specs/semconv/gen-ai/).
const (
	genAIOperationName      = attribute.Key("gen_ai.operation.name")
	genAIProviderName       = attribute.Key("gen_ai.provider.name")
	genAIRequestModel       = attribute.Key("gen_ai.request.model")
	genAIRequestTemperature = attribute.Key("gen_ai.request.temperature")
	genAIRequestMaxTokens   = attribute.Key("gen_ai.request.max_tokens")
	genAIResponseModel      = attribute.Key("gen_ai.response.model")
	genAIFinishReasons      = attribute.Key("gen_ai.response.finish_reasons")
	genAIInputTokens        = attribute.Key("gen_ai.usage.input_tokens")
	genAIOutputTokens       = attribute.Key("gen_ai.usage.output_tokens")
	genAIInputMessages      = attribute.Key("gen_ai.input.messages")  // content tracing only
	genAIOutputMessages     = attribute.Key("gen_ai.output.messages") // content tracing only
	errorType               = attribute.Key("error.type")
)

// Mustard's own attributes.
const (
	attrMessages      = attribute.Key("mustard.assistant.messages")
	attrItems         = attribute.Key("mustard.assistant.context.items")
	attrHousehold     = attribute.Key("mustard.assistant.context.household")
	attrHasProfile    = attribute.Key("mustard.assistant.context.profile")
	attrAttempts      = attribute.Key("mustard.assistant.attempts")
	attrActions       = attribute.Key("mustard.assistant.actions")
	attrDropped       = attribute.Key("mustard.assistant.dropped")
	attrFallback      = attribute.Key("mustard.assistant.fallback_reply")
	attrProviderRetry = attribute.Key("mustard.llm.provider_retry")
	attrPromptChars   = attribute.Key("mustard.llm.prompt_chars")
	attrReplyChars    = attribute.Key("mustard.llm.reply_chars")
	// On each "model proposed <type>" event.
	attrActionIndex    = attribute.Key("mustard.assistant.action.index")
	attrActionType     = attribute.Key("mustard.assistant.action.type")
	attrActionTarget   = attribute.Key("mustard.assistant.action.id")
	attrActionAccepted = attribute.Key("mustard.assistant.action.accepted")
	attrActionReason   = attribute.Key("mustard.assistant.action.rejected_because")
	attrActionFields   = attribute.Key("mustard.assistant.action.fields") // content tracing only
)

// Option configures an Assistant.
type Option func(*Assistant)

// WithModelInfo names the provider ("openai", "lmstudio") and default model
// on the model-call spans.
func WithModelInfo(provider, model string) Option {
	return func(a *Assistant) { a.provider, a.model = provider, model }
}

// WithContentTracing records prompts and replies on the model-call spans.
// They include the customer's messages and data: for local debugging only.
func WithContentTracing(on bool) Option {
	return func(a *Assistant) { a.traceContent = on }
}

// startModelSpan opens the span for one model call.
func (a *Assistant) startModelSpan(ctx context.Context, req llm.Request, providerRetry bool) (context.Context, trace.Span) {
	model := req.Model
	if model == "" {
		model = a.model
	}
	name := "chat"
	if model != "" {
		name += " " + model
	}
	attrs := []attribute.KeyValue{
		genAIOperationName.String("chat"),
		attrProviderRetry.Bool(providerRetry),
		attrPromptChars.Int(promptChars(req.Messages)),
	}
	if a.provider != "" {
		attrs = append(attrs, genAIProviderName.String(a.provider))
	}
	if model != "" {
		attrs = append(attrs, genAIRequestModel.String(model))
	}
	if req.Temperature != nil {
		attrs = append(attrs, genAIRequestTemperature.Float64(*req.Temperature))
	}
	if req.MaxTokens > 0 {
		attrs = append(attrs, genAIRequestMaxTokens.Int(req.MaxTokens))
	}
	if a.traceContent {
		attrs = append(attrs, genAIInputMessages.String(messagesJSON(req.Messages)))
	}
	return tracer().Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
}

// endModelSpan records a model call's result and ends its span.
func (a *Assistant) endModelSpan(span trace.Span, resp *llm.Response, err error) {
	defer span.End()
	if err != nil {
		var apiErr *llm.APIError
		if errors.As(err, &apiErr) {
			span.SetAttributes(errorType.String(strconv.Itoa(apiErr.StatusCode)))
		} else {
			span.SetAttributes(errorType.String("_OTHER"))
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "model call failed")
		return
	}
	span.SetAttributes(
		genAIResponseModel.String(resp.Model),
		attrReplyChars.Int(len([]rune(resp.Content))),
	)
	// Only when the provider reported usage; zeros would read as real counts.
	if u := resp.Usage; u.PromptTokens > 0 || u.CompletionTokens > 0 {
		span.SetAttributes(genAIInputTokens.Int(u.PromptTokens), genAIOutputTokens.Int(u.CompletionTokens))
	}
	if resp.FinishReason != "" {
		span.SetAttributes(genAIFinishReasons.StringSlice([]string{resp.FinishReason}))
	}
	if a.traceContent {
		span.SetAttributes(genAIOutputMessages.String(messagesJSON([]llm.Message{llm.Assistant(resp.Content)})))
	}
}

// traceAction records one action the model proposed for the turn as an event
// on the chat span: its type, the record it targets and whether it passed
// validation (with the reason when it didn't; validation messages name
// fields and limits, not values). The proposed fields are the customer's
// data, so they're recorded only with content tracing.
func (a *Assistant) traceAction(ctx context.Context, index int, kind, id string, fields map[string]any, err error) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attrActionIndex.Int(index),
		attrActionType.String(kind),
		attrActionAccepted.Bool(err == nil),
	}
	if id != "" {
		attrs = append(attrs, attrActionTarget.String(id))
	}
	if err != nil {
		attrs = append(attrs, attrActionReason.String(truncate(err.Error(), 300)))
	}
	if a.traceContent && fields != nil {
		if b, jsonErr := json.Marshal(fields); jsonErr == nil {
			attrs = append(attrs, attrActionFields.String(string(b)))
		}
	}
	span.AddEvent("model proposed "+kind, trace.WithAttributes(attrs...))
}

func truncate(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func promptChars(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len([]rune(m.Content))
	}
	return n
}

// messagesJSON renders messages in the GenAI conventions' shape:
// [{"role": "user", "parts": [{"type": "text", "content": "..."}]}].
func messagesJSON(msgs []llm.Message) string {
	type part struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	type message struct {
		Role  string `json:"role"`
		Parts []part `json:"parts"`
	}
	out := make([]message, len(msgs))
	for i, m := range msgs {
		out[i] = message{Role: string(m.Role), Parts: []part{{Type: "text", Content: m.Content}}}
	}
	b, _ := json.Marshal(out)
	return string(b)
}
