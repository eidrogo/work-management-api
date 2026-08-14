# Work Management API

I built this greenfield Go backend as the source of truth for operational work: managers create and assign work, workers see and advance only their own queue, every successful mutation is traceable, and assignment email intent is durable before a worker submits the message to the configured SMTP server. SMTP acceptance is not proof of external inbox delivery.

I built this as an assessment implementation, not a claim of production readiness. I intentionally concentrated on correctness, explicit invariants, transaction boundaries, failure visibility, operability, and a credible evolution path.

## 1. Purpose

I built the service to replace ad hoc messages, spreadsheets, and email threads with a versioned backend API that records:

- who created a work item;
- who currently owns it;
- its current lifecycle state;
- every successful state/assignment mutation;
- the durable assignment-email intent;
- every notification delivery attempt and terminal failure.

I organized the repository as one Go module with independently runnable API and notification-worker processes.

## 2. Scope

### Implemented

- Manager-only creation of unassigned work
- Manager-wide list/read visibility
- Worker queue scoped from the authenticated principal
- Manager assignment and pre-start reassignment
- Worker-only `ASSIGNED → IN_PROGRESS → COMPLETED`
- Immutable completed work
- Optimistic work-item versions exposed as ETags
- Immutable create-response idempotency and deterministic command replay
- Append-only work-item event history
- PostgreSQL transactional notification outbox
- Expiring worker leases and `FOR UPDATE SKIP LOCKED`
- Bounded retry cycles with equal jitter
- Append-only notification-attempt and attributed replay history
- Real SMTP transport: plain, STARTTLS, and implicit TLS
- Manager notification inspection and failed-delivery replay
- Keyset pagination
- Stable machine-readable API errors and request IDs
- Structured logs, separate API/worker administrative health/readiness listeners, and bounded Prometheus-compatible metrics
- Migrations, deterministic seed, Docker Compose, demo script, tests, ADRs, diagrams, and CI

### Explicit non-goals

I intentionally excluded frontend, signup, passwords, account management, production identity issuance, comments, attachments, subtasks, recurrence, scheduling, configurable workflows, cancellation, deletion, reopen, arbitrary backward transitions, Kafka, Redis, Kubernetes, cloud infrastructure, multi-region, multi-team tenancy, a full webhook platform, SMS, and push notifications.

## 3. Scenario interpretation

I interpreted the smallest coherent system as a manager-defined, worker-executed forward workflow:

```text
UNASSIGNED -> ASSIGNED -> IN_PROGRESS -> COMPLETED
```

Managers define ownership and see the whole workload, but never advance worker progress. Workers read and mutate only work currently assigned to them. Work may begin without an assignee. Once completed, it is immutable in v1.

I allow reassignment only while `ASSIGNED`: it keeps the item in `ASSIGNED`, requires a reason, appends a new event, and sends the new assignee a new email. I do not allow active-work reassignment in this version because silently transferring in-flight responsibility has materially different business semantics.

I recorded the full ambiguity analysis in [`docs/assessment/ambiguities.md`](docs/assessment/ambiguities.md).

## 4. Assumptions

I made these assumptions:

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
- Replay a terminally failed notification in a reasoned, attributed new retry cycle (up to cycle 10).

### Worker capabilities

- List their own queue.
- Filter their own queue by status.
- Read their own assigned item.
- View that item’s relevant history.
- Start an `ASSIGNED` item.
- Complete an `IN_PROGRESS` item.

### Deliberate rejections

I deliberately rejected:

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

I enforce authorization in `internal/workitems` and `internal/notifications`, below the HTTP transport.

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

I enforce these core invariants:

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

I selected a modular-monolith design:

- `cmd/api`: public REST API plus a separately bound administrative listener
- `cmd/worker`: outbox claim, SMTP delivery, retry, and a separate administrative listener
- PostgreSQL: current state, immutable history, idempotency, outbox, attempts
- Mailpit: deterministic local SMTP sink only
- one shared Go module and release artifact

I compare the architecture alternatives in [`docs/assessment/architecture-alternatives.md`](docs/assessment/architecture-alternatives.md).


### Dependency choices

I kept the Go module's third-party surface narrow: pgx is the single direct Go dependency.

| Dependency | Problem solved | Why not only the Go standard library | Operational consequence | Replacement cost |
|---|---|---|---|---|
| PostgreSQL 16 | Transactions, constraints, concurrent row claims, keyset indexes, durable current/history/outbox state | The standard library has no database server | Shared availability/capacity boundary; migrations, backups, vacuum, and connection limits must be operated | High once data exists, but SQL remains explicit and portable within PostgreSQL |
| `github.com/jackc/pgx/v5/stdlib` v5.10.0 | Maintained PostgreSQL wire/client behavior behind `database/sql` | `database/sql` requires a driver | Pure-Go builds; pinned module updates and PostgreSQL compatibility must be maintained | Low at application boundaries because services and stores remain on `database/sql` |
| Mailpit in Compose | Deterministic local SMTP acceptance and message inspection | A custom fake would not prove runtime SMTP transport | Local-only container and UI; no external delivery proof | Trivial; real providers use the same SMTP adapter |
| Docker Compose | Repeatable database, SMTP sink, migrations, seed, API, and worker startup | Process orchestration is outside Go’s standard library | Requires Docker for the full local path | Low; direct Go commands are documented |
| Go standard library | HTTP, JSON, TLS, SMTP, logging, cryptography, concurrency, shutdown, and testing | It is sufficient for these boundaries | Small dependency and supply-chain surface, but more explicit code | Individual packages can be replaced behind existing boundaries if measured needs justify it |

I retained the original assessment-only `libpq` choice as history in [ADR 0005](docs/adr/0005-libpq-assessment-adapter.md) and superseded it with the maintained pgx decision in [ADR 0006](docs/adr/0006-pgx-stdlib-driver.md).

## 9. Repository map

```text
api/openapi.json                    OpenAPI 3.1 contract
cmd/api                             Public API process
cmd/worker                          Notification worker process
cmd/migrate                         Versioned migration command
cmd/seed                            Deterministic seed command
cmd/replay-notification             Operational failed-delivery replay
cmd/healthcheck                     Container-local bounded readiness probe
internal/auth                       Principal roles and token resolver
internal/apiadmin                   API liveness/readiness/metrics listener
internal/config                     Validated environment configuration
internal/httpapi                    Versioned transport and error contract
internal/metrics                    Prometheus text metrics
internal/migrations                 Checksum/advisory-lock migration runner
internal/notifications              Outbox store, attempts, worker, retry policy
internal/platform/database          pgx-backed database/sql pool and connection policy
internal/platform/email             Real SMTP adapter
internal/workitems                  Domain policy and transactional application service
internal/testsupport/postgrestest   Isolated-schema PostgreSQL test harness
internal/workeradmin                Worker liveness/readiness/metrics
migrations                          Forward and local rollback SQL
docs/adr                            Architecture Decision Records
docs/assessment                     Discovery, alternatives, self-review, evidence
docs/debrief.md                     Timed technical walkthrough and questions
docs/operations.md                  Failure modes, runbooks, recovery, roadmap
scripts/demo.sh                     Exact live lifecycle/SMTP/durable-state proof
scripts/verify-compose.sh           Authoritative fresh-stack verifier
compose.yaml                        PostgreSQL + Mailpit + API + worker
compose.verify.yaml                 Ephemeral loopback verification ports
```

## 10. Data model

### `users`

Seeded local representation of an external identity: UUID, display name, email, role, UTC creation time.

### `auth_tokens`

SHA-256 token hash, user reference, creation/optional expiry. Plain development tokens are documented but not stored in the database.

### `work_items`

Current title, description, lifecycle status, immutable creator, nullable assignee, monotonic version, UTC timestamps.

### `idempotency_keys`

Actor, operation, client key, request hash, resource ID, immutable first-response JSON snapshot, and creation time. A matching replay returns that originally committed representation even after later item or user mutation; a key reused with a different payload fails deterministically.

The required `Idempotency-Key` is an opaque valid UTF-8 value containing 1–200 Unicode characters after trimming. Invalid UTF-8 is rejected before ID generation or a database transaction with `400 INVALID_INPUT` and field `Idempotency-Key`.

### `work_item_events`

Append-only create, assignment, reassignment, start, and completion events with actor, previous/new status, previous/new assignee, reason, sequence, and UTC time.

### `notification_deliveries`

Current outbox/delivery state, assignment event, recipient, payload, attempt counters, delivery cycle, next attempt, allowlisted last-error code and fixed generic message, stable message ID, provider ID if available, lease owner/internal UUID fence/expiry, sent time, and unique deduplication key. The fence is worker-internal and is never serialized by the API.

### `notification_attempts`

Append-only per-attempt outcome (`SENT`, `RETRY_SCHEDULED`, `FAILED`, `ABANDONED`), worker ID, allowlisted failure code and generic message, and database timestamps.

### `notification_replay_events`

Append-only replay sequence, new delivery cycle, actor ID, request ID, channel (`HTTP` or `CLI`), required reason, and database timestamp. HTTP actors are authenticated managers; CLI operator identities are self-asserted by users who already have database/config access.

I use database checks to reinforce state/assignee consistency, lease consistency, terminal scheduling, creator immutability, completed immutability, unique notification intent, and append-only histories.

## 11. Query and pagination behavior

Work-item pages are ordered by `(created_at DESC, id DESC)`. History is ordered by event sequence ascending. Notification pages use `(created_at DESC, id DESC)`. Cursors are opaque base64url-encoded boundaries.

I added default-planner regression tests that populate large tied-timestamp sets and require the matching unfiltered, status-filtered, and work-item-filtered indexes without sequential scans or sort nodes. I set the maximum page size to 100.

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
9. A worker fetches PostgreSQL time once, locks at most the first eligible `PENDING` and expired `PROCESSING` row through their ordered partial indexes, chooses the earlier eligibility boundary, and claims exactly that row before sending. A fresh database UUID fences the expiring lease.
10. The same real SMTP adapter connects to Mailpit locally or a configured provider.
11. On server acceptance, atomically and conditionally mark `SENT` and append a `SENT` attempt only while the owner/token/attempt lease fence remains current and unexpired.
12. On retryable failure, append `RETRY_SCHEDULED`, clear the lease fence, and set a bounded jittered retry delay from database time.
13. On permanent/exhausted failure, append `FAILED` and expose only a safe failure code and fixed generic message.
14. If a worker dies while leased, another worker records `ABANDONED` after lease expiry and reclaims safely.

I enforce single-line work-item titles of at most 200 Unicode code points. After existing whitespace normalization, persisted title, description, assignment-reason, and notification-replay-reason fields reject U+0000 with a field-specific `400 INVALID_INPUT`; the matching OpenAPI properties exclude that code point. I made this a narrow PostgreSQL text-compatibility rule, not general Unicode or control-character sanitization. The full accepted title remains in the message body; long subjects are deterministically rune-truncated before MIME encoding. Before dialing, the SMTP adapter validates the effective sender/recipient display names as valid, control-free UTF-8 of at most 200 code points, safely folds variable headers, quoted-printable encodes the complete text body, and rejects any irreducible physical line above 998 octets. I do not reduce the public 1,000-rune assignment-reason contract to fit transport. Once connected, one sticky 1 MiB raw-transport read budget bounds all server-controlled SMTP responses and TLS records across the complete session, including STARTTLS; exceeding it is a permanent `SMTP_PROTOCOL_ERROR`.

## 13. Delivery guarantees and limitations

- **Database guarantee:** a successful assignment response cannot exist without its assignment event and durable notification row.
- **Processing guarantee:** pending rows survive API/worker restarts.
- **Worker concurrency:** multiple workers cannot claim the same row at the same time under normal PostgreSQL semantics; a worker never reserves later rows while sending its current row.
- **Completion fencing:** stale owners, tokens, attempts, and expired leases cannot complete a delivery.
- **Clock source:** PostgreSQL controls every persisted queue eligibility, lease, retry, completion, abandonment, and replay timestamp.
- **Graceful worker shutdown:** a signal stops new claims immediately, shuts down the admin listener concurrently, and gives the active SMTP attempt plus fenced database completion the configured `SHUTDOWN_TIMEOUT`. Expiry force-cancels the attempt, closes the admin listener, waits for both goroutines, and leaves any claimed row recoverable after its lease. If cancellation intersects an unhandled operation failure, the exit error retains both the grace deadline and the sender/completion failure.
- **Delivery semantics:** at least once.
- **`SENT`:** the SMTP server accepted the completed message transaction.
- **Not guaranteed:** inbox delivery, bounce-free delivery, opens, exactly once.

I accept an unavoidable generic SMTP ambiguity window: the SMTP server can accept the message and the worker can crash before recording `SENT`. Lease recovery may resend the stable `Message-ID`. A provider API with an idempotency key plus delivery webhooks would reduce this risk.

I use Mailpit to prove real SMTP transport behavior and local server acceptance. It does **not** prove external inbox delivery.

## 14. Prerequisites

### Docker workflow

- Docker Engine
- Docker Compose v2
- `curl`
- `jq`
- `git`, `python3`, `ps`, `tar`, `file`, `diff`, and `cmp` for fail-closed verification gates

### Direct Go workflow

- Go 1.26.5 (the module declares Go 1.26.0 and pins this toolchain)
- PostgreSQL 16-compatible server
- an SMTP server or provider

## 15. Fastest local run

```bash
cp .env.example .env
docker compose up --build -d
docker compose ps
./scripts/demo.sh
```

Default local endpoints:

| Surface | Address | Routes |
|---|---|---|
| Public business API | `http://127.0.0.1:8081` | authenticated `/v1/*` only |
| API admin | `http://127.0.0.1:9090` | `/health/live`, `/health/ready`, `/metrics` |
| Worker admin | `http://127.0.0.1:9091` | `/health/live`, `/health/ready`, `/metrics` |
| Mailpit UI | `http://127.0.0.1:8025` | local mail inspection |
| PostgreSQL | `127.0.0.1:5432` | local database |

I bound every Compose-published port, including SMTP on 1025, explicitly to IPv4 loopback. `API_PORT`, `API_ADMIN_PORT`, `WORKER_PORT`, `POSTGRES_PORT`, `SMTP_PORT`, and `MAILPIT_UI_PORT` override only their host ports.

I selected port 8081 as the default host API port because 8080 was occupied during initial discovery. Override with `API_PORT`.

Stop while preserving data:

```bash
docker compose down
```

Remove all local data and restart from a clean database:

```bash
make reset
docker compose up --build -d
```

I configured Compose to run the migration and seed one-shot services before starting API and worker.

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

For direct execution, `HTTP_ADDR` defaults to `:8080`, `API_ADMIN_ADDR` to `127.0.0.1:9090`, and `WORKER_HTTP_ADDR` to `127.0.0.1:9091`. The example environment binds the public API to `127.0.0.1:8081`. Compose deliberately overrides the in-container admin binds to `:9090` and `:9091`, then restricts their published host sockets to `127.0.0.1`.

In another shell with the same environment:

```bash
go run ./cmd/worker
```

Migration rollback for local development:

```bash
go run ./cmd/migrate down 1
```

I do not use destructive down migrations as an automatic production rollback strategy.

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

I store only token hashes in the database. I made the development tokens deliberately non-secret sample data, and they must never be enabled in production.

## 18. Typical end-to-end walkthrough

Set helpers:

```bash
API=http://127.0.0.1:8081
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

Open `http://127.0.0.1:8025` and confirm a message addressed to `alice.worker@example.test`.

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

I provide the executable version of this walkthrough in [`scripts/demo.sh`](scripts/demo.sh).

## 19. API contract and errors

I document the public-business OpenAPI 3.1 in [`api/openapi.json`](api/openapi.json). It documents only authenticated `/v1/*` routes; I intentionally keep administrative health and metrics endpoints outside the public contract.

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

Stable codes include authentication required/unavailable, permission denied, not found, invalid input, invalid transition, assignment conflict, concurrent modification, idempotency-key reuse, assignee/reassignment errors, notification replay conflict, and `NOTIFICATION_REPLAY_LIMIT_REACHED` after delivery cycle 10.

I do not return SQL, SMTP credentials, provider responses containing sensitive details, or internal stack traces to clients.

## 20. Real SMTP provider configuration

I use the same SMTP adapter for Mailpit and a real provider. I do not commit credentials.

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

I reject plaintext SMTP and TLS verification bypass in production configuration. `SMTP_FROM_NAME`, including an effective name embedded in `SMTP_FROM`, must be valid UTF-8, contain no control characters, and contain at most 200 Unicode code points; invalid values stop worker configuration before dependencies or listeners. It also requires PostgreSQL `sslmode=verify-full`. The API rejects `APP_ENV=production` while the development token authenticator is selected, so production cannot accidentally launch with seeded credentials.

## 21. Tests and verification

Quick local verification:

```bash
make verify
```

I use this to run formatting, module/tidy drift, vet, normal and race tests, the OpenAPI contract, the secret-pattern check, and all command builds. Database-gated tests deliberately skip when `TEST_DATABASE_URL` is absent, so I treat this as a fast development gate rather than authoritative submission evidence.

To run the database suites directly against an existing PostgreSQL database:

```bash
export TEST_DATABASE_URL='postgres://work:work@127.0.0.1:5432/work_management?sslmode=disable'
make test-db test-db-race
```

I require the following for authoritative local verification: Docker Engine/Compose, Go, `curl`, `jq`, `git`, `python3`, `ps`, `tar`, `file`, `diff`, `cmp`, and `golangci-lint`:

```bash
make verify-full
```

I designed `make verify-full` to run the static, lint, command-build, OpenAPI, secret, Dockerfile-frontend-pin, scratch-runtime-policy, rendered-Compose, and actual-image gates. The offline Dockerfile gates mutation-test frontend naming and an exact three-stage graph: one tag-and-digest-pinned Go toolchain, its internal build stage, and a final scratch runtime. The only allowed build-time network resolution is the module graph locked by `go.sum`; package-manager commands, remote `ADD`, external `COPY`, VCS fetches, and unlocked downloader inputs are rejected. Actual image layers must contain only six static repository-built commands, byte-identical tracked migrations, and the CA bundle copied from that pinned toolchain. A failure-injection gate proves `make up` resolves the API image from the rendered Compose model even when builder-specific Compose image labels are absent, then inspects it before changing either a fresh or already-running Compose project. The rendered-config gate separately requires `pull_policy: build` for the local image. Verification then owns one unique Compose project, asks Docker for six ephemeral IPv4-loopback ports, starts a fresh PostgreSQL instance, and runs uncached normal and race suites from `go test -json`. Any test-level skip is a failure. It builds and starts the full stack, requires successful migration and seed jobs, uses the bounded repository-built numeric-loopback readiness probe for API and worker, checks both administrative surfaces and the public-route boundary, runs the exact lifecycle/Mailpit/durable-state demo, and proves that all project-labeled containers, networks, and volumes are gone. The final cleanliness gate covers tracked and unignored files; the ignored local `bin/` build directory is intentional and is not represented as artifact-free cleanup.

I provide the same Compose authority without the surrounding static gates:

```bash
make compose-smoke
```

I use the suite to protect:

- the complete manager/worker lifecycle, authorization and non-disclosure policy;
- transport-invalid requests, invalid UTF-8 idempotency keys, and PostgreSQL-incompatible U+0000 text before mutation, field-specific stable errors/request IDs, ETags, replay headers, and all 11 public operations;
- immutable create-idempotency snapshots, atomic assignment/history/outbox behavior, coherent repeatable-read notification detail, concurrency conflicts, and keyset traversal;
- database assignee/creator/completed and append-only constraints plus migration down/reapply behavior;
- one-row database-time claims, UUID lease fencing, recovery, retry/exhaustion, safe failures, replay attribution, and the cycle ceiling;
- bounded partial-index claim plans, default-planner notification page plans, tied-page traversal, graceful drain/forced recovery, and shared persisted worker-ID validation;
- real plain, STARTTLS, implicit-TLS, authenticated SMTP, authentication rejection, certificate rejection, greeting timeout, cancellation, cumulative 1 MiB malicious-response bounds at every protocol stage, quoted-printable body/header-line bounds at maximum accepted inputs, and header-injection behavior;
- public/admin listener isolation, bounded metric labels, and last-good backlog caching;
- the exact public OpenAPI method/path, security, response-status, persisted-text pattern, schema, and response-header contract;
- a live current-message Mailpit acceptance increment and matching durable `SENT` attempt, without claiming external delivery.

I recorded commands and exact observed outcomes in [`docs/assessment/verification.md`](docs/assessment/verification.md). The review traceability and remaining debt are in [`docs/assessment/self-review.md`](docs/assessment/self-review.md).

## 22. Operability

- API admin liveness: `http://127.0.0.1:9090/health/live`
- API admin database readiness: `http://127.0.0.1:9090/health/ready`
- API admin metrics: `http://127.0.0.1:9090/metrics`
- Worker admin liveness/readiness/metrics: the equivalent routes on `127.0.0.1:9091`
- JSON logs to stdout
- Request ID in API responses and error bodies
- One separate `api mutation` record per authenticated create, assignment, start, complete, or notification replay result. Every record has `request_id`, `actor_id`, `operation`, and `outcome`; successes add safe resource IDs and work-item `idempotent` state, while failures add only stable `status`/`error_code` plus a validated route resource ID when available.
- Manager notification list/get includes attempts, replay history, safe failure fields, lease owner/expiry, schedule, and sent timestamps; each detail aggregate uses one read-only repeatable-read snapshot, and lease tokens remain internal
- CLI replay: `go run ./cmd/replay-notification <notification-uuid> <operator-identity> <reason>`

I require `WORKER_ID` to be valid UTF-8, trimmed nonblank, at most 255 Unicode code points, and free of control characters. Configured violations stop startup before database/listener readiness; the generated hostname/PID form is sanitized and rune-truncated to the same persisted contract. Worker construction and claim storage validate it again below configuration.

I documented detailed failure modes, alert candidates, SLO candidates, backup/restore, migration, rollback, and replay procedures in [`docs/operations.md`](docs/operations.md).

I retain at most 1,024 distinct canonical HTTP metrics `(method, route, status)` keys. New keys beyond that cap increment label-free `work_management_http_series_dropped_total` and are forgotten. Notification backlog metrics use a five-second per-process cache: one scrape refreshes while concurrent scrapes receive the previous completed state. Refresh failures retain the last good gauges and set `work_management_metrics_scrape_error`.

I intentionally exclude bearer tokens, idempotency keys, email/display names, titles, descriptions, assignment/replay reasons, request and notification payloads, and raw provider/SMTP/SQL errors. The generic `http request` access record remains separate. Work-item history is durable but has no request-ID column, so do not treat it as a replacement for live request/mutation log correlation.

## 23. Security considerations

I implemented:

- production API startup rejects the development-only authentication mode;
- every production command requires PostgreSQL `sslmode=verify-full`;
- no caller-controlled actor/role;
- hashed local tokens;
- parameterized SQL;
- strict JSON and unknown-field rejection;
- bounded request bodies and page sizes;
- HTTP and SMTP timeouts;
- minimum TLS 1.2 for SMTP TLS modes;
- a cumulative 1 MiB inbound SMTP transport budget, including implicit TLS and STARTTLS;
- secure production SMTP defaults;
- no committed real secret;
- safe client error messages;
- immutable creator/completed rows and append-only histories;
- pinned Go version, digest-pinned builder/Compose images and Dockerfile frontend, plus a package-manager-free scratch runtime containing only static commands, tracked migrations, and the pinned-toolchain CA bundle;
- separate loopback-default administrative listeners and IPv4-loopback-only Compose publications;
- finite HTTP metric method/route labels and a fixed retained-series cap;
- secret-pattern check.

Dependabot's monthly Docker checks cover `FROM` images, not the parser-frontend comment. I require the maintainer to review the syntax frontend separately and keep it tag-plus-digest pinned. I refresh it deliberately:

```bash
candidate='sha256:<reviewed-candidate-index-digest>'
tag_digest="$(docker buildx imagetools inspect docker/dockerfile:1.7 | awk '$1 == "Digest:" { print $2; exit }')"
test "$tag_digest" = "$candidate"
docker buildx imagetools inspect --raw "docker/dockerfile:1.7@$candidate" |
  jq -e '.manifests | type == "array" and length > 0 and all(.[]; .digest | startswith("sha256:"))'
```

I review the returned platform manifest set and update the canonical line in `Dockerfile`, `scripts/check-dockerfile-syntax.sh`, and `scripts/test-dockerfile-syntax-check.sh` together, then run `make dockerfile-syntax-test runtime-dockerfile-test docker-build`. Never replace the digest with a mutable tag-only directive or add package-manager, unlocked downloader, or VCS resolution to the build; the sole allowed network resolution is the `go.sum`-locked module graph.

I require a verified builder tag/digest refresh to update the matching exact instruction in `scripts/check-runtime-dockerfile.sh` in the same change; the mutation and actual-image gates then prove the intended graph and payload again.

Before production, I would add:

- external OIDC/JWT verification;
- secret manager and rotation;
- TLS PostgreSQL;
- production network policy or an authenticated proxy for administrative listeners;
- rate limiting and abuse controls;
- dependency/SBOM/vulnerability scanning;
- threat model, security review, and penetration test;
- multi-team isolation if introduced.

## 24. Known limitations and accepted debt

I accepted the following limitations and debt:

- I intentionally prevent the API from starting in `APP_ENV=production` until a production authenticator replaces the development token adapter.
- I cannot provide SMTP exactly-once delivery with this generic interface.
- I did not implement bounce, delivery, or open webhooks.
- I made replay attribution immutable, but CLI operator identity is self-asserted by a user with database/config access rather than independently authenticated.
- I did not implement a general edit or administrative correction workflow.
- I reject reassignment after work starts.
- I did not implement a cancel, delete, unassign, reopen, or retention/archive process.
- I did not add a tenant/team dimension.
- I kept the process metrics registry in-memory, so counters reset on process restart.
- I kept worker throughput sequential per process, while supporting horizontal workers.
- I left operational endpoints unauthenticated and reliant on network isolation; loopback defaults protect local execution but are not a production authorization mechanism.
- I have not established production capacity with a load or soak test.

## 25. Architecture Decision Records

1. [ADR 0001: Modular monolith](docs/adr/0001-modular-monolith.md)
2. [ADR 0002: Transactional outbox](docs/adr/0002-transactional-outbox.md)
3. [ADR 0003: Identity and authorization](docs/adr/0003-identity-and-authorization.md)
4. [ADR 0004: Lifecycle, concurrency, pagination](docs/adr/0004-lifecycle-concurrency-pagination.md)
5. [ADR 0005: Assessment `libpq` adapter](docs/adr/0005-libpq-assessment-adapter.md)
6. [ADR 0006: Maintained pgx `database/sql` driver](docs/adr/0006-pgx-stdlib-driver.md)
7. [ADR 0007: Private operational endpoints and bounded metrics](docs/adr/0007-private-operational-endpoints.md)

## 26. Required reflection answers

### 1. How did you interpret the scenario, and what assumptions did you make?

I treated the system as an operational ownership ledger rather than a generic task product. That made assignment, current responsibility, forward progress, and durable trace the core. Managers define and route work; workers execute only their assigned work. I selected a strict four-state lifecycle, manager-only creation, pre-start reassignment with a reason, immutable completion, single-team scope, and external production identity. Ambiguities and validation questions are explicit rather than hidden in code.

### 2. What were the most important architectural decisions, and what alternatives did you reject?

The highest-leverage choices were a modular monolith, PostgreSQL, a transactional outbox, application-layer authorization, append-only history beside current state, optimistic versions, leased `SKIP LOCKED` claims, and keyset pagination. I rejected microservices, synchronous SMTP, memory queues, caller-controlled identity, event sourcing, last-write-wins, and offset pagination because they either weaken correctness or add disproportionate operational cost.

### 3. How did you approach assignment, notification delivery, and traceability?

I designed assignment as one explicit transaction containing the conditional item update, assignment event, and unique outbox row. The worker handles only committed rows. Every claim outcome is immutable and failed work remains queryable. The API exposes current delivery state and attempt history. This guarantees durable intent and honest at-least-once processing without claiming inbox delivery or exactly once.

### 4. Where will this design fail first under growth, load, or team scale, and how would you address it?

PostgreSQL will become the first shared pressure point: connection limits, outbox polling, event/attempt growth, autovacuum, and hot-row conflicts. I would establish query/queue metrics and load tests first, then tune indexes, pooling, batch size, retention, and worker count. PgBouncer, partition/archive policy, read replicas for reporting, or a transactional outbox relay to a broker follow measured pressure. Team growth is addressed through ADRs, package ownership, OpenAPI, runbooks, and explicit invariants; a service split follows ownership evidence, not speculation.

### 5. What did you intentionally simplify or defer, and what debt did you accept?

I deferred production identity, tenancy, cancellation/reopen, mid-progress reassignment, webhook delivery, provider bounce reconciliation, cloud deployment, operator/configuration audit beyond the implemented replay ledger, and compliance retention. The original narrow cgo `libpq` accommodation was replaced before submission with maintained pgx while preserving `database/sql` boundaries. I also accepted process-local counters and sequential delivery inside each worker process.

### 6. What would you put in place before production?

I would add externally verified OIDC/JWT, secret management, TLS PostgreSQL/SMTP, an HA database and tested restores, a backward-compatible migration policy, RED and queue dashboards, agreed SLOs/alerts, distributed tracing, rate limits, threat modeling, dependency/SBOM scanning, signed builds, load/failure testing, runbooks, on-call ownership, incident exercises, provider idempotency/webhooks, and a privacy/retention policy.

### 7. How would you evolve for audit history, webhooks, or multi-team usage?

For audit, I would extend the immutable replay ledger to other operator/configuration events and define retention and export policy. For webhooks, I would add subscriptions, signing secrets, a separate transactional webhook outbox, delivery attempts, replay, endpoint disablement, and ordering/version semantics. For multi-team, I would add `team_id` to every ownership and idempotency boundary, backfill safely, scope every index/query, derive team from trusted identity, consider RLS as defense in depth, and test cross-tenant non-disclosure.

### 8. How would you onboard another engineer?

I would onboard another engineer with the README walkthrough, lifecycle and authorization matrix, architecture diagrams, and ADRs. I would have them run the domain tests, then the PostgreSQL integration flow, inspect one assignment transaction and one notification claim, execute the demo, and practice a failed-notification replay. I would then ask the new owner to make a small behavior change with an ADR/test update and walk through the failure-mode/runbook implications in review.

## 27. AI usage disclosure

I used OpenAI AI coding assistants to help me structure the ambiguity analysis and implementation plan; draft and revise Go, SQL, tests, OpenAPI, Compose/CI configuration, diagrams, and documentation; enumerate failure and security cases; and compare implementation behavior with the written contract.

I treated AI output as a draft, not as verification. I corrected defects and unsupported assumptions that tests and review found in the repository. I recorded the executable gates, deliberate negative cases, and exact observed outcomes in [`docs/assessment/verification.md`](docs/assessment/verification.md), while [`docs/assessment/self-review.md`](docs/assessment/self-review.md) maps requirements to implementation and evidence.

I remain responsible for the final architecture, trade-offs, source, and claims, and I can explain and defend every included decision during the technical debrief.
