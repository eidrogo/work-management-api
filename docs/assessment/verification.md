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

The cancellation interrupt installs the ordinary SMTP deadline before the cancellation callback, so a concurrently canceled context cannot have its immediate deadline overwritten. Signal-driven clean drain retains an uncanceled attempt context through SMTP and fenced completion; only grace expiry cancels it. An already-reaped admin listener is not passed to a potentially blocking `Shutdown` call. Independent review later found that the initial implementation still replaced a completion error when grace expiry and completion failure occurred together; the focused follow-up below closes that boundary.

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

## FAIL then PASS — forced-grace error provenance follow-up

Commands:

```bash
go test ./internal/notifications ./cmd/worker \
  -run '^(TestWorkerRunUntilStoppedPreservesCompletionErrorAtForcedCancellation|TestWorkerRunUntilStoppedPreservesSendErrorAtForcedCancellation|TestServeWorkerJoinsGraceDeadlineWithCompletionError)$' \
  -count=1 -v
go test ./internal/notifications \
  -run '^TestWorkerRunUntilStoppedPreservesSendAndCompletionErrorsWhenCompletionObservesCancellation$' \
  -count=1 -v
go test -race ./internal/notifications ./cmd/worker \
  -run '^(TestWorkerRunUntilStopped|TestServeWorker)' -count=20
```

RED evidence:

```text
completion error was masked: got context canceled want database completion failed
completion error was masked: got context canceled want database completion failed: context canceled
send error was masked: got context canceled want SMTP send failed during cancellation
error=worker shutdown grace period expired: context deadline exceeded
    notification worker: context canceled want deadline and database completion failed
error=context canceled want send=SMTP send failed completion=context canceled
```

GREEN evidence:

```text
PASS: TestWorkerRunUntilStoppedPreservesCompletionErrorAtForcedCancellation/sentinel
PASS: TestWorkerRunUntilStoppedPreservesCompletionErrorAtForcedCancellation/wrapped_context_canceled
PASS: TestWorkerRunUntilStoppedPreservesCompletionErrorAtForcedCancellation/context_canceled
PASS: TestWorkerRunUntilStoppedPreservesSendErrorAtForcedCancellation
PASS: TestWorkerRunUntilStoppedPreservesSendAndCompletionErrorsWhenCompletionObservesCancellation
PASS: TestServeWorkerJoinsGraceDeadlineWithCompletionError/sentinel
PASS: TestServeWorkerJoinsGraceDeadlineWithCompletionError/wrapped_context_canceled
PASS: TestServeWorkerJoinsGraceDeadlineWithCompletionError/context_canceled
PASS: lifecycle/coordinator focused suite under race detector, 20 repetitions
```

`RunUntilStopped` now gives a non-nil operation result precedence over context state sampled after the operation. `processBatch` likewise preserves a sender error observed with cancellation and joins sender plus completion errors when completion observes cancellation. At the coordinator boundary, forced shutdown therefore retains both `context.DeadlineExceeded` and the exact completion sentinel through `errors.Is`.

## FAIL then PASS — SMTP physical lines and coherent notification detail

Focused commands:

```bash
go test -count=1 ./internal/platform/email ./internal/config
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -count=1 -run '^TestGetUsesOneSnapshot' -v ./internal/notifications
go test -race -count=1 ./internal/platform/email ./internal/config
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -race -count=1 -run '^TestGetUsesOneSnapshot' ./internal/notifications
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -count=20 -run '^TestGetUsesOneSnapshot' ./internal/notifications
go test -count=20 -run '^(TestSMTPClientLineEnforcingServerAccepts|TestBuildMessagePreservesAcceptedBoundaryMIMEValues)' ./internal/platform/email
```

RED evidence reproduced both validated blockers:

```text
maximum serialized accepted reason line=1019 octets
maximum serialized 200-emoji recipient header=2944 octets
real line-enforcing SMTP response=552 / SMTP_MESSAGE_REJECTED
Get result=old PROCESSING delivery plus newly committed SENT attempt
Get result=old FAILED cycle plus newly committed replay event
```

GREEN evidence:

```text
PASS: decoded full 1,000-rune ASCII and four-byte Unicode reasons
PASS: 200-rune ASCII, two-byte, and four-byte sender/recipient display names
PASS: long and quoted-local-part addresses plus bounded Unicode Subject
PASS: every serialized physical line <=998 octets and quoted-printable body lines remain bounded
PASS: real line-enforcing SMTP accepts both ASCII and worst-case Unicode boundary messages
PASS: config and adapter reject invalid UTF-8, controls, and 201-rune effective names before dialing
PASS: concurrent Complete interleaving returns wholly old PROCESSING snapshot; a fresh read is wholly SENT
PASS: concurrent Replay interleaving returns wholly old FAILED snapshot; a fresh read is wholly PENDING cycle 2
PASS: traced Get issues read-only REPEATABLE READ begin, all three reads, and commit
PASS: focused SMTP/config and notification suites under race; both concurrency tests and SMTP boundary matrix pass 20 repetitions
```

The SMTP server in these tests is local and proves protocol acceptance only, not external inbox delivery. The reader gate is a test-only pgx query tracer on a dedicated database pool; production `Store.Get`, `Complete`, and `Replay` execute unchanged SQL paths without a production test hook or timing sleep.

## FAIL then PASS — PostgreSQL-incompatible U+0000 input

Focused commands:

```bash
go test ./internal/workitems ./internal/notifications ./internal/contract \
  -run 'Test(PersistedText|ReplayRejectsNUL|ReplayAcceptsMaximum)' -count=1 -v
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/httpapi ./internal/workitems ./internal/notifications \
  -run 'Test(HTTPRejectsNULPersistedTextWithoutSideEffects|NULValidationPreservesAuthorizationPrecedence|ServiceRejectsNULPersistedTextBeforeMutation|ServiceAuthorizationPrecedesNULValidation|StoreReplayRejectsNULBeforeMutation)$' \
  -count=1 -v
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./... -count=1
TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -race ./... -count=1
```

RED evidence:

```text
title, description, and assignment-reason domain validation returned nil
notification replay validation called the repository with a U+0000 reason
OpenAPI retained the prior newline-only title pattern and no U+0000 constraints on the other three fields
all four live HTTP mutations returned 500 INTERNAL_ERROR after SQLSTATE 22021
direct work-item service and notification-store calls reached the same PostgreSQL error
```

GREEN evidence:

```text
PASS: title, description, assignment reason, and replay reason reject U+0000 after existing trimming
PASS: maximum 200/5,000/1,000-rune boundaries and previously accepted non-NUL controls remain accepted
PASS: all four OpenAPI properties carry their exact U+0000-excluding patterns
PASS: eight removal/weakening mutations fail the contract gate
PASS: each live HTTP response is 400 INVALID_INPUT with the exact field, request ID, and safe headers
PASS: rejected creates leave no item/event/idempotency/outbox state; valid same-key creates then succeed
PASS: rejected assignment leaves version/status/assignee/event/outbox unchanged; the original-version valid assignment succeeds exactly once
PASS: rejected replay remains FAILED in the same cycle with no replay event; the valid replay succeeds exactly once
PASS: authorization precedes field validation, and direct service/store boundaries reject before persistence
PASS: complete normal and race suites against live PostgreSQL
```

## FAIL then PASS — invalid UTF-8 idempotency keys

Raw HTTP/1.1 permits `obs-text` bytes in header values. A manager request with byte `0xff` in `Idempotency-Key` originally reached PostgreSQL and failed with SQLSTATE 22021 after transactional work. Regression-first domain, live-service, and raw-wire tests now require a stable field-specific `400 INVALID_INPUT` before ID generation or transaction work, zero database side effects, no internal-error log or 500 metric, and preserved authentication/authorization precedence. A valid non-ASCII key such as `válido-界` remains accepted.

Persisted JSON text validation excludes U+0000 only. It deliberately preserves each field's prior whitespace, length, newline, and other control-character behavior rather than claiming general Unicode sanitization. The separate idempotency-key rule rejects malformed byte sequences but keeps valid Unicode.

## FAIL then PASS — package-manager-free scratch runtime

The final scan found that the digest-pinned Debian runtime still resolved unversioned `ca-certificates` and `curl` packages during every build. The replacement uses an exact three-stage graph: a tag-and-digest-pinned Go toolchain, an internal build stage, and a final `scratch` stage. It copies only the toolchain CA bundle, six `CGO_ENABLED=0` repository commands, and tracked migrations; it runs as numeric `65532:65532`. A bounded static probe accepts only numeric loopback endpoints, issues one exact `GET /health/ready` without proxies or redirects, caps headers/body/time, and requires HTTP 200.

Except for the `go.sum`-locked module graph, regression gates reject package managers, unlocked downloaders, VCS fetches, extra/external stages, remote ADD/external COPY, unexpected runtime instructions, incomplete command sets, named/root users, and unpinned toolchains. The actual-image inspection reads image layers rather than Docker-injected container mount paths, exact-compares the runtime filesystem, and compares every migration byte-for-byte with the tracked source. Direct `docker-build`, CI, `make up`, and Compose smoke enforce the source gate and actual-image inspection. The API image is explicitly named with `pull_policy: build` by the rendered Compose model rather than discovered through optional builder-specific image labels; the failure-injection regression suppresses the old label lookup and proves local `make up` still reaches inspection without creating or replacing Compose resources. The rendered-config gate verifies the local-build pull policy, and clean Compose startup proves migrate, seed, API, worker, and both readiness probes.

## FAIL then PASS — bounded SMTP peer input and immutable Docker frontend

Provenance: sealed Standard security scan `e85bce0a-eb40-4d20-bb52-762979bc126d` reported both findings against clean `7b7b53e09d444ce27ad8e69cbbf7326f175579b2`. The remediation logs are under `/tmp/work-management-api-security-fix-evidence/`: `smtp-red.txt`, `smtp-quit-red.txt`, `smtp-final-audit-red.txt`, `smtp-final-audit-green.txt`, `smtp-final-audit-race-green.txt`, `dockerfile-syntax-red.txt`, and `dockerfile-syntax-mutations-green.txt`.

Focused commands:

```bash
go test ./internal/platform/email \
  -run 'TestSMTPClientRejectsOversizedServerResponsesWithinReadBudget|TestSMTPResponseReadBudgetAcceptsExactBoundaryAndRejectsNextByte|TestSMTPClientDoesNotReadAfterAcceptedDATA|TestWriteSMTPPayloadDoesNotCloseAfterWriteFailure|TestSMTPClientNegotiatesImplicitTLS' \
  -count=10
go test -race ./internal/platform/email \
  -run 'TestSMTPClientRejectsOversizedServerResponsesWithinReadBudget|TestSMTPResponseReadBudgetAcceptsExactBoundaryAndRejectsNextByte|TestSMTPClientDoesNotReadAfterAcceptedDATA|TestWriteSMTPPayloadDoesNotCloseAfterWriteFailure|TestSMTPClientNegotiatesImplicitTLS' \
  -count=10
make dockerfile-syntax-test
```

RED evidence reproduced both sealed findings and the acceptance-boundary edge:

```text
oversized greeting/EHLO/AUTH/envelope/DATA replies were retryable or reached the wrong failure class
an oversized post-DATA response returned success
the budget+1 byte read succeeded
QUIT consumed a 1,048,651-byte response after an already accepted DATA transaction
the mutable # syntax=docker/dockerfile:1.7 directive failed the new repository gate
```

GREEN evidence:

```text
PASS: one sticky 1 MiB raw-transport quota spans plain SMTP, implicit TLS, and both sides of STARTTLS
PASS: malicious single-line and multiline replies at every important SMTP stage fail permanently as SMTP_PROTOCOL_ERROR
PASS: exactly 1 MiB is readable and byte 1,048,577 returns a sticky sentinel
PASS: implicit TLS retains concrete TLS state for authenticated SMTP
PASS: a DATA payload write failure cannot trigger the net/smtp closer's additional peer-controlled read
PASS: post-DATA 250 acceptance returns success and closes without issuing QUIT
PASS: Dockerfile syntax uses docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
PASS: the NUL-safe offline gate case-insensitively covers Dockerfile, Dockerfile.*, and *.Dockerfile and rejects mutable, misplaced, or duplicate mixed-case directives
PASS: focused SMTP matrix passes ten normal and ten race repetitions
```

The SMTP cap is cumulative raw transport input, so TLS handshake/record bytes consume the same fixed quota and decrypted text cannot expand without bounded ciphertext. The post-DATA `250` is the durable server-acceptance boundary: a later QUIT failure must not create a duplicate-producing retry. The Docker frontend remains a tag-plus-index-digest pin; monthly Dependabot candidates require manual digest verification and the offline mutation gate before acceptance.

## FAIL then PASS — implicit-TLS AUTH state mutation gate

Provenance: independent review `/tmp/work-management-api-final-security-fix-review.md` found that the production code at `40cbd2ffd21b68f9948832c3089de148d7259732` correctly passed a concrete `*tls.Conn` to `smtp.NewClient`, but the regression used logical host `127.0.0.1`. `net/smtp.PlainAuth` permits credentials without detected TLS for that exact local address, so the documented test claim was not yet proved. Follow-up evidence is under `/tmp/work-management-api-security-fix2-evidence/`.

Mutation and focused commands:

```bash
# Exact forbidden mutation:
# connection = tlsConnection
# becomes:
# connection = newResponseLimitedConn(tlsConnection)
go test ./internal/platform/email -run '^TestSMTPClientNegotiatesImplicitTLS$' -count=10

# After changing only the test's logical Host to localhost. while retaining
# the real listener port and TLSServerName localhost:
go test ./internal/platform/email -run '^TestSMTPClientNegotiatesImplicitTLS$' -count=20
go test -race ./internal/platform/email -run '^TestSMTPClientNegotiatesImplicitTLS$' -count=20
```

RED evidence on the original test:

```text
exact outer-wrapper mutation applied
ok  github.com/eidrogo/work-management-api/internal/platform/email
```

The mutation survived because the test's logical host was `127.0.0.1`, not because production had lost TLS state.

GREEN evidence after the test correction:

```text
localhost. resolves to ::1 and 127.0.0.1 on the supported macOS environment
PASS: corrected current code, 20 normal and 20 race repetitions
--- FAIL: TestSMTPClientNegotiatesImplicitTLS
send error=The email service returned an unexpected response. credentials observed=false
PASS: corrected test killed the exact outer-wrapper mutation before AUTH credentials were observed
```

The absolute `localhost.` logical name killed the mutation on the networked macOS/Linux environments first tested, but later offline Linux verification found that it still depended on DNS when `/etc/hosts` contained only `localhost`. The DNS-free portability correction below supersedes that intermediate test input. `TLSServerName` remains `localhost` for certificate identity, and no production-only test seam or global dial hook was added.

## FAIL then PASS — DNS-free implicit-TLS mutation-gate portability

Provenance: a second independent review of clean `9d36c4ae63373e14a9f1a4d2da35f0168fe0d061` ran the exact Linux/arm64 test binary in Ubuntu/Debian with Docker `--network none`. The `localhost.` test failed before TLS because the container hosts file had `localhost` without the trailing dot and no DNS was reachable. Follow-up evidence is under `/tmp/work-management-api-security-fix3-evidence/`.

Focused commands:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go test -c -o /tmp/wma-email-test-linux-arm64 ./internal/platform/email
docker run --rm --network none --platform linux/arm64 \
  -v /tmp/wma-email-test-linux-arm64:/wma-email-test:ro \
  ubuntu:24.04 /wma-email-test \
  -test.v -test.run '^TestSMTPClientNegotiatesImplicitTLS$' -test.count=20

go test ./internal/platform/email \
  -run '^TestSMTPClientNegotiatesImplicitTLS$' -count=20
go test -race ./internal/platform/email \
  -run '^TestSMTPClientNegotiatesImplicitTLS$' -count=20
```

RED evidence on `9d36c4a`:

```text
/etc/hosts: 127.0.0.1 localhost; ::1 localhost ip6-localhost ip6-loopback
Host localhost. with --network none:
send error=The email service could not be reached. credentials observed=false
```

GREEN evidence after replacing only the test logical Host with the numeric IPv4-mapped IPv6 literal `::ffff:127.0.0.1`:

```text
PASS: current code, 20 local normal and 20 local race repetitions
PASS: current Linux/arm64 binary, 20 repetitions each in Ubuntu 24.04 and Debian bookworm-slim with Docker --network none
PASS: exact outer-wrapper mutation fails locally before AUTH, credentials observed=false
PASS: exact outer-wrapper mutation fails in Ubuntu and Debian Linux/arm64 --network none before AUTH, credentials observed=false
```

`::ffff:127.0.0.1` requires no resolver, reaches the real IPv4 loopback listener on the supported macOS and Linux environments, and is not one of `net/smtp.PlainAuth`'s exact plaintext exemptions (`localhost`, `127.0.0.1`, `::1`). The test therefore remains a real production-path oracle for the concrete implicit `*tls.Conn` invariant without external DNS, `/etc/hosts` changes, a production dial seam, or a global test hook.
