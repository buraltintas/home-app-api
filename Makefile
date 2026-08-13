.PHONY: run worker migrate migrate-down seed rebuild-admin-metrics privacy-maintenance test lint build

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

rebuild-admin-metrics:
	go run ./cmd/admin-metrics rebuild

privacy-maintenance:
	go run ./cmd/privacy-maintenance

test:
	go test -race ./...

lint:
	go vet ./...

build:
	go build ./cmd/...
