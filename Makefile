SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help
.PHONY: help fmt fmt-check tidy tidy-check mod-verify vet test test-race test-db test-db-race lint build openapi-check secret-check dockerfile-syntax-check dockerfile-syntax-test runtime-dockerfile-check runtime-dockerfile-test runtime-image-check docker-image-build up-cleanup-test compose-config-check verify-signal-check clean-check verify verify-full compose-smoke migrate seed api worker up down reset logs demo docker-build
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
	CGO_ENABLED=0 go build -o bin/healthcheck ./cmd/healthcheck
openapi-check:
	CGO_ENABLED=0 go test ./internal/contract -run TestOpenAPIContract -count=1
secret-check:
	./scripts/check-secrets.sh
dockerfile-syntax-check:
	./scripts/check-dockerfile-syntax.sh
dockerfile-syntax-test: dockerfile-syntax-check
	./scripts/test-dockerfile-syntax-check.sh
runtime-dockerfile-check:
	./scripts/check-runtime-dockerfile.sh
runtime-dockerfile-test: runtime-dockerfile-check
	./scripts/test-runtime-dockerfile-check.sh
runtime-image-check: docker-image-build
	./scripts/check-runtime-image.sh work-management-api:local
up-cleanup-test:
	./scripts/test-up-runtime-check-cleanup.sh
compose-config-check:
	./scripts/check-compose-config.sh
verify-signal-check:
	VERIFY_SIGNAL_TEST_CHILD_EXIT=42 ./scripts/test-verify-compose-signals.sh
	VERIFY_SIGNAL_TEST_SIGNAL=TERM ./scripts/test-verify-compose-signals.sh
	VERIFY_SIGNAL_TEST_SIGNAL=INT ./scripts/test-verify-compose-signals.sh
clean-check:
	git diff --check
	git diff --exit-code -- .
	git diff --cached --exit-code -- .
	@test -z "$$(git status --porcelain --untracked-files=all)" || (git status --short --untracked-files=all; exit 1)
verify: fmt-check mod-verify tidy-check vet test test-race openapi-check secret-check dockerfile-syntax-test runtime-dockerfile-test build
verify-full: fmt-check mod-verify tidy-check vet lint build openapi-check secret-check dockerfile-syntax-test runtime-dockerfile-test compose-config-check docker-build
	$(MAKE) up-cleanup-test
	$(MAKE) compose-smoke
	$(MAKE) clean-check
compose-smoke: dockerfile-syntax-check runtime-dockerfile-check verify-signal-check
	./scripts/verify-compose.sh
migrate:
	go run ./cmd/migrate up
seed:
	go run ./cmd/seed
api:
	go run ./cmd/api
worker:
	go run ./cmd/worker
up: dockerfile-syntax-check runtime-dockerfile-check
	docker compose build
	@set -Eeuo pipefail; \
	project="$$(docker compose config --format json | jq -er '.name')"; \
	image_ref="$$(docker image ls -q --no-trunc --filter "label=com.docker.compose.project=$$project" --filter 'label=com.docker.compose.service=api')"; \
	count="$$(printf '%s\n' "$$image_ref" | sed '/^[[:space:]]*$$/d' | wc -l | tr -d '[:space:]')"; \
	if [[ "$$count" != "1" ]] || [[ ! "$$image_ref" =~ ^sha256:[0-9a-f]{64}$$ ]]; then echo "Could not resolve exactly one built application image reference: $$image_ref" >&2; exit 1; fi; \
	image="$$(docker image inspect --format '{{.Id}}' "$$image_ref")"; \
	if [[ ! "$$image" =~ ^sha256:[0-9a-f]{64}$$ ]]; then echo "Built application image has an invalid ID: $$image" >&2; exit 1; fi; \
	./scripts/check-runtime-image.sh "$$image"; \
	docker compose up --no-build -d
down:
	docker compose down
reset:
	docker compose down --volumes --remove-orphans
logs:
	docker compose logs --follow api worker
demo:
	./scripts/demo.sh
docker-image-build: dockerfile-syntax-check runtime-dockerfile-check
	docker build -t work-management-api:local .
docker-build: runtime-image-check
