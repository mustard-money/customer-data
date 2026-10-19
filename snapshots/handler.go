package snapshots

import (
	"errors"
	"net/http"
	"time"

	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/fields"
	"github.com/mustard-money/customer-data/httpx"
)

// Handler serves the /api/v1/snapshots endpoints.
type Handler struct {
	store *Store
	now   func() time.Time
}

// NewHandler returns a Handler using store.
func NewHandler(store *Store) *Handler {
	return &Handler{store: store, now: time.Now}
}

// Register mounts the snapshot routes on mux, which must be behind
// auth.RequireCustomer; every call is scoped to the signed-in customer.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/snapshots", h.list)
	mux.HandleFunc("GET /api/v1/snapshots/{id}", h.get)
	mux.HandleFunc("PUT /api/v1/snapshots/{id}", h.put)
	mux.HandleFunc("DELETE /api/v1/snapshots/{id}", h.delete)

	mux.HandleFunc("/api/v1/snapshots", httpx.MethodNotAllowed("GET, OPTIONS"))
	mux.HandleFunc("/api/v1/snapshots/{id}", httpx.MethodNotAllowed("GET, PUT, DELETE, OPTIONS"))
}

type listResponse struct {
	Snapshots []Snapshot `json:"snapshots"`
	// DeletedIDs are snapshots deleted on any device; clients drop their copies.
	DeletedIDs []string `json:"deletedIds"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	snaps, err := h.store.List(r.Context(), auth.CustomerID(r))
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	deleted, err := h.store.DeletedIDs(r.Context(), auth.CustomerID(r))
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse{Snapshots: snaps, DeletedIDs: deleted})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if fields.ValidateID(id) != nil {
		writeNotFound(w)
		return
	}
	snap, err := h.store.Get(r.Context(), auth.CustomerID(r), id)
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, snap)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var in SnapshotInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := in.Validate(r.PathValue("id"), h.now())
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, created, err := h.store.Upsert(r.Context(), auth.CustomerID(r), snap)
	if errors.Is(err, ErrDeleted) {
		httpx.WriteError(w, http.StatusGone, "snapshot was deleted")
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
	httpx.WriteError(w, http.StatusNotFound, "snapshot not found")
}
