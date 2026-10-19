package taxes

import (
	"errors"
	"net/http"

	"github.com/mustard-money/customer-data/fields"
	"github.com/mustard-money/customer-data/httpx"
	"github.com/mustard-money/customer-data/taxtables"
)

// Handler serves /api/v1/taxes. It must be behind auth.RequireCustomer:
// computing taxes is for signed-in customers only.
type Handler struct {
	tables *taxtables.Tables
}

// NewHandler returns a Handler computing with tables.
func NewHandler(tables *taxtables.Tables) *Handler { return &Handler{tables: tables} }

// Register mounts the routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/taxes", h.compute)
	mux.HandleFunc("GET /api/v1/taxes/years", h.years)
	mux.HandleFunc("/api/v1/taxes", httpx.MethodNotAllowed("POST, OPTIONS"))
	mux.HandleFunc("/api/v1/taxes/years", httpx.MethodNotAllowed("GET, OPTIONS"))
}

type yearsResponse struct {
	FirstYear      int `json:"firstYear"`
	LastActualYear int `json:"lastActualYear"`
	LastYear       int `json:"lastYear"`
}

func (h *Handler) years(w http.ResponseWriter, _ *http.Request) {
	first, lastActual, last := h.tables.Years()
	httpx.WriteJSON(w, http.StatusOK, yearsResponse{first, lastActual, last})
}

func (h *Handler) compute(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := Compute(h.tables, req)
	var invalid *fields.ValidationError
	if errors.As(err, &invalid) {
		httpx.WriteError(w, http.StatusBadRequest, invalid.Error())
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}
