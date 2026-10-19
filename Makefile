# Mustard customer-data backend.
# Uses plain `docker run` (no docker compose required).

DB_CONTAINER ?= mustard-postgres
DB_VOLUME    ?= mustard-postgres-data
DB_IMAGE     ?= postgres:17
DB_PORT      ?= 5432
DB_USER      ?= mustard
DB_PASSWORD  ?= mustard
DB_NAME      ?= mustard
TEST_DB_NAME ?= mustard_test

# Jaeger for traces (see README, "Tracing"). Traces are kept in memory.
JAEGER_CONTAINER ?= mustard-jaeger
JAEGER_IMAGE     ?= jaegertracing/jaeger:2.22.0
JAEGER_UI_PORT   ?= 16686
JAEGER_OTLP_PORT ?= 4318

DATABASE_URL      ?= postgres://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable
TEST_DATABASE_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(TEST_DB_NAME)?sslmode=disable

.PHONY: help db db-stop db-wait db-test db-reset default-data jaeger jaeger-stop jaeger-restart test-accounts test-accounts-remove run build test test-integration lint fmt

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

db: ## Start Postgres in Docker (idempotent)
	@if [ -n "$$(docker ps -q --filter name=^/$(DB_CONTAINER)$$)" ]; then \
		echo "$(DB_CONTAINER) is already running"; \
	elif [ -n "$$(docker ps -aq --filter name=^/$(DB_CONTAINER)$$)" ]; then \
		echo "starting existing container $(DB_CONTAINER)"; \
		docker start $(DB_CONTAINER) >/dev/null; \
	else \
		echo "creating container $(DB_CONTAINER) ($(DB_IMAGE))"; \
		docker run -d --name $(DB_CONTAINER) \
			-e POSTGRES_USER=$(DB_USER) \
			-e POSTGRES_PASSWORD=$(DB_PASSWORD) \
			-e POSTGRES_DB=$(DB_NAME) \
			-p $(DB_PORT):5432 \
			-v $(DB_VOLUME):/var/lib/postgresql/data \
			$(DB_IMAGE) >/dev/null; \
	fi
	@$(MAKE) --no-print-directory db-wait

db-wait:
	@# Check over TCP: during first-time init Postgres only listens on the unix socket.
	@for i in $$(seq 1 60); do \
		if docker exec $(DB_CONTAINER) pg_isready -q -h 127.0.0.1 -U $(DB_USER) -d $(DB_NAME); then \
			echo "postgres is ready on localhost:$(DB_PORT)"; exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "postgres did not become ready in time" >&2; exit 1

db-stop: ## Stop the Postgres container (data is kept in the volume)
	@docker stop $(DB_CONTAINER) >/dev/null 2>&1 && echo "stopped $(DB_CONTAINER)" || echo "$(DB_CONTAINER) is not running"

db-test: db ## Create the test database if needed
	@docker exec $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -tAc \
		"SELECT 1 FROM pg_database WHERE datname = '$(TEST_DB_NAME)'" | grep -q 1 \
		|| (docker exec $(DB_CONTAINER) createdb -U $(DB_USER) $(TEST_DB_NAME) && echo "created database $(TEST_DB_NAME)")

jaeger: ## Start Jaeger in Docker (idempotent): UI on JAEGER_UI_PORT, OTLP/HTTP on JAEGER_OTLP_PORT
	@if [ -n "$$(docker ps -q --filter name=^/$(JAEGER_CONTAINER)$$)" ]; then \
		echo "$(JAEGER_CONTAINER) is already running"; \
	elif [ -n "$$(docker ps -aq --filter name=^/$(JAEGER_CONTAINER)$$)" ]; then \
		echo "starting existing container $(JAEGER_CONTAINER)"; \
		docker start $(JAEGER_CONTAINER) >/dev/null; \
	else \
		echo "creating container $(JAEGER_CONTAINER) ($(JAEGER_IMAGE))"; \
		docker run -d --name $(JAEGER_CONTAINER) \
			-p $(JAEGER_UI_PORT):16686 \
			-p $(JAEGER_OTLP_PORT):4318 \
			$(JAEGER_IMAGE) >/dev/null; \
	fi
	@for i in $$(seq 1 30); do \
		if curl -fsS -o /dev/null http://localhost:$(JAEGER_UI_PORT)/ 2>/dev/null; then \
			echo "jaeger is ready: UI http://localhost:$(JAEGER_UI_PORT), OTLP http://localhost:$(JAEGER_OTLP_PORT)"; exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "jaeger did not answer on localhost:$(JAEGER_UI_PORT) (is Docker publishing ports?)" >&2; exit 1

jaeger-stop: ## Stop Jaeger (its traces are kept in memory, so they're lost)
	@docker stop $(JAEGER_CONTAINER) >/dev/null 2>&1 && echo "stopped $(JAEGER_CONTAINER)" || echo "$(JAEGER_CONTAINER) is not running"

jaeger-restart: ## Recreate Jaeger: a fresh container with no traces (also picks up a new JAEGER_IMAGE or ports)
	@docker rm -f $(JAEGER_CONTAINER) >/dev/null 2>&1 || true
	@$(MAKE) --no-print-directory jaeger

db-reset: ## DESTRUCTIVE: empty DB_NAME and rebuild it from db/sql (drop, schema, default data); dev databases only
	@for f in drop schema default-data; do \
		docker exec -i $(DB_CONTAINER) psql -q -v ON_ERROR_STOP=1 -U $(DB_USER) -d $(DB_NAME) -1 < db/sql/$$f.sql || exit 1; \
	done
	@echo "$(DB_NAME) rebuilt from db/sql; add test accounts with: make test-accounts"

default-data: ## Regenerate db/sql/default-data.sql (the tax tables) from taxtables/data
	go test ./taxdb -run TestDefaultData -update

test-accounts: ## Add test0001..test0100@mustard.money (password "mustard"); dev databases only
	@docker exec -i $(DB_CONTAINER) psql -q -v ON_ERROR_STOP=1 -U $(DB_USER) -d $(DB_NAME) < db/sql/test-data.sql
	@docker exec $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -tAc \
		"SELECT count(*) || ' test accounts in $(DB_NAME)' FROM customers WHERE email ~ '^test[0-9]{4}@mustard\.money$$'"

test-accounts-remove: ## Remove those test accounts and everything they saved
	@docker exec -i $(DB_CONTAINER) psql -q -v ON_ERROR_STOP=1 -U $(DB_USER) -d $(DB_NAME) < db/sql/test-data-remove.sql
	@docker exec $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -tAc \
		"SELECT count(*) || ' test accounts in $(DB_NAME)' FROM customers WHERE email ~ '^test[0-9]{4}@mustard\.money$$'"

run: ## Run the API server
	DATABASE_URL='$(DATABASE_URL)' go run .

build: ## Build the server binary into bin/
	go build -o bin/server .

test: ## Unit tests (integration tests skip without TEST_DATABASE_URL)
	go test ./...

test-integration: db-test ## Unit + integration tests against the mustard_test database
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -count=1 ./...

lint: ## go vet
	go vet ./...

fmt: ## gofmt
	gofmt -w .
