.PHONY: up down stop restart reset ps logs migrate test run fmt hooks-install

# Start service(s) in the background. Usage: make up  |  make up SERVICE=postgres
up:
	docker compose up -d $(SERVICE)

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

ps:
	docker compose ps

# Usage: make logs  |  make logs SERVICE=postgres
logs:
	docker compose logs -f $(SERVICE)

migrate:
	migrate -path migrations -database "$(DATABASE_URL)" up

test:
	go test ./...

run:
	go run ./cmd/server

fmt:
	gofmt -w .
	goimports -w .

hooks-install:
	git config core.hooksPath .githooks
