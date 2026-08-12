# Colors for status lines below. ESC is a real escape byte generated once via
# shell printf, not a backslash sequence for the recipe shell to reinterpret,
# so this stays portable across whatever /bin/sh a recipe runs under.
ESC    := $(shell printf '\033')
BOLD   := $(ESC)[1m
RESET  := $(ESC)[0m
BLUE   := $(ESC)[34m
GREEN  := $(ESC)[32m
YELLOW := $(ESC)[33m
CYAN   := $(ESC)[36m

.PHONY: help up down stop restart reset ps logs migrate migrate-down migrate-version test run tenant token fmt hooks-install

# Bare `make` shows the command list instead of silently running the first
# target, which used to be `up`.
.DEFAULT_GOAL := help

## Show this command list.
help:
	@printf "$(BOLD)SCIMage$(RESET)\n"
	@awk 'BEGIN {FS = ":.*##"} \
	  /^##@/ { printf "\n$(BOLD)%s$(RESET)\n", substr($$0, 5); next } \
	  /^[a-zA-Z_-]+:.*##/ { sub(/^ /, "", $$2); printf "  $(CYAN)%-20s$(RESET) %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@printf "\n"

##@ Database

# Start service(s) in the background and bring the schema up to date.
# Usage: make up  |  make up SERVICE=postgres
up: ## Start service(s) and apply migrations
	@printf "$(BLUE)==>$(RESET) starting $(or $(SERVICE),every service)\n"
	@docker compose up -d $(SERVICE)
	@$(MAKE) --no-print-directory migrate
	@printf "$(GREEN)==>$(RESET) up\n"

# Stop and remove all containers/network. Always whole-project (compose
# doesn't support scoping `down` to one service); use `stop` for that.
down: ## Stop and remove all containers/network (always whole-project)
	@printf "$(BLUE)==>$(RESET) tearing down\n"
	@docker compose down
	@printf "$(GREEN)==>$(RESET) down\n"

# Stop service(s) without removing them. Usage: make stop  |  make stop SERVICE=postgres
stop: ## Stop service(s) without removing them
	@printf "$(BLUE)==>$(RESET) stopping $(or $(SERVICE),every service)\n"
	@docker compose stop $(SERVICE)

# Restart service(s) in place, keeping data. Usage: make restart  |  make restart SERVICE=postgres
restart: ## Restart service(s) in place, keeping data
	@printf "$(BLUE)==>$(RESET) restarting $(or $(SERVICE),every service)\n"
	@docker compose restart $(SERVICE)

# Full reset: wipe volumes and re-initialize from .env. Always whole-project.
reset: ## Wipe the data volume and re-initialize from .env
	@printf "$(YELLOW)==>$(RESET) wiping the data volume\n"
	@docker compose down -v
	@docker compose up -d
	@$(MAKE) --no-print-directory migrate
	@printf "$(GREEN)==>$(RESET) reset\n"

ps: ## Show what's running
	@docker compose ps

# Usage: make logs  |  make logs SERVICE=postgres
logs: ## Follow logs
	@docker compose logs -f $(SERVICE)

##@ Migrations

# Apply every pending migration. `make up` runs this for you; it's here for
# re-running after adding a migration file. Waits for Postgres to be ready
# first, and needs no host-installed migrate binary; see scripts/migrate.sh.
migrate: ## Apply every pending migration
	@printf "$(BLUE)==>$(RESET) applying migrations\n"
	@scripts/with-env.sh scripts/migrate.sh up

migrate-down: ## Roll back the most recent migration
	@printf "$(YELLOW)==>$(RESET) rolling back one migration\n"
	@scripts/with-env.sh scripts/migrate.sh down 1

migrate-version: ## Show which migration version the database is on
	@scripts/with-env.sh scripts/migrate.sh version

##@ Build and test

# Loads .env so the integration tests can reach the compose Postgres. They hit
# the real database, never a mock; start it with `make up` first.
#
# Every test gets its own tenant (internal/store) or shares one dedicated to
# the file (internal/scim), and every query is scoped by tenant_id, so the
# store's row-count assertions can't race against the handler tests creating
# users even though both packages' suites now run concurrently against the
# same Postgres.
test: ## Run the full test suite against a real Postgres
	@printf "$(BLUE)==>$(RESET) running tests\n"
	@scripts/with-env.sh go test ./...

run: ## Run the SCIM server
	@printf "$(BLUE)==>$(RESET) starting the server\n"
	@scripts/with-env.sh go run ./cmd/server

fmt: ## Format every Go file
	@gofmt -w .
	@goimports -w .
	@printf "$(GREEN)==>$(RESET) formatted\n"

##@ Tenants and tokens

# Usage: make tenant NAME="Acme Corp"
tenant: ## Create a tenant
	@scripts/with-env.sh go run ./cmd/scimage-admin tenant create -name "$(NAME)"

# Usage: make token TENANT=tenant_xxx LABEL="Okta prod"  [EXPIRES=90d]
token: ## Issue a token for a tenant
	@scripts/with-env.sh go run ./cmd/scimage-admin token issue -tenant "$(TENANT)" -label "$(LABEL)" $(if $(EXPIRES),-expires "$(EXPIRES)")

##@ Setup

hooks-install: ## One-time per clone: activate the real git pre-commit hook
	@git config core.hooksPath .githooks
	@printf "$(GREEN)==>$(RESET) pre-commit hook installed\n"
