package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mustard-money/customer-data/httpx"
)

// Handler serves the /api/v1/auth endpoints.
type Handler struct {
	store     *Store
	providers map[string]*Provider
}

// NewHandler returns a Handler using store. Only the given providers can be
// used to sign in; the routes of unconfigured ones answer 404.
func NewHandler(store *Store, providers ...*Provider) *Handler {
	h := &Handler{store: store, providers: make(map[string]*Provider, len(providers))}
	for _, p := range providers {
		h.providers[p.Name()] = p
	}
	return h
}

// Register mounts the auth routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/signup", h.signup)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	for _, name := range []string{ProviderApple, ProviderGoogle} {
		mux.HandleFunc("POST /api/v1/auth/"+name, h.signInWith(name))
	}
	require := RequireCustomer(h.store)
	mux.Handle("POST /api/v1/auth/logout", require(http.HandlerFunc(h.logout)))
	mux.Handle("GET /api/v1/auth/me", require(http.HandlerFunc(h.me)))

	for _, p := range []string{"signup", "login", "logout", ProviderApple, ProviderGoogle} {
		mux.HandleFunc("/api/v1/auth/"+p, httpx.MethodNotAllowed("POST, OPTIONS"))
	}
	mux.HandleFunc("/api/v1/auth/me", httpx.MethodNotAllowed("GET, OPTIONS"))
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	var in PasswordInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	email, password, err := in.validateSignup()
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, err := h.store.SignUp(r.Context(), email, password)
	if errors.Is(err, ErrEmailTaken) {
		httpx.WriteError(w, http.StatusConflict, ErrEmailTaken.Error())
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sess)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in PasswordInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	email, password, err := in.validateLogin()
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, err := h.store.LogIn(r.Context(), email, password)
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, ErrInvalidCredentials.Error())
		return
	}
	if err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sess)
}

func (h *Handler) signInWith(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := h.providers[name]
		if p == nil {
			httpx.WriteError(w, http.StatusNotFound, "sign in with "+name+" is not enabled")
			return
		}
		var in IDTokenInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		idToken, nonce, err := in.validate()
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		ident, err := p.Verify(r.Context(), idToken, nonce)
		if err != nil {
			slog.InfoContext(r.Context(), "rejected id token", "provider", name, "err", err)
			httpx.WriteError(w, http.StatusUnauthorized, errInvalidIDToken.Error())
			return
		}
		sess, created, err := h.store.SignInWithIdentity(r.Context(), ident)
		if errors.Is(err, ErrEmailTaken) {
			httpx.WriteError(w, http.StatusConflict, ErrEmailTaken.Error()+"; sign in the way you did before")
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
		httpx.WriteJSON(w, status, sess)
	}
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	token, _ := bearerToken(r)
	if err := h.store.LogOut(r.Context(), token); err != nil {
		httpx.WriteInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	Customer Customer `json:"customer"`
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	c, _ := CustomerFromContext(r.Context())
	httpx.WriteJSON(w, http.StatusOK, meResponse{Customer: c})
}

type ctxKey struct{}

// CustomerFromContext returns the customer RequireCustomer resolved.
func CustomerFromContext(ctx context.Context) (Customer, bool) {
	c, ok := ctx.Value(ctxKey{}).(Customer)
	return c, ok
}

// CustomerID returns the ID of the customer RequireCustomer resolved for r.
// It panics if r did not pass through RequireCustomer: serving a data route
// without it would be a wiring bug, and Recover turns the panic into a 500.
func CustomerID(r *http.Request) string {
	c, ok := CustomerFromContext(r.Context())
	if !ok {
		panic("auth.CustomerID: request did not pass through RequireCustomer")
	}
	return c.ID
}

// RequireCustomer rejects requests without a valid "Authorization: Bearer
// <token>" header with 401, and otherwise makes the customer available via
// CustomerFromContext.
func RequireCustomer(store *Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w, "missing bearer token")
				return
			}
			c, err := store.CustomerForToken(r.Context(), token)
			if errors.Is(err, ErrInvalidSession) {
				unauthorized(w, ErrInvalidSession.Error())
				return
			}
			if err != nil {
				httpx.WriteInternalError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c)))
		})
	}
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="mustard"`)
	httpx.WriteError(w, http.StatusUnauthorized, msg)
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}
