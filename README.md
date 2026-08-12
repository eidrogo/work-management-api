# Work Management API

A greenfield Go backend that acts as the source of truth for operational work: managers create and assign work, workers see and advance only their own queue, every successful mutation is traceable, and assignment email is delivered through a durable PostgreSQL outbox and a real SMTP connection.

This is an assessment implementation, not a claim of production readiness. It intentionally concentrates on correctness, explicit invariants, transaction boundaries, failure visibility, operability, and a credible evolution path.

## 1. Purpose

The service replaces ad hoc messages, spreadsheets, and email threads with a versioned backend API that records:

- who created a work item;
- who currently owns it;
- its current lifecycle state;
- every successful state/assignment mutation;
- the durable assignment-email intent;
- every notification delivery attempt and terminal failure.

The repository contains one Go module with independently runnable API and notification-worker processes.

## 2. Scope

### Implemented

- Manager-only creation of unassigned work
- Manager-wide list/read visibility
- Worker queue scoped from the authenticated principal
- Manager assignment and pre-start reassignment
- Worker-only `ASSIGNED → IN_PROGRESS → COMPLETED`
- Immutable completed work
- Optimistic work-item versions exposed as ETags
- Create idempotency and deterministic command replay
- Append-only work-item event history
- PostgreSQL transactional notification outbox
- Expiring worker leases and `FOR UPDATE SKIP LOCKED`
- Bounded retry cycles with equal jitter
- Append-only notification-attempt history
- Real SMTP transport: plain, STARTTLS, and implicit TLS
- Manager notification inspection and failed-delivery replay
- Keyset pagination
- Stable machine-readable API errors and request IDs
- Structured logs, health/readiness, and Prometheus-compatible metrics
- Migrations, deterministic seed, Docker Compose, demo script, tests, ADRs, diagrams, and CI

### Explicit non-goals

Frontend, signup, passwords, account management, production identity issuance, comments, attachments, subtasks, recurrence, scheduling, configurable workflows, cancellation, deletion, reopen, arbitrary backward transitions, Kafka, Redis, Kubernetes, cloud infrastructure, multi-region, multi-team tenancy, a full webhook platform, SMS, and push notifications.

## 3. Scenario interpretation

The smallest coherent system is a manager-defined, worker-executed forward workflow:

```text
UNASSIGNED -> ASSIGNED -> IN_PROGRESS -> COMPLETED
```

Managers define ownership and see the whole workload, but never advance worker progress. Workers read and mutate only work currently assigned to them. Work may begin without an assignee. Once completed, it is immutable in v1.

Reassignment is allowed only while `ASSIGNED`, keeps the item in `ASSIGNED`, requires a reason, appends a new event, and sends the new assignee a new email. Active work cannot be reassigned in this version because silently transferring in-flight responsibility has materially different business semantics.

The full ambiguity record is in [`docs/assessment/ambiguities.md`](docs/assessment/ambiguities.md).

## 4. Assumptions

1. Only managers create work items.
2. Users have exactly one v1 role: `MANAGER` or `WORKER`.
3. Managers cannot be assignees.
4. General title/description editing is outside the core assessment.
5. There is one team and one authorization domain.
6. Reopen, cancellation, deletion, unassignment, and backward transitions require future explicit commands.
7. SMTP `SENT` means the configured server accepted the message after `DATA`; it does not mean inbox placement, open, or even final delivery.
8. Assignment durability matters more than synchronous email latency: provider failure does not roll back a committed assignment.
9. Generic SMTP provides at-least-once, not exactly-once, delivery.
10. Future production identity will map an externally verified subject to a local user/principal.

## 5. Functional behavior

### Manager capabilities

- Create an unassigned work item.
- Get and list all work.
- Filter by status and assignee.
- Assign an unassigned item.
- Reassign an `ASSIGNED` item to a different worker with a reason.
- View item history.
- List and inspect notification delivery state and attempt history.
- Replay a terminally failed notification in a new retry cycle.

### Worker capabilities

- List their own queue.
- Filter their own queue by status.
- Read their own assigned item.
- View that item’s relevant history.
- Start an `ASSIGNED` item.
- Complete an `IN_PROGRESS` item.

### Deliberate rejections

- Worker creates or assigns work.
- Manager starts/completes worker work.
- Worker reads or mutates another worker’s work.
- Caller widens `/v1/me/work-items` with an assignee query parameter.
- Assignee is a manager or unknown user.
- Reassignment after work starts.
- Reassignment without a reason.
- Backward, skipped, post-completion, cancellation, deletion, or reopen transitions.
- Stale state-changing mutation without a matching version.

## 6. Authorization matrix

| Operation | Manager | Current assignee | Different worker |
|---|---:|---:|---:|
| Create work item | Allow | Deny | Deny |
| List all work | Allow | Deny | Deny |
| List own queue | Deny | Allow | Allow for their own queue |
| Read item | Allow | Allow | Hidden as 404 |
| Assign/reassign | Allow | Deny | Deny |
| Start | Deny | Allow from `ASSIGNED` | Hidden as 404 |
| Complete | Deny | Allow from `IN_PROGRESS` | Hidden as 404 |
| View item history | Allow | Allow | Hidden as 404 |
| Inspect/replay notification | Allow | Deny | Deny |

Authorization is enforced in `internal/workitems` and `internal/notifications`, below the HTTP transport.

## 7. Lifecycle and invariants

```mermaid
stateDiagram-v2
    [*] --> UNASSIGNED: manager creates
    UNASSIGNED --> ASSIGNED: manager assigns
    ASSIGNED --> ASSIGNED: manager reassigns + reason
    ASSIGNED --> IN_PROGRESS: current worker starts
    IN_PROGRESS --> COMPLETED: current worker completes
    COMPLETED --> [*]
```

Core invariants:

- The authenticated actor never comes from a request body.
- Creator is immutable.
- `UNASSIGNED` has no assignee.
- Every later state has an assignee.
- Assignees are existing workers.
- Every successful mutation appends a durable event.
- Assignment update, assignment event, and notification insert commit together.
- Same-assignee replay creates no event and no duplicate delivery.
- Same-target state replay is a deterministic no-op.
- A version-conditional update prevents silent concurrent overwrite.
- Completed work cannot be modified in the application or directly in the database.
- Timestamps use UTC.

## 8. Architecture overview

See [`docs/architecture.md`](docs/architecture.md) for component and assignment sequence diagrams.

The selected design is a modular monolith:

- `cmd/api`: REST API, authentication, health, metrics
- `cmd/worker`: outbox claim, SMTP delivery, retry, worker health/metrics
- PostgreSQL: current state, immutable history, idempotency, outbox, attempts
- Mailpit: deterministic local SMTP sink only
- one shared Go module and release artifact

Architecture alternatives are compared in [`docs/assessment/architecture-alternatives.md`](docs/assessment/architecture-alternatives.md).


### Dependency choices

The Go module deliberately has no third-party Go dependencies.

| Dependency | Problem solved | Why not only the Go standard library | Operational consequence | Replacement cost |
|---|---|---|---|---|
| PostgreSQL 16 | Transactions, constraints, concurrent row claims, keyset indexes, durable current/history/outbox state | The standard library has no database server | Shared availability/capacity boundary; migrations, backups, vacuum, and connection limits must be operated | High once data exists, but SQL remains explicit and portable within PostgreSQL |
| System `libpq` through a narrow cgo driver | PostgreSQL wire/client behavior in the module-proxy-restricted assessment environment | `database/sql` requires a driver | cgo toolchain and OS client-library patching; intentionally not production-ready | Low at application boundaries because services use `database/sql`; transaction/concurrency tests protect replacement |
| Mailpit in Compose | Deterministic local SMTP acceptance and message inspection | A custom fake would not prove runtime SMTP transport | Local-only container and UI; no external delivery proof | Trivial; real providers use the same SMTP adapter |
| Docker Compose | Repeatable database, SMTP sink, migrations, seed, API, and worker startup | Process orchestration is outside Go’s standard library | Requires Docker for the full local path | Low; direct Go commands are documented |
| Go standard library | HTTP, JSON, TLS, SMTP, logging, cryptography, concurrency, shutdown, and testing | It is sufficient for these boundaries | Small dependency and supply-chain surface, but more explicit code | Individual packages can be replaced behind existing boundaries if measured needs justify it |

The `libpq` choice is captured separately in [ADR 0005](docs/adr/0005-libpq-assessment-adapter.md) because it is an environmental accommodation, not a preferred production default.

## 9. Repository map

```text
api/openapi.json                    OpenAPI 3.1 contract
cmd/api                             Public API process
cmd/worker                          Notification worker process
cmd/migrate                         Versioned migration command
cmd/seed                            Deterministic seed command
cmd/replay-notification             Operational failed-delivery replay
internal/auth                       Principal roles and token resolver
internal/config                     Validated environment configuration
internal/httpapi                    Versioned transport and error contract
internal/metrics                    Prometheus text metrics
internal/migrations                 Checksum/advisory-lock migration runner
internal/notifications              Outbox store, attempts, worker, retry policy
internal/platform/database          Pool and DSN policy
internal/platform/email             Real SMTP adapter
internal/platform/postgres          Assessment-only database/sql libpq adapter
internal/workitems                  Domain policy and transactional application service
internal/testsupport/postgrestest   Isolated-schema PostgreSQL test harness
internal/workeradmin                Worker liveness/readiness/metrics
migrations                          Forward and local rollback SQL
docs/adr                            Architecture Decision Records
docs/assessment                     Discovery, ambiguity, alternatives, plan
docs/operations.md                  Failure modes, alerts, recovery, roadmap
scripts/demo.sh                     End-to-end reviewer flow
compose.yaml                        PostgreSQL + Mailpit + API + worker
```

## 10. Data model

### `users`

Seeded local representation of an external identity: UUID, display name, email, role, UTC creation time.

### `auth_tokens`

SHA-256 token hash, user reference, creation/optional expiry. Plain development tokens are documented but not stored in the database.

### `work_items`

Current title, description, lifecycle status, immutable creator, nullable assignee, monotonic version, UTC timestamps.

### `idempotency_keys`

Actor, operation, client key, request hash, resource ID, creation time. A key reused with a different payload fails deterministically.

### `work_item_events`

Append-only create, assignment, reassignment, start, and completion events with actor, previous/new status, previous/new assignee, reason, sequence, and UTC time.

### `notification_deliveries`

Current outbox/delivery state, assignment event, recipient, payload, attempt counters, retry cycle, next attempt, last error, stable message ID, provider ID if available, lease owner/expiry, sent time, and unique deduplication key.

### `notification_attempts`

Append-only per-attempt outcome (`SENT`, `RETRY_SCHEDULED`, `FAILED`, `ABANDONED`), worker ID, error, and timestamps.

Database checks reinforce state/assignee consistency, lease consistency, terminal scheduling, creator immutability, completed immutability, unique notification intent, and append-only histories.

## 11. Query and pagination behavior

Work-item pages are ordered by `(created_at DESC, id DESC)`. History is ordered by event sequence ascending. Notification pages use `(created_at DESC, id DESC)`. Cursors are opaque base64url-encoded boundaries.

Covered indexes let the database seek to a cursor in approximately `O(log N)` and read `O(page_size)` rows instead of scanning/skipping every prior row. The maximum page size is 100.

Cursors provide stable normal forward traversal; they do not promise a frozen snapshot across an arbitrarily long browsing session.

## 12. Assignment and notification flow

1. Authenticate the manager.
2. Load the item and candidate assignee inside a transaction.
3. Validate role, state, reason, and expected version.
4. Conditionally update the work item.
5. Append the assignment/reassignment event.
6. Insert a unique `PENDING` delivery with a stable message ID.
7. Commit.
8. Return the updated item and ETag.
9. A worker claims due rows with an expiring PostgreSQL lease.
10. The same real SMTP adapter connects to Mailpit locally or a configured provider.
11. On server acceptance, atomically mark `SENT` and append a `SENT` attempt.
12. On retryable failure, append `RETRY_SCHEDULED`, clear the lease, and set a bounded jittered retry time.
13. On permanent/exhausted failure, append `FAILED` and expose terminal state.
14. If a worker dies while leased, another worker records `ABANDONED` after lease expiry and reclaims safely.

## 13. Delivery guarantees and limitations

- **Database guarantee:** a successful assignment response cannot exist without its assignment event and durable notification row.
- **Processing guarantee:** pending rows survive API/worker restarts.
- **Worker concurrency:** multiple workers cannot claim the same row at the same time under normal PostgreSQL semantics.
- **Delivery semantics:** at least once.
- **`SENT`:** the SMTP server accepted the completed message transaction.
- **Not guaranteed:** inbox delivery, bounce-free delivery, opens, exactly once.

There is an unavoidable generic SMTP ambiguity window: the SMTP server can accept the message and the worker can crash before recording `SENT`. Lease recovery may resend the stable `Message-ID`. A provider API with an idempotency key plus delivery webhooks would reduce this risk.

Mailpit proves real SMTP transport behavior and local server acceptance. It does **not** prove external inbox delivery.

## 14. Prerequisites

### Docker workflow

- Docker Engine
- Docker Compose v2
- `curl`
- `jq`

### Direct Go workflow

- Go 1.23.2
- a C compiler
- `pkg-config`
- PostgreSQL `libpq` runtime and development headers
- PostgreSQL 16-compatible server
- an SMTP server or provider

On Debian/Ubuntu, the direct-build system packages are typically:

```bash
sudo apt-get install build-essential pkg-config libpq-dev
```

## 15. Fastest local run

```bash
cp .env.example .env
docker compose up --build -d
docker compose ps
./scripts/demo.sh
```

Default local endpoints:

- API: `http://localhost:8081`
- API metrics: `http://localhost:8081/metrics`
- Worker health/metrics: `http://localhost:9091`
- Mailpit UI: `http://localhost:8025`
- PostgreSQL: `localhost:5432`

Port 8081 is the default host API port because 8080 was occupied during initial discovery. Override with `API_PORT`.

Stop while preserving data:

```bash
docker compose down
```

Remove all local data and restart from a clean database:

```bash
make reset
docker compose up --build -d
```

Compose automatically runs the migration and seed one-shot services before starting API and worker.

## 16. Direct Go run

Start only dependencies:

```bash
docker compose up -d postgres mailpit
cp .env.example .env
set -a
source .env
set +a
```

Then, in order:

```bash
go run ./cmd/migrate up
go run ./cmd/seed
go run ./cmd/api
```

In another shell with the same environment:

```bash
go run ./cmd/worker
```

Migration rollback for local development:

```bash
go run ./cmd/migrate down 1
```

Do not use destructive down migrations as an automatic production rollback strategy.

## 17. Seed identities and authentication

Development-only bearer tokens:

| Role | User | ID | Token |
|---|---|---|---|
| Manager | Maya Manager | `11111111-1111-4111-8111-111111111111` | `dev-manager-token` |
| Worker | Alice Worker | `22222222-2222-4222-8222-222222222222` | `dev-worker-alice-token` |
| Worker | Bob Worker | `33333333-3333-4333-8333-333333333333` | `dev-worker-bob-token` |

Use:

```bash
-H 'Authorization: Bearer dev-manager-token'
```

The database stores only token hashes. The development tokens are deliberately non-secret sample data and must never be enabled in production.

## 18. Typical end-to-end walkthrough

Set helpers:

```bash
API=http://localhost:8081
MANAGER=dev-manager-token
ALICE=dev-worker-alice-token
BOB=dev-worker-bob-token
ALICE_ID=22222222-2222-4222-8222-222222222222
```

Create unassigned work:

```bash
created="$(curl --fail --silent \
  -X POST "$API/v1/work-items" \
  -H "Authorization: Bearer $MANAGER" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: walkthrough-001' \
  -d '{"title":"Inspect loading dock","description":"Document the exception."}')"

item_id="$(jq -r '.data.id' <<<"$created")"
version="$(jq -r '.data.version' <<<"$created")"
```

Assign Alice:

```bash
assigned="$(curl --fail --silent \
  -X PUT "$API/v1/work-items/$item_id/assignment" \
  -H "Authorization: Bearer $MANAGER" \
  -H 'Content-Type: application/json' \
  -H "If-Match: \"$version\"" \
  -d "{\"assignee_id\":\"$ALICE_ID\"}")"

version="$(jq -r '.data.version' <<<"$assigned")"
```

Open `http://localhost:8025` and confirm a message addressed to `alice.worker@example.test`.

Inspect durable notification state:

```bash
curl --fail --silent \
  "$API/v1/notifications?work_item_id=$item_id" \
  -H "Authorization: Bearer $MANAGER" | jq
```

List Alice’s queue:

```bash
curl --fail --silent \
  "$API/v1/me/work-items?status=ASSIGNED" \
  -H "Authorization: Bearer $ALICE" | jq
```

Start and complete:

```bash
started="$(curl --fail --silent \
  -X POST "$API/v1/work-items/$item_id/start" \
  -H "Authorization: Bearer $ALICE" \
  -H "If-Match: \"$version\"")"

version="$(jq -r '.data.version' <<<"$started")"

curl --fail --silent \
  -X POST "$API/v1/work-items/$item_id/complete" \
  -H "Authorization: Bearer $ALICE" \
  -H "If-Match: \"$version\"" | jq
```

Inspect history:

```bash
curl --fail --silent \
  "$API/v1/work-items/$item_id/history" \
  -H "Authorization: Bearer $ALICE" | jq
```

Demonstrate that Bob cannot enumerate Alice’s item:

```bash
curl --silent --include \
  "$API/v1/work-items/$item_id" \
  -H "Authorization: Bearer $BOB"
# HTTP/1.1 404
```

Demonstrate that a manager cannot work on Alice’s behalf:

```bash
curl --silent --include \
  -X POST "$API/v1/work-items/$item_id/start" \
  -H "Authorization: Bearer $MANAGER" \
  -H 'If-Match: "2"'
# HTTP/1.1 403
```

The executable version of this walkthrough is [`scripts/demo.sh`](scripts/demo.sh).

## 19. API contract and errors

OpenAPI 3.1: [`api/openapi.json`](api/openapi.json)

All application errors use:

```json
{
  "error": {
    "code": "WORK_ITEM_INVALID_TRANSITION",
    "message": "The work item cannot move from COMPLETED to IN_PROGRESS.",
    "details": {
      "current_status": "COMPLETED",
      "requested_status": "IN_PROGRESS"
    },
    "request_id": "..."
  }
}
```

Stable codes include authentication required/unavailable, permission denied, not found, invalid input, invalid transition, assignment conflict, concurrent modification, idempotency-key reuse, assignee/reassignment errors, and notification replay conflict.

SQL, SMTP credentials, provider responses containing sensitive details, and internal stack traces are not returned to clients.

## 20. Real SMTP provider configuration

The worker uses the same SMTP adapter for Mailpit and a real provider. Do not commit credentials.

Example production-style environment shape:

```bash
APP_ENV=production
SMTP_HOST=smtp.provider.example
SMTP_PORT=587
SMTP_USERNAME='secret-manager-injected-user'
SMTP_PASSWORD='secret-manager-injected-password'
SMTP_FROM=operations@example.com
SMTP_FROM_NAME='Operations'
SMTP_TLS_MODE=starttls
SMTP_TLS_SERVER_NAME=smtp.provider.example
SMTP_TLS_INSECURE_SKIP_VERIFY=false
SMTP_TIMEOUT=10s
```

Production configuration rejects plaintext SMTP and TLS verification bypass. It also requires PostgreSQL `sslmode=verify-full`. The API rejects `APP_ENV=production` while the development token authenticator is selected, so production cannot accidentally launch with seeded credentials.

## 21. Tests and verification

Run tests that do not require a local PostgreSQL server:

```bash
go test ./...
go test -race ./...
```

PostgreSQL tests use an isolated schema per test and skip only when `TEST_DATABASE_URL` is absent:

```bash
export TEST_DATABASE_URL='postgres://work:work@localhost:5432/work_management?sslmode=disable'
go test -count=1 ./...
```

Locally executable static/unit verification:

```bash
make verify
```

Full clean-state verification on a machine with Docker, PostgreSQL-capable networking, and `golangci-lint`:

```bash
make verify-full
```

`make verify-full` runs linting, builds the image, starts an isolated Compose project on alternate host ports, waits for API and worker readiness, runs the PostgreSQL integration suite, exercises the manager-to-worker flow, confirms authorization rejection, sends through Mailpit over real SMTP, and removes the verification volumes.

Individual checks remain available:

```bash
make lint
make docker-build
make compose-smoke
git diff --check
```

The suite protects:

- lifecycle and authorization policy;
- assignee role and creator/completed invariants;
- create idempotency;
- atomic assignment/history/outbox behavior;
- forced outbox-insert rollback;
- concurrent assignment and transition results;
- stable pagination boundaries;
- migration/constraint behavior;
- seed idempotency;
- concurrent notification claims and lease recovery;
- retry, terminal failure, replay, and attempt history;
- real TCP SMTP delivery and error classification;
- HTTP authentication, scoping, error shape, ETags, and full PostgreSQL-backed flow;
- OpenAPI route presence.

In the repository-construction environment, Docker, a PostgreSQL server, and `golangci-lint` were unavailable. Those checks are represented in Compose/CI but must be executed in a capable environment before treating the submission as fully verified.

## 22. Operability

- API liveness: `/health/live`
- API database readiness: `/health/ready`
- API metrics: `/metrics`
- Worker liveness/readiness/metrics: port 9091 equivalents
- JSON logs to stdout
- Request ID in API responses and error bodies
- Manager notification list/get includes attempts, last error, lease, schedule, and sent timestamps
- CLI replay: `go run ./cmd/replay-notification <notification-uuid>`

Detailed failure modes, alert candidates, SLO candidates, backup/restore, migration, rollback, and replay procedures are in [`docs/operations.md`](docs/operations.md).

## 23. Security considerations

Implemented:

- production API startup rejects the development-only authentication mode;
- every production command requires PostgreSQL `sslmode=verify-full`;
- no caller-controlled actor/role;
- hashed local tokens;
- parameterized SQL;
- strict JSON and unknown-field rejection;
- bounded request bodies and page sizes;
- HTTP and SMTP timeouts;
- minimum TLS 1.2 for SMTP TLS modes;
- secure production SMTP defaults;
- no committed real secret;
- safe client error messages;
- immutable creator/completed rows and append-only histories;
- pinned Go version and Docker image tags;
- secret-pattern check.

Before production:

- external OIDC/JWT verification;
- secret manager and rotation;
- TLS PostgreSQL;
- private metrics/health exposure;
- rate limiting and abuse controls;
- dependency/SBOM/vulnerability scanning;
- maintained PostgreSQL Go driver;
- threat model, security review, and penetration test;
- multi-team isolation if introduced.

## 24. Known limitations and accepted debt

- The API intentionally cannot start in `APP_ENV=production` until a production authenticator replaces the development token adapter.
- The `libpq` cgo driver is an assessment-environment accommodation and must be replaced with a maintained driver before production.
- SMTP exactly-once delivery is impossible with this generic interface.
- No bounce/delivery/open webhooks.
- Manual replay actor is in structured logs, not a dedicated immutable operator-audit table.
- No general edit or administrative correction workflow.
- Reassignment is rejected after work starts.
- No cancel, delete, unassign, reopen, or retention/archive process.
- No tenant/team dimension.
- The process metrics registry is in-memory; counters reset on process restart.
- Worker throughput is sequential per process; horizontal workers are supported.
- Operational endpoints rely on network isolation rather than application authentication.
- No load/soak test has established production capacity.
- Docker/Compose and PostgreSQL integration execution could not be completed in the original construction environment.

## 25. Architecture Decision Records

1. [ADR 0001: Modular monolith](docs/adr/0001-modular-monolith.md)
2. [ADR 0002: Transactional outbox](docs/adr/0002-transactional-outbox.md)
3. [ADR 0003: Identity and authorization](docs/adr/0003-identity-and-authorization.md)
4. [ADR 0004: Lifecycle, concurrency, pagination](docs/adr/0004-lifecycle-concurrency-pagination.md)
5. [ADR 0005: Assessment `libpq` adapter](docs/adr/0005-libpq-assessment-adapter.md)

## 26. Required reflection answers

### 1. How did you interpret the scenario, and what assumptions did you make?

I treated the system as an operational ownership ledger rather than a generic task product. That made assignment, current responsibility, forward progress, and durable trace the core. Managers define and route work; workers execute only their assigned work. I selected a strict four-state lifecycle, manager-only creation, pre-start reassignment with a reason, immutable completion, single-team scope, and external production identity. Ambiguities and validation questions are explicit rather than hidden in code.

### 2. What were the most important architectural decisions, and what alternatives did you reject?

The highest-leverage choices were a modular monolith, PostgreSQL, a transactional outbox, application-layer authorization, append-only history beside current state, optimistic versions, leased `SKIP LOCKED` claims, and keyset pagination. I rejected microservices, synchronous SMTP, memory queues, caller-controlled identity, event sourcing, last-write-wins, and offset pagination because they either weaken correctness or add disproportionate operational cost.

### 3. How did you approach assignment, notification delivery, and traceability?

Assignment is one explicit transaction containing the conditional item update, assignment event, and unique outbox row. The worker handles only committed rows. Every claim outcome is immutable and failed work remains queryable. The API exposes current delivery state and attempt history. This guarantees durable intent and honest at-least-once processing without claiming inbox delivery or exactly once.

### 4. Where will this design fail first under growth, load, or team scale, and how would you address it?

PostgreSQL will become the first shared pressure point: connection limits, outbox polling, event/attempt growth, autovacuum, and hot-row conflicts. I would establish query/queue metrics and load tests first, then tune indexes, pooling, batch size, retention, and worker count. PgBouncer, partition/archive policy, read replicas for reporting, or a transactional outbox relay to a broker follow measured pressure. Team growth is addressed through ADRs, package ownership, OpenAPI, runbooks, and explicit invariants; a service split follows ownership evidence, not speculation.

### 5. What did you intentionally simplify or defer, and what debt did you accept?

I deferred production identity, tenancy, cancellation/reopen, mid-progress reassignment, webhook delivery, provider bounce reconciliation, cloud deployment, full operator audit, and compliance retention. I accepted a narrow cgo `libpq` adapter because the execution environment could not fetch modules; replacing it is a pre-production requirement. I also accepted process-local counters and sequential delivery inside each worker process.

### 6. What would you put in place before production?

Externally verified OIDC/JWT, secret management, TLS PostgreSQL/SMTP, a maintained DB driver, HA database and tested restores, backward-compatible migration policy, RED and queue dashboards, agreed SLOs/alerts, distributed tracing, rate limits, threat modeling, dependency/SBOM scanning, signed builds, load/failure testing, runbooks, on-call ownership, incident exercises, provider idempotency/webhooks, and privacy/retention policy.

### 7. How would you evolve for audit history, webhooks, or multi-team usage?

For audit, add immutable operator/configuration events with actor, reason, source, correlation ID, retention, and export policy. For webhooks, add subscriptions, signing secrets, a separate transactional webhook outbox, delivery attempts, replay, endpoint disablement, and ordering/version semantics. For multi-team, add `team_id` to every ownership and idempotency boundary, backfill safely, scope every index/query, derive team from trusted identity, consider RLS as defense in depth, and test cross-tenant non-disclosure.

### 8. How would you onboard another engineer?

Start with the README walkthrough, lifecycle and authorization matrix, architecture diagrams, and ADRs. Run the domain tests, then the PostgreSQL integration flow, inspect one assignment transaction and one notification claim, execute the demo, and practice a failed-notification replay. The new owner should then make a small behavior change with an ADR/test update and walk through the failure-mode/runbook implications in review.

## 27. AI usage disclosure

OpenAI ChatGPT (GPT-5.6 Pro) was used to help:

- structure the ambiguity analysis and implementation plan;
- generate and revise Go code, SQL, tests, Compose/CI configuration, OpenAPI, diagrams, and documentation;
- enumerate failure modes, security concerns, and verification checks;
- review implementation consistency and surface defects during iterative test runs.

The architecture, invariants, scope, trade-offs, code, SQL, tests, error contract, and documentation were manually reviewed and repeatedly corrected. Generated content was rewritten where tests or design review exposed incorrect assumptions, including PostgreSQL timestamp formats, DSN runtime options, SMTP address formatting, metrics dependency wiring, and retry semantics.

Validation includes unit tests, race-capable tests, real TCP SMTP tests, PostgreSQL integration suites, an HTTP/PostgreSQL end-to-end test, OpenAPI checks, secret-pattern checks, and Compose/demo workflows. Environment-dependent checks that could not run are disclosed rather than represented as successful.

The submitter is expected to be able to explain and defend every included decision and implementation detail during the technical debrief.
