# Agent notes: customer-data

Go REST API + Postgres for the Mustard app. See README.md for the API contract; a frontend sync client depends on it, so do not change request/response shapes, status codes or error format without coordinating.

## Conventions

- Standard library `net/http` with Go 1.22+ method patterns (`"GET /api/v1/items/{id}"`); no web framework. Postgres via `pgx/v5` `pgxpool`; plain SQL, no ORM.
- `server.go` in the repo root is the entry point (`package main`). All other packages are top-level directories (no `internal/`, no `cmd/`).
- One package per resource (`items`, `profiles`, `scenarios`, `account`) holding model, validation, store and handlers. Shared field rules live in `fields`, HTTP helpers in `httpx`, wiring in `server`. LLM calls go through the `llm.Client` interface (`llm/openai`, `llm/lmstudio`).
- JSON is camelCase. Timestamps are rendered with `fields.FormatTime` (UTC, always milliseconds). `createdAt` comes from the client; `updatedAt` is always set by the server.
- Request structs use pointer fields so validation can tell missing from zero. Validation returns `*fields.ValidationError` (message is shown to clients as a 400).
- Errors are `{"error": "..."}` via `httpx.WriteError`. For unexpected errors use `httpx.WriteInternalError`, which logs the cause and returns a generic 500. Never leak DB errors to clients.
- Every method-specific route pair also registers a method-less pattern with `httpx.MethodNotAllowed` so 405s are JSON.
- Schema changes: add a new `db/migrations/NNNN_description.sql` (numbered from `0033`); never edit an applied migration, and never edit `db/sql/schema.sql`, which is the 0032 baseline (the original 0001–0032 combined). At startup the server applies the baseline to an empty database (or records it for one that ran 0001–0032), then each migration not yet in `schema_migrations`, in its own transaction. A new table also goes in `db/sql/drop.sql`. `db/sql/` is the database as SQL (drop, schema, default data, test data; see README). `db/sql/default-data.sql` is generated from the tax data by `taxdb.WriteSQL`: run `make default-data` after the tax data changes (`go test ./taxdb` fails until you do).
- Auth lives in `auth` (customers, Apple/Google identities, opaque bearer sessions). All data routes are registered on a mux that `server` mounts behind `auth.RequireCustomer`; handlers pass `auth.CustomerID(r)` to every store method, and every table's key starts with `customer_id`. New resources must do the same. Synced resources (items, profiles, scenarios, snapshots) also remember deletions via `deletions`: `Delete` records the id in the same statement, `Upsert` refuses deleted ids (`ErrDeleted` → 410), and lists include `deletedIds`. A new synced resource needs its name added to `deletions` and to the `deletions_collection_valid` constraint.
- Taxes: the tax tables (`taxtables/data/`) and the tax arithmetic (`taxtables`) live here and nowhere else: this server is their source of truth, and neither the app nor financial-planning keeps a copy. Check data changes with `go test ./taxtables`, then `make default-data`. `taxdb` loads them into Postgres at startup when their hash changes (reference data, not keyed by customer; never edit by SQL), and `taxes` serves `POST /api/v1/taxes` / `GET /api/v1/taxes/years` to signed-in customers only. Never add a route that returns the tables themselves (brackets, rates, deductions, ZIP coverage): clients get computed taxes only. `taxes/testdata/golden.json` holds results from the app's former TypeScript tax code that the Go port must match.
- The assistant (`assistant`, `POST /api/v1/assistant/chat`) is stateless: the app sends the conversation and its data snapshot, and the model's proposed actions are validated by decoding them into the resources' own input structs (`items.ItemInput`, `profiles.ProfileInput`, `account.ProfileInput`), so new fields there are accepted automatically; describe them in `assistant/prompt.go` too. Never log conversation contents.
- Tracing: OpenTelemetry, set up in `tracing` (exports over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, else a no-op); Jaeger receives it in the Kubernetes deployment. The assistant's spans are in `assistant/trace.go` (request → `assistant.chat` → one `chat <model>` span per model call, GenAI attribute names). Spans carry counts, timings and model details only; the conversation and the customer's data go into them only with `TRACE_ASSISTANT_CONTENT=true` (config, off by default), and tests check both. Get tracers with `otel.Tracer(...)` at the point of use, not in a package-level variable, which would stay bound to the first provider installed.
- Logging: `log/slog` only.

## Checks before finishing

```sh
gofmt -l .                 # must print nothing
go vet ./...
go build ./...
make test                  # unit tests
make test-integration      # needs Docker; add DB_PORT=5433 if localhost:5432 is taken
```

Integration tests live in `server` and skip unless `TEST_DATABASE_URL` is set. `newTestServer` signs up a default customer and sends its token on requests without an `Authorization` header (set `anonymousHeader` to test unauthenticated access). They truncate tables, so never point them at a database you care about.

Use plain `docker run` in scripts (the Makefile must not depend on `docker compose`).
