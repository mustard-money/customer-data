package items

import (
	"errors"
	"net/http"

	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/fields"
	"github.com/mustard-money/customer-data/httpx"
)

// Handler serves the /api/v1/items endpoints.
type Handler struct {
	store *Store
}

// NewHandler returns a Handler using store.
func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

// Register mounts the item routes on mux.
//
// The routes must be served behind auth.RequireCustomer; every call is
// scoped to the signed-in customer.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/items", h.list)
	mux.HandleFunc("GET /api/v1/items/{id}", h.get)
	mux.HandleFunc("PUT /api/v1/items/{id}", h.put)
	mux.HandleFunc("DELETE /api/v1/items/{id}", h.delete)

	// Method-less patterns are less specific than the ones above, so they only
	// match unsupported methods; answer those with a JSON 405.
	mux.HandleFunc("/api/v1/items", httpx.MethodNotAllowed("GET, OPTIONS"))
	mux.HandleFunc("/api/v1/items/{id}", httpx.MethodNotAllowed("GET, PUT, DELETE, OPTIONS"))
}

type listResponse struct {
	Items []Item `json:"items"`
	// DeletedIDs are items deleted on any device; clients drop their copies.
	DeletedIDs []string `json:"deletedIds"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	if category != "" && !ValidCategory(category) {
		httpx.WriteError(w, http.StatusBadRequest, "category must be one of: income, investments, expenses")
		return
	}
	items, err := h.store.List(r.Context(), auth.CustomerID(r), category)
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	deleted, err := h.store.DeletedIDs(r.Context(), auth.CustomerID(r))
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse{Items: items, DeletedIDs: deleted})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if fields.ValidateID(id) != nil {
		writeNotFound(w)
		return
	}
	it, err := h.store.Get(r.Context(), auth.CustomerID(r), id)
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, it)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var in ItemInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	it, err := in.Validate(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, created, err := h.store.Upsert(r.Context(), auth.CustomerID(r), it)
	if errors.Is(err, ErrDeleted) {
		httpx.WriteError(w, http.StatusGone, "item was deleted")
		return
	}
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

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if fields.ValidateID(id) != nil {
		writeNotFound(w)
		return
	}
	err := h.store.Delete(r.Context(), auth.CustomerID(r), id)
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeNotFound(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, "item not found")
}
