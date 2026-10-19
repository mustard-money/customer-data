package assistant

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/mustard-money/customer-data/fields"
	"github.com/mustard-money/customer-data/httpx"
)

// Timeout bounds one chat, which can mean two model calls. Longer than the
// server's usual write timeout, which the handler extends for this route.
const Timeout = 2 * time.Minute

// Handler serves POST /api/v1/assistant/chat.
type Handler struct {
	assistant *Assistant // nil when no LLM is configured
}

// NewHandler returns a Handler. A nil assistant answers 503 (not configured).
func NewHandler(a *Assistant) *Handler {
	return &Handler{assistant: a}
}

// Register mounts the assistant route on mux, which must be behind
// auth.RequireCustomer: only signed-in customers can use the model.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+route, h.chat)
	mux.HandleFunc(route, httpx.MethodNotAllowed("POST, OPTIONS"))
}

const route = "/api/v1/assistant/chat"

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	// The request's span (see trace.go), continuing the caller's trace if it sent one.
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := tracer().Start(ctx, "POST "+route, trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(semconv.HTTPRequestMethodPost, semconv.HTTPRoute(route)))
	defer span.End()
	r = r.WithContext(ctx)
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	w = sw
	defer func() {
		span.SetAttributes(semconv.HTTPResponseStatusCode(sw.status))
		if sw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(sw.status))
		}
	}()

	if h.assistant == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "the assistant is not configured on this server")
		return
	}
	var req Request
	if err := httpx.DecodeJSON(r, &req); err != nil {
		span.AddEvent("invalid request body")
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		var ve *fields.ValidationError
		if errors.As(err, &ve) {
			// Validation messages name fields and limits, never message text.
			span.AddEvent("invalid request", trace.WithAttributes(attribute.String("message", ve.Error())))
			httpx.WriteError(w, http.StatusBadRequest, ve.Error())
			return
		}
		httpx.WriteInternalError(w, r, err)
		return
	}

	// Model calls can outlast the server's default write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(Timeout + 10*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), Timeout)
	defer cancel()

	resp, err := h.assistant.Chat(ctx, req)
	if err != nil {
		// Logged without the conversation, which is the customer's private data.
		slog.WarnContext(r.Context(), "assistant: model call failed", "err", err)
		httpx.WriteError(w, http.StatusBadGateway, "the assistant couldn't reach its AI model; try again later")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// statusWriter remembers the status code written, for the request's span.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the underlying writer (the
// handler extends the write deadline through it).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
