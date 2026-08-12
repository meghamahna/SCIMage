.PHONY: up down stop restart reset ps logs migrate migrate-down migrate-version test run tenant token fmt hooks-install

# Start service(s) in the background and bring the schema up to date.
# Usage: make up  |  make up SERVICE=postgres
up:
	docker compose up -d $(SERVICE)
	$(MAKE) migrate

# Stop and remove all containers/network. Always whole-project (compose
# doesn't support scoping `down` to one service) — use `stop` for that.
down:
	docker compose down

# Stop service(s) without removing them. Usage: make stop  |  make stop SERVICE=postgres
stop:
	docker compose stop $(SERVICE)

# Restart service(s) in place, keeping data. Usage: make restart  |  make restart SERVICE=postgres
restart:
	docker compose restart $(SERVICE)

# Full reset: wipe volumes and re-initialize from .env. Always whole-project.
reset:
	docker compose down -v
	docker compose up -d
	$(MAKE) migrate

ps:
	docker compose ps

# Usage: make logs  |  make logs SERVICE=postgres
logs:
	docker compose logs -f $(SERVICE)

# Apply every pending migration. `make up` runs this for you; it's here for
# re-running after adding a migration file. Waits for Postgres to be ready
# first, and needs no host-installed migrate binary — see scripts/migrate.sh.
migrate:
	scripts/with-env.sh scripts/migrate.sh up

# Roll back the most recent migration.
migrate-down:
	scripts/with-env.sh scripts/migrate.sh down 1

# Which migration version the database is currently on.
migrate-version:
	scripts/with-env.sh scripts/migrate.sh version

# Loads .env so the integration tests can reach the compose Postgres. They hit
# the real database, never a mock — start it with `make up` first.
#
# Every test gets its own tenant (internal/store) or shares one dedicated to
# the file (internal/scim), and every query is scoped by tenant_id — so the
# store's row-count assertions can't race against the handler tests creating
# users even though both packages' suites now run concurrently against the
# same Postgres.
test:
	scripts/with-env.sh go test ./...

run:
	scripts/with-env.sh go run ./cmd/server

# Usage: make tenant NAME="Acme Corp"
tenant:
	scripts/with-env.sh go run ./cmd/scimage-admin tenant create -name "$(NAME)"

# Usage: make token TENANT=tenant_xxx LABEL="Okta prod"  [EXPIRES=90d]
token:
	scripts/with-env.sh go run ./cmd/scimage-admin token issue -tenant "$(TENANT)" -label "$(LABEL)" $(if $(EXPIRES),-expires "$(EXPIRES)")

fmt:
	gofmt -w .
	goimports -w .

hooks-install:
	git config core.hooksPath .githooks
