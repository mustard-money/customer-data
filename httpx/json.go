// Package httpx holds small HTTP helpers shared by handlers: JSON encoding,
// error responses, CORS and middleware.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// ErrorResponse is the JSON body for every error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "err", err)
	}
}

// WriteError writes {"error": msg} with the given status code.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, ErrorResponse{Error: msg})
}

// WriteInternalError logs err and writes a generic 500 that does not leak
// internal details to the client.
func WriteInternalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	WriteError(w, http.StatusInternalServerError, "internal server error")
}

// MethodNotAllowed returns a handler answering 405 with a JSON error and the
// given Allow header. Register it on a method-less pattern next to the
// method-specific ones so it only catches unsupported methods.
func MethodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		WriteError(w, http.StatusMethodNotAllowed, "method "+r.Method+" not allowed")
	}
}

// DecodeJSON decodes a single JSON object from the request body into dst,
// rejecting unknown fields and trailing data. The returned error message is
// safe to show to clients.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return decodeError(err)
		}
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func decodeError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxErr    *http.MaxBytesError
	)
	switch {
	case errors.Is(err, io.EOF):
		return errors.New("request body must not be empty")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("request body contains malformed JSON")
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("request body contains malformed JSON (at offset %d)", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return fmt.Errorf("field %q must be %s", typeErr.Field, jsonTypeName(typeErr.Type.Kind().String()))
		}
		return errors.New("request body must be a JSON object")
	case errors.As(err, &maxErr):
		return fmt.Errorf("request body must not be larger than %d bytes", maxErr.Limit)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return fmt.Errorf("unknown field %s", strings.TrimPrefix(err.Error(), "json: unknown field "))
	default:
		return errors.New("request body contains invalid JSON")
	}
}

func jsonTypeName(kind string) string {
	switch kind {
	case "float64", "float32":
		return "a number"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "an integer"
	case "string":
		return "a string"
	case "bool":
		return "a boolean"
	default:
		return "a value of the correct type"
	}
}
