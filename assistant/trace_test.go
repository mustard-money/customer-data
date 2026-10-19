package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mustard-money/customer-data/llm"
)

// recordSpans installs a tracer provider that keeps finished spans in memory.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return rec
}

func spanNamed(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no span %q", name)
	return nil
}

func attrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

func eventAttrs(e sdktrace.Event) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range e.Attributes {
		m[kv.Key] = kv.Value
	}
	return m
}

// usageLLM answers with a model name and token counts, like a real provider.
type usageLLM struct{ fakeLLM }

func (f *usageLLM) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	resp, err := f.fakeLLM.Chat(ctx, req)
	if resp != nil {
		resp.Model, resp.FinishReason = "stand-in-1", "stop"
		resp.Usage = llm.Usage{PromptTokens: 812, CompletionTokens: 37}
	}
	return resp, err
}

const secret = "my salary is 123456"

func runTracedChat(t *testing.T, opts ...Option) []sdktrace.ReadOnlySpan {
	t.Helper()
	rec := recordSpans(t)
	fake := &usageLLM{fakeLLM{replies: []string{
		"not json",
		`{"reply": "Done.", "actions": [{"type": "deleteItem", "id": "gym"}, {"type": "deleteItem", "id": "nope"}]}`,
	}}}
	a := New(fake, append([]Option{WithModelInfo("lmstudio", "stand-in")}, opts...)...)
	body, _ := json.Marshal(Request{Messages: []Message{{Role: "user", Content: secret}}, Context: testContext})
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	NewHandler(a).Register(mux)
	mux.ServeHTTP(w, httptest.NewRequest("POST", route, strings.NewReader(string(body))))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	return rec.Ended()
}

func TestTraceRecordsTheChatWithoutContent(t *testing.T) {
	spans := runTracedChat(t)

	request := spanNamed(t, spans, "POST "+route)
	if got := attrs(request)["http.response.status_code"].AsInt64(); got != 200 {
		t.Errorf("status code = %d", got)
	}

	chat := spanNamed(t, spans, "assistant.chat")
	a := attrs(chat)
	if a[attrMessages].AsInt64() != 1 || a[attrItems].AsInt64() != 2 || a[attrHousehold].AsInt64() != 1 ||
		!a[attrHasProfile].AsBool() || a[attrAttempts].AsInt64() != 2 ||
		a[attrActions].AsInt64() != 1 || a[attrDropped].AsInt64() != 1 {
		t.Errorf("assistant.chat attributes = %v", a)
	}
	if chat.Parent().SpanID() != request.SpanContext().SpanID() {
		t.Error("assistant.chat isn't a child of the request span")
	}
	// The re-ask, then one event per action the model proposed.
	events := chat.Events()
	if len(events) != 3 || !strings.Contains(events[0].Name, "valid JSON") {
		t.Fatalf("events = %v", events)
	}
	accepted, rejected := eventAttrs(events[1]), eventAttrs(events[2])
	if events[1].Name != "model proposed deleteItem" || accepted[attrActionTarget].AsString() != "gym" ||
		!accepted[attrActionAccepted].AsBool() {
		t.Errorf("accepted action event = %s %v", events[1].Name, accepted)
	}
	if rejected[attrActionTarget].AsString() != "nope" || rejected[attrActionAccepted].AsBool() ||
		rejected[attrActionReason].AsString() == "" {
		t.Errorf("rejected action event = %v", rejected)
	}
	for _, e := range events {
		if _, ok := eventAttrs(e)[attrActionFields]; ok {
			t.Errorf("event %q has the action's fields without content tracing", e.Name)
		}
	}

	var calls int
	for _, s := range spans {
		if s.Name() != "chat stand-in" {
			continue
		}
		calls++
		m := attrs(s)
		if m[genAIProviderName].AsString() != "lmstudio" || m[genAIResponseModel].AsString() != "stand-in-1" ||
			m[genAIInputTokens].AsInt64() != 812 || m[genAIOutputTokens].AsInt64() != 37 {
			t.Errorf("model call attributes = %v", m)
		}
		if s.Parent().SpanID() != chat.SpanContext().SpanID() {
			t.Error("model call isn't a child of assistant.chat")
		}
	}
	if calls != 2 {
		t.Errorf("model call spans = %d, want 2 (the reply wasn't JSON the first time)", calls)
	}

	// Nothing the customer wrote, and none of their data, is in any span.
	for _, s := range spans {
		for _, kv := range s.Attributes() {
			v := kv.Value.Emit()
			if strings.Contains(v, secret) || strings.Contains(v, "94103") || strings.Contains(v, "Salary") {
				t.Errorf("span %q attribute %s holds conversation or data: %q", s.Name(), kv.Key, v)
			}
		}
		if _, ok := attrs(s)[genAIInputMessages]; ok {
			t.Errorf("span %q has prompt content without content tracing", s.Name())
		}
	}
}

func TestTraceIncludesContentWhenAskedTo(t *testing.T) {
	spans := runTracedChat(t, WithContentTracing(true))
	var prompts, replies []string
	for _, s := range spans {
		if s.Name() == "chat stand-in" {
			prompts = append(prompts, attrs(s)[genAIInputMessages].AsString())
			replies = append(replies, attrs(s)[genAIOutputMessages].AsString())
		}
	}
	if len(prompts) != 2 || !strings.Contains(prompts[0], secret) || !strings.Contains(prompts[0], "94103") {
		t.Errorf("prompts = %v", prompts)
	}
	if len(replies) != 2 || !strings.Contains(replies[0], "not json") || !strings.Contains(replies[1], "Done.") {
		t.Errorf("replies = %v", replies)
	}
	var parsed []struct {
		Role  string `json:"role"`
		Parts []struct {
			Type, Content string
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(prompts[0]), &parsed); err != nil || parsed[0].Role != "system" || parsed[1].Parts[0].Content != secret {
		t.Errorf("prompt isn't in the GenAI message shape: %v %+v", err, parsed)
	}
}

func TestTraceMarksFailedModelCalls(t *testing.T) {
	rec := recordSpans(t)
	fake := &fakeLLM{err: &llm.APIError{StatusCode: 503}}
	_, err := New(fake, WithModelInfo("openai", "gpt")).Chat(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}}, Context: testContext,
	})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	var failed int
	for _, s := range rec.Ended() {
		if s.Name() == "chat gpt" {
			if s.Status().Code != codes.Error || attrs(s)[errorType].AsString() != "503" {
				t.Errorf("status = %v, attributes = %v", s.Status(), attrs(s))
			}
			if attrs(s)[attrProviderRetry].AsBool() {
				failed++ // the retry
			}
		}
	}
	if failed != 1 {
		t.Errorf("provider retries traced = %d, want 1", failed)
	}
	if s := spanNamed(t, rec.Ended(), "assistant.chat"); s.Status().Code != codes.Error {
		t.Errorf("assistant.chat status = %v", s.Status())
	}
}

func TestTraceRecordsProposedFieldsOnlyWithContent(t *testing.T) {
	create := `{"reply": "Added.", "actions": [{"type": "createItem", "fields": {"category": "expenses", "name": "Daycare", "amount": 1500, "frequency": "monthly"}}]}`
	for _, content := range []bool{false, true} {
		rec := recordSpans(t)
		_, err := New(&fakeLLM{replies: []string{create}}, WithContentTracing(content)).Chat(context.Background(),
			Request{Messages: []Message{{Role: "user", Content: "add daycare"}}, Context: testContext})
		if err != nil {
			t.Fatal(err)
		}
		events := spanNamed(t, rec.Ended(), "assistant.chat").Events()
		if len(events) != 1 || events[0].Name != "model proposed createItem" || !eventAttrs(events[0])[attrActionAccepted].AsBool() {
			t.Fatalf("content=%v: events = %v", content, events)
		}
		fields, ok := eventAttrs(events[0])[attrActionFields]
		if content != ok || (ok && !strings.Contains(fields.AsString(), "Daycare")) {
			t.Errorf("content=%v: fields attribute = %v (present %v)", content, fields.AsString(), ok)
		}
	}
}
