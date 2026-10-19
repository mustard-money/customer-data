// Package server wires routes and middleware into a single http.Handler.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mustard-money/customer-data/account"
	"github.com/mustard-money/customer-data/assistant"
	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/httpx"
	"github.com/mustard-money/customer-data/items"
	"github.com/mustard-money/customer-data/profiles"
	"github.com/mustard-money/customer-data/scenarios"
	"github.com/mustard-money/customer-data/snapshots"
	"github.com/mustard-money/customer-data/taxes"
	"github.com/mustard-money/customer-data/taxtables"
)

// MaxBodyBytes caps request bodies.
const MaxBodyBytes = 1 << 20 // 1 MB

// Options configures the handler.
type Options struct {
	CORSAllowedOrigins []string
	Logger             *slog.Logger
	// AuthProviders are the enabled Apple/Google sign-in providers.
	AuthProviders []*auth.Provider
	// Assistant answers chat requests; nil turns the assistant off (503).
	Assistant *assistant.Assistant
	// TaxTables compute taxes; nil uses the tables embedded in taxtables.
	TaxTables *taxtables.Tables
}

// New returns the application's root handler.
func New(pool *pgxpool.Pool, opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	notFound := func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, "not found")
	}
	authStore := auth.NewStore(pool)

	// Customer data: every route requires a signed-in customer.
	data := http.NewServeMux()
	items.NewHandler(items.NewStore(pool)).Register(data)
	profiles.NewHandler(profiles.NewStore(pool)).Register(data)
	account.NewHandler(account.NewStore(pool)).Register(data)
	scenarios.NewHandler(scenarios.NewStore(pool)).Register(data)
	snapshots.NewHandler(snapshots.NewStore(pool)).Register(data)
	assistant.NewHandler(opts.Assistant).Register(data)
	tables := opts.TaxTables
	if tables == nil {
		d, err := taxtables.Embedded()
		if err != nil {
			panic("embedded tax tables: " + err.Error()) // validated by taxtables' tests
		}
		tables = taxtables.NewTables(d)
	}
	taxes.NewHandler(tables).Register(data)
	data.HandleFunc("/", notFound)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(pool))
	// The auth routes are more specific than /api/v1/, so they stay public.
	auth.NewHandler(authStore, opts.AuthProviders...).Register(mux)
	mux.HandleFunc("/api/v1/auth/", notFound)
	mux.Handle("/api/v1/", auth.RequireCustomer(authStore)(data))
	mux.HandleFunc("/", notFound)

	return httpx.Chain(mux,
		httpx.LogRequests(logger),
		httpx.Recover(logger),
		httpx.CORS(opts.CORSAllowedOrigins),
		httpx.LimitBody(MaxBodyBytes),
	)
}

type healthResponse struct {
	Status string `json:"status"`
}

func healthz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			slog.WarnContext(r.Context(), "health check: database ping failed", "err", err)
			httpx.WriteJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, healthResponse{Status: "ok"})
	}
}
