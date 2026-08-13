# Verification Evidence

All commands below were executed locally during Task 4 across 2026-08-12 and 2026-08-13 (America/Argentina/Buenos_Aires). PASS sections use the candidate/final code; deliberate FAIL sections capture the named gap before its correction. Output excerpts are copied from those commands; durations, generated UUIDs, project names, and ephemeral ports are omitted where they are not stable contract evidence.

## PASS — tool environment

Command:

```bash
go version
docker version --format 'Docker client {{.Client.Version}}; server {{.Server.Version}}'
docker compose version
/tmp/work-management-task2-bin/golangci-lint version
jq --version
curl --version | sed -n '1p'
git --version
```

Evidence:

```text
go version go1.26.5 darwin/arm64
Docker client 29.5.2; server 29.5.2
Docker Compose version v5.1.3
golangci-lint has version 2.12.2 built with go1.26.5
jq-1.7.1-apple
curl 8.7.1 (x86_64-apple-darwin25.0)
git version 2.50.1 (Apple Git-155)
```

## FAIL — audited base allowed skipped database tests

Command:

```bash
set -o pipefail
if CGO_ENABLED=0 go test -count=1 -v ./... 2>&1 | tee "$LOG" |
  grep -Eq -- '--- SKIP:|TEST_DATABASE_URL is not configured'; then
  echo 'RED: database tests were skipped'
  exit 1
fi
```

Evidence:

```text
integration_test.go:25: TEST_DATABASE_URL is not configured
--- SKIP: TestDocumentedHTTPFlow (0.00s)
integration_test.go:57: TEST_DATABASE_URL is not configured
--- SKIP: TestCreateIdempotencyHTTPReturnsOriginalETagAndBody (0.00s)
RED: database tests were skipped
```

Correction: the authoritative verifier now starts fresh PostgreSQL first, exports its dynamic loopback URL, consumes `go test -json`, and rejects every test-level `Action == "skip"` plus textual skip markers.

## FAIL — audited demo poll false positive

Command:

```bash
DEMO_POLL_ATTEMPTS=1 DEMO_POLL_INTERVAL=0 \
API_URL="$FAKE_API_URL" API_ADMIN_URL="$FAKE_ADMIN_URL" \
MAILPIT_URL="$FAKE_MAILPIT_URL" ./scripts/demo.sh
```

Evidence:

```text
Demo completed successfully for work item aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
RED: demo reported success after notification polls exhausted
```

Correction: one bounded polling helper now returns nonzero after exhaustion, and the live demo selects only the current unique message and durable notification.

## FAIL then PASS — HTTP contract defects

Command:

```bash
CGO_ENABLED=0 go test ./internal/httpapi \
  -run 'TestHTTPPublicMethodsErrorsAndHeaders|TestHTTPRejectsMalformedBodiesAndPreconditionsBeforeMutation|TestHTTPMapsStableDomainErrorsWithoutLeakingCauses' \
  -count=1 -v
```

Initial evidence:

```text
server_test.go:87: Idempotent-Replayed="" want="true"
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders
server_test.go:213: status=422 code="INTERNAL_ERROR"
--- FAIL: TestHTTPMapsStableDomainErrorsWithoutLeakingCauses
FAIL
```

Passing rerun:

```text
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders
--- PASS: TestHTTPRejectsMalformedBodiesAndPreconditionsBeforeMutation
--- PASS: TestHTTPMapsStableDomainErrorsWithoutLeakingCauses
PASS
ok  github.com/eidrogo/work-management-api/internal/httpapi
```

## FAIL then PASS — exhaustive public OpenAPI contract

Command:

```bash
CGO_ENABLED=0 go test ./internal/contract -run '^TestOpenAPIContract$' -count=1 -v
```

Initial evidence:

```text
GET /v1/work-items responses=[200 401 403 400 500] want=[200 400 401 403 500 503]
GET /v1/work-items 401 omits WWW-Authenticate
schema additionalProperties=<nil>
POST /v1/work-items/{work_item_id}/start 200 omits Idempotent-Replayed
POST /v1/work-items/{work_item_id}/complete 200 omits Idempotent-Replayed
--- FAIL: TestOpenAPIContract
FAIL
```

Passing rerun:

```text
=== RUN   TestOpenAPIContract
--- PASS: TestOpenAPIContract
PASS
ok  github.com/eidrogo/work-management-api/internal/contract
```

## PASS — real SMTP transport boundaries

Command:

```bash
CGO_ENABLED=0 go test ./internal/platform/email \
  -run 'TestSMTPClientNegotiatesSTARTTLS|TestSMTPClientNegotiatesImplicitTLS|TestSMTPClientRejectsUntrustedCertificate|TestSMTPClientClassifiesRealAUTHFailure|TestSMTPClientTimeoutIsBoundedAndRetryable|TestSMTPClientRejectsHeaderInjectionBeforeDial' \
  -count=1 -v
```

Evidence:

```text
--- PASS: TestSMTPClientNegotiatesSTARTTLS
--- PASS: TestSMTPClientNegotiatesImplicitTLS
--- PASS: TestSMTPClientRejectsUntrustedCertificate
    --- PASS: TestSMTPClientRejectsUntrustedCertificate/starttls
    --- PASS: TestSMTPClientRejectsUntrustedCertificate/tls
--- PASS: TestSMTPClientClassifiesRealAUTHFailure
--- PASS: TestSMTPClientTimeoutIsBoundedAndRetryable
--- PASS: TestSMTPClientRejectsHeaderInjectionBeforeDial
PASS
ok  github.com/eidrogo/work-management-api/internal/platform/email
```

These are SMTP protocol, TLS, and authentication results; they are not external inbox-delivery evidence.

## PASS — authentication, schema, and PostgreSQL-backed HTTP boundaries

Command:

```bash
TEST_DATABASE_URL="$TEST_DATABASE_URL" CGO_ENABLED=0 go test -count=1 -v \
  ./internal/auth ./internal/migrations ./internal/httpapi
```

Evidence:

```text
--- PASS: TestPostgresTokenStoreHonorsExpiry
PASS
ok  github.com/eidrogo/work-management-api/internal/auth
--- PASS: TestInitialDownMigrationDropsTask2Objects
--- PASS: TestDatabaseGuardsWorkItemAndAuditInvariants
PASS
ok  github.com/eidrogo/work-management-api/internal/migrations
--- PASS: TestHTTPManagerWorkerLifecycleAndNotificationContract
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders
--- PASS: TestHTTPRejectsMalformedBodiesAndPreconditionsBeforeMutation
--- PASS: TestHTTPMapsStableDomainErrorsWithoutLeakingCauses
PASS
ok  github.com/eidrogo/work-management-api/internal/httpapi
```

## PASS — static, dependency, build, contract, secret, Compose-config, image, and lint gates

Commands:

```bash
PATH=/tmp/work-management-task2-bin:$PATH \
  make fmt-check mod-verify tidy-check vet lint build openapi-check \
       secret-check compose-config-check docker-build
```

Evidence:

```text
all modules verified
ok  github.com/eidrogo/work-management-api/internal/contract
No high-confidence secret patterns found.
#19 naming to docker.io/library/work-management-api:local done
0 issues.
```

## PASS — injected-failure and signal cleanup propagation

Commands:

```bash
set +e
VERIFY_COMPOSE_PROJECT="$PROJECT" \
VERIFY_COMPOSE_INJECT_FAILURE=after-postgres \
./scripts/verify-compose.sh
injected_rc=$?
set -e
test "$injected_rc" = 97

test -z "$({
  docker ps -aq --filter "label=com.docker.compose.project=$PROJECT"
  docker network ls -q --filter "label=com.docker.compose.project=$PROJECT"
  docker volume ls -q --filter "label=com.docker.compose.project=$PROJECT"
} | sed '/^$/d')"

VERIFY_COMPOSE_PROJECT="$SIGNAL_PROJECT" ./scripts/verify-compose.sh >"$LOG" 2>&1 &
pid=$!
# After the PostgreSQL PASS line appeared:
kill -TERM "$pid"
set +e
wait "$pid"
signal_rc=$?
set -e
test "$signal_rc" = 143
test -z "$({
  docker ps -aq --filter "label=com.docker.compose.project=$SIGNAL_PROJECT"
  docker network ls -q --filter "label=com.docker.compose.project=$SIGNAL_PROJECT"
  docker volume ls -q --filter "label=com.docker.compose.project=$SIGNAL_PROJECT"
} | sed '/^$/d')"
```

Evidence:

```text
Injected verification failure after PostgreSQL startup
PASS: Compose cleanup removed all project containers, networks, and volumes
PASS: injected verification failure propagated with exit 97 and zero labeled resources
PASS: Compose cleanup removed all project containers, networks, and volumes
PASS: SIGTERM propagated as exit 143 and left zero labeled resources
```

## PASS — complete fresh Compose authority

Command:

```bash
VERIFY_LOG_DIR="$LOG_DIR" ./scripts/verify-compose.sh
```

Evidence:

```text
PASS: verification Compose config is valid
PASS: PostgreSQL test database is fresh and loopback-published
PASS: go-test-normal completed with zero skips (120 top-level tests; 17 packages)
PASS: go-test-race completed with zero skips (120 top-level tests; 17 packages)
PASS: migrate and seed completed successfully
PASS: all six published ports are ephemeral IPv4 loopback mappings
PASS: API and worker admin live, ready, and metrics endpoints respond
PASS: public API serves business routes and excludes operational routes
PASS: API admin readiness
PASS: manager created current work item
PASS: manager assigned Alice
PASS: manager progress denied and Bob cannot discover the item
PASS: Alice started and completed the item with ordered history
PASS: Mailpit accepted exactly one current SMTP transaction
PASS: durable SENT detail matches the current Mailpit Message-ID and attempt
PASS: live manager/worker/Mailpit/durable-state demo completed
PASS: authoritative Compose verification completed
PASS: Compose cleanup removed all project containers, networks, and volumes
```
