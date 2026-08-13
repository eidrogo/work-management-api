# Verification Evidence

All commands below were executed locally during Task 4 and the final whole-branch reliability pass across 2026-08-12 and 2026-08-13 (America/Argentina/Buenos_Aires). PASS sections use the candidate/final code; deliberate FAIL sections capture the named gap before its correction. Output excerpts are copied from those commands; durations, generated UUIDs, project names, and ephemeral ports are omitted where they are not stable contract evidence.

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

## FAIL then PASS — independent authentication hash boundary

Command:

```bash
CGO_ENABLED=0 go test ./internal/auth \
  -run 'TestHashTokenIsDeterministic|TestAuthenticateHashesTokenAndMapsStoreFailures' \
  -count=1 -v
```

With `HashToken` temporarily mutated to hash a prefixed token, the fixed test failed against the independent known SHA-256 digest and the exact store argument:

```text
auth_test.go:51: hash="54b3ec7ead5c67b8ed002806161f7550b3451b5fa3d358a40268475d9925246f"
auth_test.go:16: HashToken()="54b3ec7ead5c67b8ed002806161f7550b3451b5fa3d358a40268475d9925246f" want SHA-256 "2fb45d55493f2a01708bf8079a547104e5d60bd027b54ec838971fd0ecbedb54"
FAIL
RED CONFIRMED: SHA-256 mutation rejected with test exit 1
```

After restoring the SHA-256 implementation:

```text
--- PASS: TestHashTokenIsDeterministic
--- PASS: TestAuthenticateHashesTokenAndMapsStoreFailures
PASS
ok  github.com/eidrogo/work-management-api/internal/auth
```

## FAIL then PASS — exact trailing-slash route rejection

Command:

```bash
CGO_ENABLED=0 go test ./internal/httpapi \
  -run '^TestHTTPPublicMethodsErrorsAndHeaders$' -count=1 -v
```

Before the exact-route guard, authenticated dynamic item, notification, and action aliases reached the application and violated the stable 404 expectation:

```text
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/work_item_trailing_slash
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/assignment_trailing_slash
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/start_trailing_slash
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/complete_trailing_slash
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/history_trailing_slash
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/notification_trailing_slash
--- FAIL: TestHTTPPublicMethodsErrorsAndHeaders/notification_replay_trailing_slash
FAIL
```

The passing rerun covered all three collections, both dynamic resource families, and every public action; every case asserted a correlated 404 and zero application calls:

```text
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/work_item_collection_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/work_item_create_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/own_work_item_collection_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/work_item_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/assignment_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/start_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/complete_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/history_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/notification_collection_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/notification_trailing_slash
--- PASS: TestHTTPPublicMethodsErrorsAndHeaders/notification_replay_trailing_slash
PASS
```

## FAIL then PASS — fail-closed OpenAPI header references

Commands:

```bash
CGO_ENABLED=0 go test ./internal/contract -count=1 -v
CGO_ENABLED=0 go test ./internal/contract \
  -run 'TestOpenAPIContract|TestOpenAPIRequiredHeadersFailClosed' \
  -count=1 -v
```

Changing every `#/components/headers/XRequestID` reference to a nonexistent local component still passed the prior contract, proving the false positive:

```text
--- PASS: TestOpenAPIContract
PASS
RED CONFIRMED: dangling XRequestID references still passed the existing OpenAPI contract
```

The repaired gate recursively resolves required header objects and schemas and rejects dangling, cyclic, non-local, and malformed references. It also pins the X-Request-ID bounds, exact bearer challenge, quoted positive ETag pattern, and replay value:

```text
--- PASS: TestOpenAPIRequiredHeadersFailClosed/dangling_local_ref
--- PASS: TestOpenAPIRequiredHeadersFailClosed/cyclic_local_refs
--- PASS: TestOpenAPIRequiredHeadersFailClosed/nonlocal_ref
--- PASS: TestOpenAPIRequiredHeadersFailClosed/malformed_local_ref
--- PASS: TestOpenAPIRequiredHeadersFailClosed/request_id_semantics
--- PASS: TestOpenAPIRequiredHeadersFailClosed/authentication_semantics
--- PASS: TestOpenAPIRequiredHeadersFailClosed/entity_tag_semantics
--- PASS: TestOpenAPIRequiredHeadersFailClosed/idempotency_semantics
PASS
```

Repeating the dangling-reference mutation then failed every affected operation with `missing segment "DOES_NOT_EXIST"` and exited 1.

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

## PASS — prompt owned-child signal and cleanup propagation

Commands:

```bash
make verify-signal-check

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
# After the PostgreSQL PASS line and owned Go test PID/PGID appeared:
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
PASS: owned test child status 42 propagated unchanged and cleanup reaped the child
PASS: SIGTERM reached the owned test process group, reaped it, and began Compose cleanup promptly (3 polls)
PASS: SIGINT reached the owned test process group, reaped it, and began Compose cleanup promptly (3 polls)
Injected verification failure after PostgreSQL startup
PASS: Compose cleanup removed all project containers, networks, and volumes
exit=97
containers=0
networks=0
volumes=0
PASS: injected exit 97 propagated and cleanup left zero labeled resources
PASS: Compose cleanup removed all project containers, networks, and volumes
child_pid=65339
child_pgid=65339
exit=143
polls_10ms=42
containers=0
networks=0
volumes=0
PASS: real SIGTERM promptly reaped owned test group and cleaned Compose (42 10ms polls)
```

The deterministic regression runs through the same generic owned-child wrapper used for both Compose startup phases, normal/race Go suites, and the live demo. `compose-smoke` depends on this regression, while `verify-full` reaches both the regression and Compose authority exactly once through `compose-smoke`.

## PASS — complete fresh Compose authority

Command:

```bash
VERIFY_LOG_DIR="$LOG_DIR" ./scripts/verify-compose.sh
```

Evidence:

```text
PASS: verification Compose config is valid
PASS: PostgreSQL test database is fresh and loopback-published
PASS: go-test-normal completed with zero skips (121 top-level tests; 17 packages)
PASS: go-test-race completed with zero skips (121 top-level tests; 17 packages)
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

## PASS — final post-fix authority and clean tree

Command:

```bash
set -o pipefail
PATH=/tmp/work-management-task2-bin:$PATH make verify-full 2>&1 |
  tee /tmp/work-management-api-task-4-fix1-evidence/21-final-make-verify-full.log
```

Stable final evidence:

```text
all modules verified
0 issues.
No high-confidence secret patterns found.
PASS: owned test child status 42 propagated unchanged and cleanup reaped the child
PASS: SIGTERM reached the owned test process group, reaped it, and began Compose cleanup promptly
PASS: SIGINT reached the owned test process group, reaped it, and began Compose cleanup promptly
PASS: go-test-normal completed with zero skips (121 top-level tests; 17 packages)
PASS: go-test-race completed with zero skips (121 top-level tests; 17 packages)
PASS: Mailpit accepted exactly one current SMTP transaction
PASS: durable SENT detail matches the current Mailpit Message-ID and attempt
PASS: authoritative Compose verification completed
PASS: Compose cleanup removed all project containers, networks, and volumes
git diff --check
git diff --exit-code -- .
git diff --cached --exit-code -- .
```

Result: exit 0. The final `clean-check` also required `git status --porcelain --untracked-files=all` to be empty; the subsequent explicit status check was empty.

## FAIL then PASS — final worker drain and SMTP cancellation regressions

Commands:

```bash
go test -count=1 ./internal/notifications \
  -run TestWorkerRunUntilStoppedDrainsActiveAttemptBeforeReturning
go test -count=1 ./cmd/worker -run 'TestServeWorker'
go test -count=1 ./internal/platform/email \
  -run TestSMTPClientCancellationInterruptsBlockedTransaction
go test -count=1 ./internal/notifications \
  -run TestWorkerRunUntilStoppedPreservesCompletionErrorAfterClaimsStop
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -count=1 ./internal/notifications \
  -run 'TestWorkerDrainPersistsActiveCompletionBeforeReturning|TestWorkerForcedDrainIsBoundedAndLeaseRecovers'
```

RED evidence:

```text
worker.RunUntilStopped undefined
undefined: serveWorker
context cancellation did not interrupt the SMTP transaction
TestWorkerRunUntilStoppedPreservesCompletionErrorAfterClaimsStop: error=<nil>
TestWorkerRunUntilStoppedPreservesCompletionErrorAfterClaimsStop/context_canceled: error=<nil>
already-stopped admin waited for shutdown timeout: context deadline exceeded
```

GREEN evidence:

```text
PASS: TestWorkerRunUntilStoppedDrainsActiveAttemptBeforeReturning
PASS: TestServeWorkerDrainsOnSignalAndReapsBothComponents
PASS: TestServeWorkerForcesCancellationAtGraceDeadlineAndReapsBothComponents
PASS: TestServeWorkerPreservesFirstComponentError
PASS: TestServeWorkerPreservesContextCanceledCompletionErrorDuringDrain
PASS: TestServeWorkerForceClosesAdminAfterPromptShutdownError
PASS: TestSMTPClientCancellationInterruptsBlockedTransaction
PASS: TestWorkerRunUntilStoppedPreservesCompletionErrorAfterClaimsStop
PASS: TestWorkerDrainPersistsActiveCompletionBeforeReturning
PASS: TestWorkerForcedDrainIsBoundedAndLeaseRecovers
```

The cancellation interrupt installs the ordinary SMTP deadline before the cancellation callback, so a concurrently canceled context cannot have its immediate deadline overwritten. Signal-driven clean drain retains an uncanceled attempt context through SMTP and fenced completion; only grace expiry cancels it. Genuine completion errors remain errors even when their value is `context.Canceled`, and an already-reaped admin listener is not passed to a potentially blocking `Shutdown` call.

## FAIL then PASS — final claim and notification-page plans

Command:

```bash
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -count=1 -v ./internal/notifications \
  -run 'TestClaimNextSelectsEarliestEligibilityAcrossPendingAndExpired|TestClaimCandidatePlansUseBoundedPartialIndexes|TestNotificationListPlansUseMatchingIndexesWithoutSorts|TestUnfilteredNotificationKeysetPagination|TestFilteredNotificationKeysetPaginationAcrossTiedTimestamps'
```

Initial JSON plans exposed a `Sort` over `notification_deliveries_status_page_idx` for the expired candidate and failed to require the filtered list indexes. After the production-query correction and set-based fixture calibration:

```text
PASS: TestClaimNextSelectsEarliestEligibilityAcrossPendingAndExpired
PASS: TestClaimCandidatePlansUseBoundedPartialIndexes
PASS: TestNotificationListPlansUseMatchingIndexesWithoutSorts
PASS: TestUnfilteredNotificationKeysetPagination
PASS: TestFilteredNotificationKeysetPaginationAcrossTiedTimestamps/status
PASS: TestFilteredNotificationKeysetPaginationAcrossTiedTimestamps/work_item
```

The two claim checks use the exact production candidate SQL against a 12,000-row mixed backlog. The three list checks use the production query builder against 12,000 tied timestamps. They run under default planner settings, reject `Seq Scan`, `Sort`, and `Incremental Sort`, and require the matching partial/page index.

## FAIL then PASS — final title and persisted worker-ID contracts

Commands:

```bash
go test -count=1 -v ./internal/workitems ./internal/httpapi ./internal/notifications \
  -run 'TestValidateCreateInputEnforcesSMTPCompatibleTitleBoundaries|TestCreateRejectsHeaderNewlineTitleWithoutPersistence|TestEveryAcceptedTitleRendersAHeaderValidSubject'
go test -count=1 -v ./internal/config ./internal/notifications \
  -run 'TestLoadWorkerValidatesPersistedWorkerIDContract|TestGeneratedWorkerID|TestValidateWorkerIDContract|TestNewWorkerRejectsInvalidWorkerIDBelowConfig|TestClaimNextRejectsInvalidWorkerIDBeforeSQLMutation'
go test -count=1 -v ./cmd/worker \
  -run TestRunRejectsInvalidWorkerIDBeforeOpeningDependencies
```

RED evidence included accepted LF/CR/CRLF titles, HTTP `201` for a newline title, a 1,500-octet MIME-Q Subject line, and missing shared worker-ID validation. Final evidence:

```text
PASS: title 200-rune boundary
PASS: title 201-rune boundary
PASS: title LF, leading LF, CR, and CRLF rejection
PASS: rejected HTTP create left no item, event, idempotency row, or notification
PASS: accepted short ASCII, 200 two-byte-rune, and 200 four-byte-rune titles rendered header-valid bounded subjects with the complete title in the body
PASS: configured/generated/NewWorker/ClaimNext worker-ID boundaries, including 255/256 runes, multibyte input, blank/control/NUL/invalid UTF-8, and long/invalid hostnames
PASS: invalid configured WORKER_ID stopped process startup before dependencies/listener readiness
```

## PASS — final reliability candidate Compose authority

Command:

```bash
PATH=/tmp/work-management-task2-bin:$PATH \
VERIFY_LOG_DIR=/tmp/work-management-api-final-fix-evidence/precommit-compose-final \
make compose-smoke
```

Evidence:

```text
PASS: owned test child status 42 propagated unchanged and cleanup reaped the child
PASS: SIGTERM reached the owned test process group, reaped it, and began Compose cleanup promptly
PASS: SIGINT reached the owned test process group, reaped it, and began Compose cleanup promptly
PASS: verification Compose config is valid
PASS: PostgreSQL test database is fresh and loopback-published
PASS: go-test-normal completed with zero skips (145 top-level tests; 18 packages)
PASS: go-test-race completed with zero skips (145 top-level tests; 18 packages)
PASS: migrate and seed completed successfully
PASS: all six published ports are ephemeral IPv4 loopback mappings
PASS: API and worker admin live, ready, and metrics endpoints respond
PASS: public API serves business routes and excludes operational routes
PASS: Mailpit accepted exactly one current SMTP transaction
PASS: durable SENT detail matches the current Mailpit Message-ID and attempt
PASS: authoritative Compose verification completed
PASS: Compose cleanup removed all project containers, networks, and volumes
```
