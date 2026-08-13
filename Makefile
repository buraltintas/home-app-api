.PHONY: run worker migrate migrate-down seed test lint build

run:
	go run ./cmd/api

worker:
	go run ./cmd/worker

migrate:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

seed:
	go run ./cmd/seed

test:
	go test -race ./...

lint:
	go vet ./...

build:
	go build ./cmd/...
