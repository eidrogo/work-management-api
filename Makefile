SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help
.PHONY: help fmt fmt-check tidy vet test test-race lint build openapi-check secret-check verify verify-full compose-smoke migrate seed api worker up down reset logs demo docker-build
help:
	@printf '%s\n' 'make up - start the complete stack' 'make demo - run the documented flow' 'make verify - static/unit verification'
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')
fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || (gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)
tidy:
	go mod tidy
vet:
	go vet ./...
test:
	go test ./...
test-race:
	go test -race ./...
lint:
	@command -v golangci-lint >/dev/null || (echo 'golangci-lint is not installed'; exit 1)
	golangci-lint run ./...
build:
	mkdir -p bin
	CGO_ENABLED=1 go build -o bin/api ./cmd/api
	CGO_ENABLED=1 go build -o bin/worker ./cmd/worker
	CGO_ENABLED=1 go build -o bin/migrate ./cmd/migrate
	CGO_ENABLED=1 go build -o bin/seed ./cmd/seed
	CGO_ENABLED=1 go build -o bin/replay-notification ./cmd/replay-notification
openapi-check:
	go test ./internal/contract -run TestOpenAPIContract -count=1
secret-check:
	./scripts/check-secrets.sh
verify: fmt-check vet test test-race openapi-check secret-check build
verify-full: verify lint docker-build compose-smoke
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
