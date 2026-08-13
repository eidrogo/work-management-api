SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help
.PHONY: help fmt fmt-check tidy tidy-check mod-verify vet test test-race test-db test-db-race lint build openapi-check secret-check compose-config-check clean-check verify verify-full compose-smoke migrate seed api worker up down reset logs demo docker-build
help:
	@printf '%s\n' 'make up - start the complete stack' 'make demo - run the documented flow' 'make verify - quick checks (DB-gated tests may skip)' 'make verify-full - authoritative clean-tree Compose verification'
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')
fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || (gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)
tidy:
	go mod tidy
tidy-check:
	go mod tidy -diff
mod-verify:
	go mod verify
vet:
	CGO_ENABLED=0 go vet ./...
test:
	CGO_ENABLED=0 go test -count=1 ./...
test-race:
	go test -race -count=1 ./...
test-db:
	@test -n "$${TEST_DATABASE_URL:-}" || (echo 'TEST_DATABASE_URL is required'; exit 1)
	CGO_ENABLED=0 go test -count=1 -v ./...
test-db-race:
	@test -n "$${TEST_DATABASE_URL:-}" || (echo 'TEST_DATABASE_URL is required'; exit 1)
	go test -race -count=1 -v ./...
lint:
	@command -v golangci-lint >/dev/null || (echo 'golangci-lint is not installed'; exit 1)
	golangci-lint run ./...
build:
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/api ./cmd/api
	CGO_ENABLED=0 go build -o bin/worker ./cmd/worker
	CGO_ENABLED=0 go build -o bin/migrate ./cmd/migrate
	CGO_ENABLED=0 go build -o bin/seed ./cmd/seed
	CGO_ENABLED=0 go build -o bin/replay-notification ./cmd/replay-notification
openapi-check:
	CGO_ENABLED=0 go test ./internal/contract -run TestOpenAPIContract -count=1
secret-check:
	./scripts/check-secrets.sh
compose-config-check:
	./scripts/check-compose-config.sh
clean-check:
	git diff --check
	git diff --exit-code -- .
	git diff --cached --exit-code -- .
	@test -z "$$(git status --porcelain --untracked-files=all)" || (git status --short --untracked-files=all; exit 1)
verify: fmt-check mod-verify tidy-check vet test test-race openapi-check secret-check build
verify-full: fmt-check mod-verify tidy-check vet lint build openapi-check secret-check compose-config-check docker-build
	./scripts/verify-compose.sh
	$(MAKE) clean-check
compose-smoke:
	./scripts/verify-compose.sh
migrate:
	go run ./cmd/migrate up
seed:
	go run ./cmd/seed
api:
	go run ./cmd/api
worker:
	go run ./cmd/worker
up:
	docker compose up --build -d
down:
	docker compose down
reset:
	docker compose down --volumes --remove-orphans
logs:
	docker compose logs --follow api worker
demo:
	./scripts/demo.sh
docker-build:
	docker build -t work-management-api:local .
