package account

import (
	"errors"
	"net/http"

	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/httpx"
)

// Handler serves the /api/v1/account-profile endpoints.
type Handler struct {
	store *Store
}

// NewHandler returns a Handler using store.
func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

// Register mounts the account profile routes on mux.
//
// The routes must be served behind auth.RequireCustomer; every call is
// scoped to the signed-in customer.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/account-profile", h.get)
	mux.HandleFunc("PUT /api/v1/account-profile", h.put)

	mux.HandleFunc("/api/v1/account-profile", httpx.MethodNotAllowed("GET, PUT, OPTIONS"))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.store.Get(r.Context(), auth.CustomerID(r))
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var in ProfileInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := in.Validate()
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, created, err := h.store.Put(r.Context(), auth.CustomerID(r), p)
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, saved)
}
