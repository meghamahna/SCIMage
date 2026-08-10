.PHONY: up down migrate test run fmt hooks-install

up:
	docker compose up -d

down:
	docker compose down

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
