# customer-data

Go REST backend, backed by Postgres, that stores personal-finance **items**, household-member **profiles** and what-if **scenarios** for the Mustard app.

The Mustard frontend (Expo/React Native PWA) keeps data in on-device storage by default and can optionally sync to this service. Customers sign up and sign in with email + password or Apple/Google; every data endpoint requires a session token and only sees the signed-in customer's records.

## Quick start

Requirements: Go 1.25+ and a Docker runtime (e.g. `colima start`). `docker compose` is **not** required.

```sh
make db     # start Postgres 17 in a container named mustard-postgres (idempotent)
make run    # start the API on http://localhost:8080 (applies migrations on startup)
curl localhost:8080/healthz
```

Other targets:

| Target                  | What it does                                                                 |
| ----------------------- | ---------------------------------------------------------------------------- |
| `make db`               | Create/start the `mustard-postgres` container (volume `mustard-postgres-data`) and wait until ready |
| `make db-stop`          | Stop the container (data is kept in the volume)                              |
| `make run`              | `go run .`                                                                   |
| `make build`            | Build `bin/server`                                                           |
| `make test`             | Unit tests (integration tests skip without `TEST_DATABASE_URL`)              |
| `make test-integration` | Creates the `mustard_test` database if needed and runs all tests against it  |
| `make lint`             | `go vet ./...`                                                               |
| `make fmt`              | `gofmt -w .`                                                                 |
| `make jaeger`           | Start Jaeger (`mustard-jaeger` container): UI on 16686, OTLP on 4318; `jaeger-restart` clears it, `jaeger-stop` stops it |
| `make test-accounts`    | Add 100 test accounts, `test0001@mustard.money` to `test0100@mustard.money`, password `mustard` |
| `make test-accounts-remove` | Remove those test accounts and everything they saved                     |

### Dump and restore the dev database

Two scripts at the repo root save the local database to SQL files and build a database from them (both run `pg_dump`/`psql` inside the `mustard-postgres` container, starting it if needed):

```sh
../db-dump.sh [DIR]                                   # schema.sql, data.sql, dump.txt into DIR
../db-load.sh [DIR] [--db NAME] [--replace] [--schema-only]
```

`DIR` defaults to `db/dumps/` (git-ignored: a dump holds customers' password hashes and sessions). `schema.sql` has the tables plus the `schema_migrations` rows, so the backend doesn't re-run migrations on a restored database; `data.sql` has every other table's rows. `db-load.sh` loads into `mustard` (or `--db NAME`), creating it if needed; it won't touch a database that already has tables unless given `--replace`, which drops it first (disconnecting a running backend). `--schema-only` gives an empty database at the dump's migration.

### The database in SQL: `db/sql/`

The whole database is four sets of statements, each runnable on its own with
`psql "$DATABASE_URL" -1 -f db/sql/<file>`, in this order:

| File | What it does |
| --- | --- |
| `drop.sql` | `DROP TABLE IF EXISTS` every table (customer data, tax tables, migration records). **Destructive.** |
| `schema.sql` | Creates every table, constraint and index: the schema of the original migrations 0001–0032, combined. Records itself as migration `0032_baseline`. |
| `default-data.sql` | The data every database needs that isn't a customer's: the tax tables (1970 onward with projected years) and their version. Generated; regenerate with `make default-data` after the tax data in `../financial-planning/taxtables` changes (`go test ./taxdb` fails while it's out of date). |
| `test-data.sql` | 100 empty accounts, `test0001@mustard.money` to `test0100@mustard.money`, all with the password `mustard` (`test-data-remove.sql` deletes them with everything they saved). Safe to repeat. **Development and test databases only:** the accounts share a known, short password (shorter than sign-up allows; sign-in doesn't check length). |

`make db-reset` runs the first three against the `mustard-postgres` container's `$(DB_NAME)` database
(dev only), and `make test-accounts` / `make test-accounts-remove` the test data.

The server doesn't need any of this to start: on an empty database it applies `schema.sql` itself and
loads the tax tables (it reloads them whenever their version changes). A database that already ran the
original migrations 0001–0032 is recognised and recorded as at the baseline; one part of the way through
them must first be migrated by a server from before they were combined. Schema changes since go in
`db/migrations/NNNN_description.sql`, numbered from `0033`.

### Port 5432 already taken?

If something else listens on `localhost:5432` (e.g. Postgres.app or a Homebrew Postgres), connections to
`localhost:5432` go to it instead of the container and fail with authentication errors. Use another host port;
every Makefile target honours `DB_PORT`:

```sh
make db DB_PORT=5433
make run DB_PORT=5433
make test-integration DB_PORT=5433
```

(`make db` does not move an existing container to a new port; `docker rm -f mustard-postgres` first. The data volume is kept.)

A `compose.yaml` is included for people who prefer `docker compose up -d`; the Makefile does not use it.

## Configuration

| Variable               | Default                                                             |
| ---------------------- | ------------------------------------------------------------------- |
| `DATABASE_URL`         | `postgres://mustard:mustard@localhost:5432/mustard?sslmode=disable` |
| `PORT`                 | `8080`                                                              |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173,http://localhost:3000,http://localhost:8081` (comma-separated; `*` allows any origin) |
| `APPLE_CLIENT_IDS`     | unset (comma-separated bundle ID and/or Services ID that Apple ID tokens must be issued to; unset disables `POST /api/v1/auth/apple`) |
| `GOOGLE_CLIENT_IDS`    | unset (comma-separated OAuth client IDs, e.g. web, iOS and Android; unset disables `POST /api/v1/auth/google`) |
| `LLM_PROVIDER` | (unset) | The assistant's model: `openai` (any OpenAI-compatible API) or `lmstudio` (a local LM Studio server). Unset turns the assistant off (`/api/v1/assistant/chat` answers 503) |
| `LLM_BASE_URL` | provider default | API root, e.g. `https://api.openai.com/v1` or `http://localhost:1234/v1` |
| `LLM_API_KEY` | (unset) | Bearer token for the provider, if it needs one |
| `LLM_MODEL` | (unset) | Model name; required for `openai` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (unset) | Where to send traces over OTLP/HTTP, e.g. Jaeger's `http://jaeger:4318`. Unset turns tracing off. The other standard `OTEL_*` variables apply too (`OTEL_SERVICE_NAME`, default `customer-data`; `OTEL_TRACES_SAMPLER`; ...) |
| `TRACE_ASSISTANT_CONTENT` | `false` | `true` adds the assistant's prompts and replies, which include the customer's messages and data, to its traces. For local debugging only: never with real customers |
| `TEST_DATABASE_URL`    | unset; integration tests run only when set                          |

### Tracing

With `OTEL_EXPORTER_OTLP_ENDPOINT` set, each assistant chat is traced (`assistant/trace.go`):

```
POST /api/v1/assistant/chat     status code, validation errors
  assistant.chat                message and record counts, re-asks for invalid JSON, actions proposed and dropped;
                                one "model proposed <type>" event per action the model asked for (target id,
                                accepted, or why it was rejected; its fields only with TRACE_ASSISTANT_CONTENT)
    chat <model>                one per model call (OpenTelemetry GenAI conventions): provider, model,
                                temperature, max tokens, token usage, finish reason, provider retries, errors
```

By default the spans hold counts and timings, never the conversation or the customer's data.
`TRACE_ASSISTANT_CONTENT=true` adds `gen_ai.input.messages` (the system prompt with the data snapshot,
plus the conversation) and `gen_ai.output.messages` (the model's reply) to each model call.

To look at traces locally, `../dev.sh start api` (or `../dev.sh`, the whole stack) starts Jaeger and wires the backend to it (`--no-trace` skips it,
`--trace-content` adds prompts and replies, `../dev.sh restart-jaeger` clears the traces). Without dev.sh:

```sh
make jaeger                                                   # UI http://localhost:16686, OTLP :4318
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make run
make jaeger-restart                                           # fresh container, no traces
make jaeger-stop
```

Jaeger keeps traces in memory, so stopping or restarting it clears them. If `make jaeger` reports that
Jaeger didn't answer, Docker may not be publishing ports (with Colima, `colima restart` fixes it).

On Kubernetes, Jaeger is part of the deployment (`../kubernetes/base/jaeger.yaml`).

## Container image

`Dockerfile` builds a static binary and runs it on `gcr.io/distroless/static-debian12:nonroot` (port 8080,
migrations applied at startup). Configure it with the variables above. Build it from this directory (`.dockerignore` leaves out binaries and database dumps).

```sh
docker build -t mustard/customer-data:dev .
# with `make db DB_PORT=5433` running:
docker run --rm -p 18080:8080 \
  -e 'DATABASE_URL=postgres://mustard:mustard@host.docker.internal:5433/mustard?sslmode=disable' \
  mustard/customer-data:dev
curl localhost:18080/healthz
```

Kubernetes manifests and deployment steps: [`../kubernetes/README.md`](../kubernetes/README.md).

## API

Base URL `http://localhost:8080`. All bodies are JSON (camelCase). Every `/api/v1` route except `/api/v1/auth/*` requires `Authorization: Bearer <token>` (see [Authentication](#authentication)) and returns 401 without it; records belong to the signed-in customer, and ids only need to be unique per customer. Request bodies are limited to 1 MB; unknown fields are rejected.

### Item

```json
{ "id": "lz3k9abc12", "category": "income", "name": "Salary", "amount": 6500, "frequency": "monthly",
  "createdAt": "2026-09-27T06:20:39.033Z", "updatedAt": "2026-09-27T06:20:39.100Z" }
```

| Field       | Rules |
| ----------- | ----- |
| `id`        | Client-generated, 1–64 chars of `[A-Za-z0-9_-]`. Optional in a PUT body, but must equal the path id if present. |
| `category`  | `income`, `investments` or `expenses` |
| `name`      | 1–200 characters after trimming whitespace (stored trimmed) |
| `amount`    | Number >= 0, stored as `NUMERIC(14,2)` (rounded to cents) |
| `frequency` | Required for `income`/`expenses`; must be absent or `null` for `investments` (omitted in responses). `income`: `weekly`, `biweekly` (every two weeks), `semimonthly` (twice a month), `monthly`, `quarterly`, `semiannually` (twice a year) or `yearly`. `expenses`: `once` (a one-time cost in its start month), `daily`, `weekly`, `monthly` or `yearly` |
| `incomeType` | Optional, `income` only (must be absent or `null` otherwise; omitted in responses when unset). The tax form it's reported on: `w2`, `1099nec`, `1099misc`, `1099k`, `k1`, `1099int`, `1099div`, `1099b`, `1099r`, `ssa1099`, `1099g` or `other` |
| `selfEmployment` | Optional, `income` only: `true` when the income is self-employment earnings, which owe self-employment tax whatever its `incomeType`. `false`, `null` and absent all mean no (only `true` is stored; omitted in responses otherwise); `true` on other categories is rejected |
| `qualifiedDividends` | Optional, `income` with `incomeType` `1099div` only: `false` when the dividends aren't qualified (taxed as ordinary income). Qualified is the default: `true`, `null` and absent all mean qualified (only `false` is stored; omitted in responses otherwise); `false` on other income is rejected |
| `benefits` | Optional; only for `income` with `incomeType` `w2` (omitted in responses when unset). Workplace savings, each part optional: `retirement` (a 401(k) or 403(b)) `{contributionRate, roth, matchRate, matchUpTo}`, `plan457` `{contributionRate, roth}` and `hsa` `{employeeAnnual, employerAnnual, family}`. Each plan may also carry an optional `balance` `{amount, asOfYear, asOfMonth}` (dollars, rounded to cents; `asOfYear` 1900 to next year, omitted for today's balance; `asOfMonth` 1–12 and only with `asOfYear`): where apps kept a plan's balance before workplace accounts became linked investments (see `workplace`); current apps move it to the linked investment and save the plan without it. Rates are fractions of pay from 0 to 1; `matchRate` (dollars matched per dollar contributed) goes up to 2, and `matchUpTo` 1 means no limit. `roth` and `family` default to false. HSA amounts are yearly dollars, rounded to cents. Saving an item without `benefits` removes them |
| `account`   | Optional, `investments` only (must be absent or `null` otherwise; omitted in responses when unset): `taxable` (brokerage), `bank` (checking, savings), `cd` (certificate of deposit), `hsa`, `traditionalIra`, `rothIra`, `traditional401k`, `roth401k`, `traditional403b`, `roth403b`, `traditional457b` or `roth457b` |
| `assetClass` | Optional, `investments` with `account` `taxable` only: `equities` (the default; grows at the expected return) or `fixedIncome` (keeps its value and pays `incomeRate`). Only `fixedIncome` is stored; omitted in responses otherwise |
| `incomeRate` | Required with `assetClass` `fixedIncome` (and only then): yearly income as a fraction of the value, from 0 to 0.5. `returnRate` must be absent for fixed income |
| `taxExempt` | Optional, fixed income only: `{federal, amt, state, local}` booleans for the taxes its interest is exempt from (AMT is recorded; not yet computed). Omitted when none |
| `workplace` | Optional, `investments` only: links a workplace account to the W-2 `income` item whose plan it is, `{incomeId, plan}` with `plan` one of `retirement` (then `account` must be `traditional401k`, `roth401k`, `traditional403b` or `roth403b`), `plan457` (`traditional457b` or `roth457b`) or `hsa` (`hsa`). The income holds the plan's contributions in `benefits`, the investment its balance; the app creates, updates and unlinks these. `incomeId` is an id only and isn't checked against the customer's items. Omitted when unset |
| `asOfYear`, `asOfMonth` | Optional, `investments` only: when `amount` was the value. `asOfYear` from 1900 to next year; `asOfMonth` 1–12 and only with `asOfYear`. Omitted in responses when unset |
| `returnRate` | Optional, `investments` only: expected yearly return overriding the account profile's `expectedReturn`, from -0.5 to 0.5. Omitted when unset |
| `startYear`, `startMonth`, `endYear`, `endMonth` | Optional, `income` and `expenses` (not `investments`): when it starts and ends (years 1900–2150, months 1–12 and only with their year). The end (December if no month) can't be before the start (January if no month); a `once` expense has no end. Omitted when unset |
| `loan` | Optional, `expenses` with frequency `monthly` only (omitted when unset): makes the expense the payment on a debt. `{kind, balance, interestRate, asOfYear, asOfMonth, homeValue}`: `kind` is `mortgage`, `autoLoan`, `studentLoan` or `otherLoan`; `balance` is what's owed (dollars, rounded to cents) as of `asOfYear`/`asOfMonth` (optional, as for workplace balances); `interestRate` is the APR as a fraction from 0 to 1; `homeValue` (dollars) is for mortgages only. The `amount` (the monthly payment) must be more than a month's interest on the balance. Saving the item without `loan` removes it |
| `scenarioId` | Optional id (format above) of the scenario the item belongs to; omitted for items in Main, the user's actual situation. Not checked for existence |
| `replaces` | Optional id of the Main item this one stands in for within its scenario (an edit made while working in the scenario); needs `scenarioId` and can't be the item's own id |
| `withdrawalStartYear`, `withdrawalStartMonth`, `withdrawalRate` | Optional withdrawal plan for `investments` in a retirement account (`traditionalIra`, `rothIra`, `traditional401k`, `roth401k`, `traditional403b`, `roth403b`, `traditional457b`, `roth457b`): start year and rate come together; `withdrawalRate` is the yearly share of the balance, above 0 and at most 1. Omitted when unset |
| `createdAt` | Required RFC3339 timestamp set by the client; returned in UTC with millisecond precision |
| `updatedAt` | Set by the server on every write; any client value is ignored |

### Profile (household member)

```json
{ "id": "lz3k9abc12", "name": "Alex", "relationship": "self", "birthYear": 1988, "birthMonth": 3,
  "createdAt": "2026-09-27T06:20:39.033Z", "updatedAt": "2026-09-27T06:20:39.100Z" }
```

| Field          | Rules |
| -------------- | ----- |
| `id`           | Same as items |
| `name`         | 1–200 characters after trimming whitespace |
| `relationship` | `self`, `partner`, `child` or `other` |
| `birthYear`    | Optional integer (absent or `null`), 1900 through the current year; omitted in responses when unset |
| `birthMonth`   | Optional integer 1–12 (1 = January); requires `birthYear`, and must not be in the future; omitted in responses when unset |
| `createdAt` / `updatedAt` | Same as items |

### Snapshot (a month's figures)

The app records one snapshot per month for the Home graph's history:
`/api/v1/snapshots` (list, with `deletedIds`) and `/api/v1/snapshots/{id}` (GET, PUT, DELETE), like profiles.

```json
{ "id": "2026-10", "year": 2026, "month": 10, "netWorth": 306255, "assets": 720000, "debts": 413745,
  "monthlyIncome": 9000, "monthlyExpenses": 3748.2,
  "createdAt": "2026-10-06T19:00:00.000Z", "updatedAt": "2026-10-06T19:00:00.100Z" }
```

| Field | Rules |
| ----- | ----- |
| `id` | The month, `YYYY-MM`; must match `year` and `month` |
| `year`, `month` | Required; not a month that hasn't started yet anywhere (UTC+14) |
| `netWorth` | Required dollars, ±1 trillion (can be negative); rounded to cents |
| `assets`, `debts`, `monthlyIncome`, `monthlyExpenses` | Required dollars, 0 to 1 trillion; rounded to cents |
| `createdAt` / `updatedAt` | Same as items |

### Scenario

A user-defined what-if projection over a set of items.

```json
{ "id": "lz3k9abc12", "name": "Buy a house in 2028", "itemIds": ["mujge6vxt1vsmn", "mujgf43apl78jy"],
  "events": [
    { "id": "ev1", "kind": "oneTimeCost", "name": "Down payment", "year": 2, "amount": 250000 },
    { "id": "ev2", "kind": "incomePause", "name": "Sabbatical", "year": 3, "amount": 0, "months": 6 },
    { "id": "ev3", "kind": "recurringCost", "name": "Childcare", "year": 4, "amount": 30000, "years": 5 },
    { "id": "ev4", "kind": "oneTimeIncome", "name": "RSU sale", "year": 1, "amount": 80000 }
  ],
  "parameters": { "years": 20, "returnRate": 0.07, "inflation": 0.03, "incomeGrowth": 0.03, "incomeAdjustment": 0, "expenseAdjustment": 0 },
  "createdAt": "2026-09-27T06:20:39.033Z", "updatedAt": "2026-09-27T06:20:39.100Z" }
```

| Field        | Rules |
| ------------ | ----- |
| `id`         | Same as items |
| `name`       | 1–200 characters after trimming whitespace |
| `itemIds`    | Required array (may be empty), at most 500 entries. Each entry uses the id format above; no duplicates. Items are not checked for existence (they sync independently) |
| `excludedItemIds` | Optional array, same rules as `itemIds`: the Main items this scenario leaves out. A scenario is Main minus these, with its own items (`scenarioId`) added and replacing the ones they name (`replaces`). Scenarios saved without it are from before layers: they include only the Main items in `itemIds` |
| `events`     | Required array (may be empty), at most 100 events (see below) |
| `parameters` | Required object; every field below must be present |
| `createdAt` / `updatedAt` | Same as items |

Event fields (unknown keys are rejected):

| Field    | Rules |
| -------- | ----- |
| `id`     | Id format above; unique within the scenario |
| `kind`   | `oneTimeCost`, `oneTimeIncome`, `incomePause` or `recurringCost` |
| `name`   | 1–200 characters after trimming whitespace |
| `year`   | Integer 1–60 |
| `amount` | Number >= 0 and <= 999999999999.99, rounded to cents. Must be `0` for `incomePause` |
| `months` | Integer 1–60; required for `incomePause`, must be absent or `null` otherwise (omitted in responses) |
| `years`  | Integer 1–60; required for `recurringCost`, must be absent or `null` otherwise (omitted in responses) |

Parameter fields (unknown keys are rejected; rates are fractions, so `0.07` is 7%; all are required except the retirement date):

| Field               | Range |
| ------------------- | ----- |
| `years`             | Integer 1–60 |
| `returnRate`        | −0.5 to 0.5 |
| `inflation`         | −0.1 to 0.2 |
| `incomeGrowth`      | −0.2 to 0.3 |
| `incomeAdjustment`  | −1 to 2 (`0.1` means +10%) |
| `expenseAdjustment` | −0.9 to 2 |
| `retirementYear`    | Optional integer 1900–2200: when the household retires (the app stops earned income, W-2 and 1099-NEC/K, and its workplace contributions at the end of that month). Omitted when unset |
| `retirementMonth`   | Optional integer 1–12, only with `retirementYear` (none means January). Omitted when unset |

### Account profile

One record per customer: where they live in the US and their filing status, which the app sends with its tax requests. The service is US-only.

```json
{ "country": "US", "postalCode": "94103", "filingStatus": "single", "updatedAt": "2026-09-27T06:20:39.100Z" }
```

| Field          | Rules |
|----------------|-------|
| `country`      | Optional; must be `US` if sent. Always `US` in responses |
| `postalCode`   | Optional (empty until entered); trimmed; a US ZIP code, `12345` or `12345-6789` |
| `filingStatus` | `single` (default) or `marriedJoint` |
| `expectedReturn` | Optional default yearly investment return (0.07 is 7%), from -0.5 to 0.5. Omitted or `null` clears it; omitted in responses when unset |
| `inflation` | Optional expected yearly inflation (0.03 is 3%), from -0.1 to 0.2 (the scenario parameter's bounds). Omitted or `null` clears it; omitted in responses when unset |
| `cashReturn` | Optional yearly interest rate (0 to 0.2, `0.04` is 4%) that bank accounts and CDs without a rate of their own earn; the app defaults it when unset. Omitted when unset |
| `emergencyFund` | Optional cash (dollars, rounded to cents, 0 or more) to keep on hand as an emergency fund; the app treats cash beyond it as invested in a taxable account and defaults it to 3 months of expenses. Omitted or `null` clears it; omitted in responses when unset |
| `updatedAt`    | Set by the server on every write; ignored if sent |

### Authentication

Every sign-in endpoint returns a session:

```json
{ "token": "q8V0...43 chars", "expiresAt": "2026-10-27T06:20:39.100Z",
  "customer": { "id": "M3QK7ZC2B4XW6YJ5NRT2DHLAVE", "email": "alex@example.com", "createdAt": "2026-09-27T06:20:39.033Z" } }
```

Send the token as `Authorization: Bearer <token>`. Tokens are opaque, valid for 30 days, and revoked by logout; the server stores only their SHA-256. `customer.email` is lowercased, and `null` for an Apple/Google customer whose provider shared no verified email.

| Body field | Rules |
| ---------- | ----- |
| `email`    | Required for signup/login; a bare address (no display name), at most 254 characters; trimmed and lowercased |
| `password` | Signup: at least 8 characters, at most 256 bytes. Login: required. Hashed with argon2id |
| `idToken`  | Apple/Google: the ID token from the provider's sign-in SDK. Its issuer, audience (`APPLE_CLIENT_IDS` / `GOOGLE_CLIENT_IDS`), signature and expiry are checked |
| `nonce`    | Apple/Google, optional: the raw nonce the app generated. When sent, the token's `nonce` claim must equal it or its SHA-256 hex |

The first Apple/Google sign-in creates a customer (201); later ones return the same customer (200). A new Apple/Google identity is never linked to an existing account by email: if its verified email already belongs to a customer, the response is 409. Password login for an Apple/Google-only customer is a 401.

### Endpoints

| Method & path                  | Success                        | Errors |
| ------------------------------ | ------------------------------ | ------ |
| `GET /healthz`                 | 200 `{"status":"ok"}`          | 503 `{"status":"unavailable"}` when the DB ping fails |
| `GET /api/v1/items`            | 200 `{"items":[...],"deletedIds":[...]}`, items ordered by `createdAt`, then `id`. Optional `?category=` (filters items, not `deletedIds`) | 400 invalid category |
| `GET /api/v1/items/{id}`       | 200 item                       | 404 |
| `PUT /api/v1/items/{id}`       | 201 item (created) / 200 item (replaced) | 400 validation, malformed JSON, unknown field, id mismatch; 410 deleted |
| `DELETE /api/v1/items/{id}`    | 204                            | 404 (the deletion is still recorded) |
| `GET /api/v1/profiles`         | 200 `{"profiles":[...],"deletedIds":[...]}` ordered by `createdAt`, then `id` | |
| `GET /api/v1/profiles/{id}`    | 200 profile                    | 404 |
| `PUT /api/v1/profiles/{id}`    | 201 / 200 profile              | 400; 410 deleted |
| `DELETE /api/v1/profiles/{id}` | 204                            | 404 (the deletion is still recorded) |
| `GET /api/v1/scenarios`        | 200 `{"scenarios":[...],"deletedIds":[...]}` ordered by `createdAt`, then `id` | |
| `GET /api/v1/scenarios/{id}`   | 200 scenario                   | 404 |
| `PUT /api/v1/scenarios/{id}`   | 201 / 200 scenario             | 400; 410 deleted |
| `DELETE /api/v1/scenarios/{id}` | 204                           | 404 (the deletion is still recorded) |
| `GET /api/v1/snapshots`        | 200 `{"snapshots":[...],"deletedIds":[...]}` ordered by month | |
| `GET /api/v1/snapshots/{id}`   | 200 snapshot                   | 404 |
| `PUT /api/v1/snapshots/{id}`   | 201 / 200 snapshot             | 400; 410 deleted |
| `DELETE /api/v1/snapshots/{id}` | 204                           | 404 (the deletion is still recorded) |
| `GET /api/v1/account-profile`  | 200 account profile            | 404 until one has been saved |
| `PUT /api/v1/account-profile`  | 201 (created) / 200 (replaced) | 400 validation, malformed JSON, unknown field |
| `POST /api/v1/auth/signup`     | 201 session                    | 400 validation, 409 email already registered |
| `POST /api/v1/auth/login`      | 200 session                    | 400, 401 wrong email or password |
| `POST /api/v1/auth/apple`      | 201 session (new customer) / 200 session | 400, 401 invalid ID token, 404 not configured, 409 email already registered |
| `POST /api/v1/auth/google`     | Same as Apple                  | Same as Apple |
| `POST /api/v1/auth/logout`     | 204 (bearer token required)    | 401 |
| `POST /api/v1/assistant/chat` | 200 `{"reply":"...","actions":[...],"dropped":0}` (see Assistant below) | 400 validation, 502 model unreachable, 503 not configured |
| `GET /api/v1/auth/me`          | 200 `{"customer":{...}}` (bearer token required) | 401 missing, unknown or expired token (with `WWW-Authenticate: Bearer`) |
| `POST /api/v1/taxes`          | 200 `{"taxYear":…,"projected":false,"layers":[...],"results":[...]}` (signed in; see Taxes below) | 400 validation |
| `GET /api/v1/taxes/years`     | 200 `{"firstYear":1970,"lastActualYear":2026,"lastYear":2100}` (signed in) | |

Errors are `{"error":"<message>"}` with status 400, 401, 404, 405 (with an `Allow` header), 409, 410 or 500. 500s never include internal details; they are logged instead.

Deletions are permanent and remembered per customer. `DELETE` records the id even when the server never had it (it may exist only on another device), each list's `deletedIds` names every deleted id so clients drop their copies, and `PUT` to a deleted id answers 410 Gone. Ids are generated by clients, so a deleted id is never meant to be reused.

CORS: origins in `CORS_ALLOWED_ORIGINS` get `Access-Control-Allow-Origin`; allowed methods `GET, POST, PUT, DELETE, OPTIONS`, allowed headers `Authorization, Content-Type`. Preflight `OPTIONS` requests are answered with 204.

### Assistant

`POST /api/v1/assistant/chat` (signed in) asks the configured LLM about the customer's finances. The body is `{"messages":[{"role":"user"|"assistant","content":"..."}], "context":{"today":"YYYY-MM-DD","items":[...],"household":[...],"profile":{...}}}`: the conversation (at most 40 messages of up to 4,000 characters, ending with the user's) and a snapshot of the customer's data from the device, in the API's record shapes (at most 500 items and household members). Nothing is stored, and the conversation isn't logged.

The reply's `actions` are changes the model proposes; the server validates each with the same rules as the data endpoints and leaves out (and counts in `dropped`) any that fail. Types: `createItem`, `updateItem`, `deleteItem`, `createMember`, `updateMember`, `deleteMember` (household members) and `updateProfile` (account profile). Creates and updates carry `fields`, the complete record as it would be saved, without `id`, `createdAt` or `updatedAt` (fields an update leaves out should be cleared); updates and deletes carry the record's `id`. The app shows them to the customer and applies the accepted ones through the normal endpoints. If the model doesn't return the expected JSON, it's asked once more; after that its text is the reply with no actions.

### Taxes

The tax tables (US income and payroll tax parameters by year, 1970 through 2100) live here, in `taxtables/`: transcribed from official sources into `taxtables/data/` (format, sources, coverage and projection rules in its `README.md`), with the package that validates them, projects later years, looks up the taxes for a ZIP code and year (`Tables.For`) and does the tax arithmetic (`compute.go`). This server is their only home and their only user: `taxdb` loads the tables into Postgres at startup whenever the data changes, and `taxes` computes with them. **The tables are never sent to clients**; only computed taxes are, and only to signed-in customers.

`POST /api/v1/taxes` takes `{"taxYear":2026,"postalCode":"94103","filingStatus":"single"|"marriedJoint","cases":[...]}`: the year whose rules apply (`GET /api/v1/taxes/years` gives the span; years after the last published one are projected by indexing brackets at an assumed 2.3% a year), the household's ZIP code (empty for federal tax alone), and 1 to 2,000 yearly incomes. Each case has `payrollWages` (W-2 wages owing Social Security and Medicare), `income` (what income tax applies to under the federal rules, before self-employment income), and optionally `hsa` (payroll HSA contributions, taxed as wages by California and New Jersey), `deferrals` (traditional 401(k)/403(b)/457(b) contributions, taxed by Pennsylvania) `selfEmployment` (net self-employment earnings), `capitalGains` (net long-term capital gains and qualified dividends: taxed federally at the year's capital gains rates, stacked on top of the other income as on the IRS Qualified Dividends and Capital Gain Tax Worksheet, and as ordinary income by states and localities), `qualifiedDividends` (taxed like `capitalGains` from 2003, when the Jobs and Growth Tax Relief Reconciliation Act extended the capital gains rates to dividends, and as ordinary income before) `interest` (interest and other investment income taxed at ordinary rates), `earlyDistributions` (the part of `income` that is retirement plan or IRA withdrawals before 59½, owing the 10% early-distribution tax from 1975) `interestExemptFederal`, `interestExemptState` and `interestExemptLocal` (the parts of `interest` exempt from each level's income tax, e.g. municipal bonds federally, Treasuries from state and local tax; federal-exempt interest also isn't net investment income), and `hsaNonMedical` (the part of `income` that is HSA withdrawals not for medical costs before 65, owing 10% for 2004–2010 and 20% from 2011). Amounts are dollars from 0 to 10¹².

The response names the taxes that apply (`layers`: `id`, `kind`, `region`, `coverage`, without rates) and gives each case's `incomeTax` (one amount per layer), `investmentTax` (per layer, the part of `incomeTax` that `capitalGains`, `qualifiedDividends` and `interest` add), `netInvestmentIncomeTax` (the 3.8% federal tax on investment income above the income threshold, from 2013), `additionalTax` (the early-distribution and HSA additional taxes), `socialSecurity`, `medicare`, `selfEmployment` and `total`. `unknownPostalCode` (federal only), `stateMissing` and `missingLocals` (rules for that year not on file, so left out), `capitalGainsMissing` (no capital gains rules for that year, so gains were taxed as ordinary income) and `projected` say what the figures leave out or assume. Nothing in the request is stored.

### curl examples

```sh
# Sign up (201) or log in (200), and keep the token for the other requests
TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/signup -H 'Content-Type: application/json' \
  -d '{"email":"alex@example.com","password":"correct horse"}' | jq -r .token)
curl -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"alex@example.com","password":"correct horse"}'
AUTH="Authorization: Bearer $TOKEN"
curl -H "$AUTH" localhost:8080/api/v1/auth/me

# Create (201) or replace (200) an item
curl -H "$AUTH" -X PUT localhost:8080/api/v1/items/lz3k9abc12 -H 'Content-Type: application/json' \
  -d '{"category":"income","name":"Salary","amount":6500,"frequency":"monthly","incomeType":"w2","createdAt":"2026-09-27T06:20:39.033Z"}'

# Investments have no frequency; account is optional
curl -H "$AUTH" -X PUT localhost:8080/api/v1/items/inv1 -H 'Content-Type: application/json' \
  -d '{"category":"investments","name":"Index fund","amount":1200,"account":"rothIra","createdAt":"2026-09-27T06:21:00.000Z"}'

curl -H "$AUTH" localhost:8080/api/v1/items
curl -H "$AUTH" 'localhost:8080/api/v1/items?category=income'
curl -H "$AUTH" localhost:8080/api/v1/items/lz3k9abc12
curl -H "$AUTH" -X DELETE localhost:8080/api/v1/items/lz3k9abc12

# Profiles
curl -H "$AUTH" -X PUT localhost:8080/api/v1/profiles/me -H 'Content-Type: application/json' \
  -d '{"name":"Alex","relationship":"self","birthYear":1988,"birthMonth":3,"createdAt":"2026-09-27T06:20:39.033Z"}'
curl -H "$AUTH" localhost:8080/api/v1/profiles
curl -H "$AUTH" localhost:8080/api/v1/profiles/me
curl -H "$AUTH" -X DELETE localhost:8080/api/v1/profiles/me

# Scenarios
curl -H "$AUTH" -X PUT localhost:8080/api/v1/scenarios/house -H 'Content-Type: application/json' \
  -d '{"name":"Buy a house in 2028","itemIds":["lz3k9abc12"],
       "events":[{"id":"ev1","kind":"oneTimeCost","name":"Down payment","year":2,"amount":250000},
                 {"id":"ev2","kind":"incomePause","name":"Sabbatical","year":3,"amount":0,"months":6}],
       "parameters":{"years":20,"returnRate":0.07,"inflation":0.03,"incomeGrowth":0.03,"incomeAdjustment":0,"expenseAdjustment":0},
       "createdAt":"2026-09-27T06:20:39.033Z"}'
curl -H "$AUTH" localhost:8080/api/v1/scenarios
curl -H "$AUTH" localhost:8080/api/v1/scenarios/house
curl -H "$AUTH" -X DELETE localhost:8080/api/v1/scenarios/house

# CORS preflight
curl -i -X OPTIONS localhost:8080/api/v1/items/lz3k9abc12 \
  -H 'Origin: http://localhost:5173' -H 'Access-Control-Request-Method: PUT'
```

## Layout

```
server.go     main: config, DB connect + migrate, HTTP server, graceful shutdown
account/      account profile model, validation, store, handlers
assistant/    LLM chat about the customer's data: prompt, proposed-action validation, handler
auth/         signup/login (password, Apple, Google), sessions, RequireCustomer middleware
config/       environment configuration
db/           pgx pool, the schema baseline (sql/schema.sql) and later migrations (migrations/NNNN_name.sql)
db/sql/       the database as SQL: drop, schema, default data (tax tables), test data
deletions/    remembered deletions of items, profiles and scenarios (shared SQL)
fields/       shared field rules (id, name, createdAt), time format, ValidationError
httpx/        JSON helpers, CORS, body limit, logging and recovery middleware
items/        item model, validation, store, handlers
llm/          provider-neutral LLM chat interface; openai/ and lmstudio/ clients
profiles/     profile model, validation, store, handlers
scenarios/    scenario model, validation, store, handlers
server/       route + middleware wiring; HTTP integration tests
snapshots/    monthly net-worth snapshots: model, validation, store, handlers
taxtables/    US tax tables by year (data/), validation, projection, lookup by ZIP code and tax arithmetic
taxdb/        loads the tax tables into Postgres at startup, and writes them as db/sql/default-data.sql
taxes/        computes taxes for signed-in customers (POST /api/v1/taxes) with taxtables
```
