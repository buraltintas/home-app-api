.PHONY: run worker migrate migrate-up migrate-down seed rebuild-admin-metrics privacy-maintenance test test-race vet lint build integration-test provider-smoke smoke-test

run:
	go run ./cmd/api

worker:
	go run ./cmd/worker

migrate:
	go run ./cmd/migrate up

migrate-up: migrate

migrate-down:
	go run ./cmd/migrate down

seed:
	go run ./cmd/seed

rebuild-admin-metrics:
	go run ./cmd/admin-metrics rebuild

privacy-maintenance:
	go run ./cmd/privacy-maintenance

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint: vet

build:
	go build ./cmd/...

integration-test:
	test -n "$$TEST_DATABASE_URL" || (echo "TEST_DATABASE_URL is required"; exit 1)
	go test -tags=integration -count=1 -timeout=3m ./internal/integration

provider-smoke:
	go test -tags=provider -count=1 -timeout=2m ./internal/provider

smoke-test:
	./scripts/smoke.sh
