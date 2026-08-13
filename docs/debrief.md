# Ten-Minute Technical Debrief

This guide is a speaking outline, not a claim of production readiness. The executable evidence is in [`assessment/verification.md`](assessment/verification.md).

## 0:00–0:45 — Scenario, scope, and non-goals

This service is an operational ownership ledger. Managers create and route work; workers see and advance only work currently assigned to them. The assessment focuses on trusted identity boundaries, durable state, concurrency, traceability, and failure visibility. It deliberately defers production identity issuance, tenancy, general editing, cancellation/reopen, cloud deployment, and provider delivery webhooks.

## 0:45–1:45 — Lifecycle and authorization

```text
UNASSIGNED -> ASSIGNED -> IN_PROGRESS -> COMPLETED
                 \-> ASSIGNED (manager reassignment with reason)
```

Managers create, list, read, assign/reassign, inspect history, and inspect/replay notifications. Managers cannot start or complete. A worker lists their own queue, reads their own item, and performs only `ASSIGNED -> IN_PROGRESS -> COMPLETED`. Another worker receives a non-disclosing 404. Completed work is immutable in both service policy and database guards.

## 1:45–2:30 — Modular monolith and listener boundaries

One Go module and PostgreSQL schema produce separate API and worker processes. This keeps assignment, history, and outbox creation in one transaction without a distributed dual write while allowing worker scaling independent of API request handling. The API has an authenticated public listener and a separately bound admin listener; the worker has its own admin listener. Health and metrics are unauthenticated, excluded from OpenAPI, loopback-published in Compose, and still require private production ingress.

## 2:30–3:30 — Atomic assignment, event, and outbox

Assignment begins an explicit PostgreSQL transaction, validates the authenticated manager, assignee role, lifecycle, and expected version, then conditionally updates `work_items`, appends `work_item_events`, and inserts a unique `PENDING` delivery with a stable `Message-ID`. Commit makes all three visible together. SMTP never runs inside the request transaction, so provider latency cannot hold locks or roll back ownership.

## 3:30–4:45 — ClaimNext, database time, fencing, and recovery

Each send iteration fetches PostgreSQL `clock_timestamp()` once, reads at most one eligible row from each ordered partial index under `FOR UPDATE SKIP LOCKED`, and chooses the earlier eligibility/creation/UUID boundary in Go. The claim gets a fresh database UUID token. Completion requires the same delivery, owner, token, attempt number, and an unexpired lease. A crashed worker cannot later complete stale work. After expiry, another worker appends an `ABANDONED` attempt and reclaims with a new token. Batch size limits sequential iterations; it does not reserve many leases while one SMTP call runs.

## 4:45–5:45 — SMTP boundary, safe failures, and replay

The adapter supports plain SMTP for local Mailpit, verified STARTTLS, and implicit TLS, with optional AUTH and bounded/cancelable operations. Titles reject CR/LF before persistence, long Unicode titles retain their full body text while the subject is rune-safely bounded, and the SMTP layer still rejects header newlines before dialing. `SENT` means the server accepted the completed SMTP transaction, not inbox placement or final delivery. Acceptance followed by a worker crash before the database update can cause a resend; the stable `Message-ID` helps reconciliation but cannot make generic SMTP exactly once. Only allowlisted codes and fixed generic messages persist. A manager can replay a terminal failure with a reason; replay attribution and state commit together and stop at cycle 10. CLI actor identity is explicitly self-asserted by an operator with database/config access.

## 5:45–6:30 — Idempotency and optimistic concurrency

Create stores actor, operation, client key, request hash, resource, and the immutable first-response JSON. A matching retry returns that original representation even after later mutations; a different payload under the same key fails. Assignment and transitions use ETag/`If-Match` versions. Same-assignee and same-target repeats are deterministic no-ops; stale conflicting commands are explicit rather than last-write-wins.

## 6:30–7:15 — Keyset indexes and query behavior

Work and notification lists use `(created_at DESC, id DESC)` boundaries; history uses per-item sequence order. Opaque cursors seek into indexes rather than using offsets. Large tied-timestamp tests traverse pages without duplicates or omissions and use default-planner JSON `EXPLAIN` evidence that rejects sequential scans and all sort nodes for each notification query shape. Inserts between pages do not create a frozen snapshot; the contract is stable forward traversal from the supplied boundary.

## 7:15–8:15 — Observability and administrative behavior

Both processes expose liveness, database readiness, and Prometheus text metrics on admin listeners. HTTP method/route labels are closed, retained `(method, route, status)` keys are capped at 1,024, and rejected keys increment a label-free drop counter. Notification backlog queries use a five-second last-good cache: one refresh proceeds, concurrent scrapes receive completed state, and failures retain gauges while setting an explicit scrape-error metric. Counters are process-local and reset on restart.

## 8:15–9:15 — Failure scenarios, trade-offs, and debt

- PostgreSQL outage makes both processes unready and blocks the source of truth.
- SMTP outage leaves assignments committed while retryable/failed deliveries remain visible.
- Worker death pauses delivery until restart or expired-lease recovery.
- Hot-item races produce `CONCURRENT_MODIFICATION` and require a refetch.
- The first scaling pressure is likely PostgreSQL connections, polling/index churn, event growth, or hot rows; measure before adding a broker or service split.
- Production still needs verified external identity, secret management, HA/restore exercises, private operational ingress, provider webhooks, rate limits, load/failure testing, and retention policy.

## 9:15–10:00 — Live demo and evidence

Fast local reviewer flow:

```bash
cp .env.example .env
docker compose up --build -d
./scripts/demo.sh
docker compose down --volumes --remove-orphans
```

The demo proves a current manager-created/assigned item, manager progress denial, other-worker non-disclosure, assigned-worker completion and ordered history, one current Mailpit SMTP-acceptance increment with recipient/subject/body, matching normalized `Message-ID`, and one durable `SENT` attempt. It does not prove an external inbox.

Authoritative gate:

```bash
make verify-full
```

See [`assessment/verification.md`](assessment/verification.md) for commands and exact observed results and [`assessment/self-review.md`](assessment/self-review.md) for requirement traceability.

## Likely interviewer questions

### Why PostgreSQL and an outbox instead of synchronous SMTP or a broker?

The ownership update, audit event, and notification intent need one atomic boundary. PostgreSQL already supplies that boundary. Synchronous SMTP adds remote latency and ambiguous rollback; a broker does not remove the database/broker dual write unless an outbox still exists. A broker becomes justified only after measured queue pressure or separate platform ownership.

### Why is delivery not exactly once?

Generic SMTP has no application idempotency key. The server can accept `DATA` and the worker can die before recording `SENT`, so recovery may resend. Database intent and completion fencing are strong; external delivery remains at least once.

### What do owner, token, attempt, and lease expiry each add?

Owner identifies the claimant, the fresh unpredictable token fences different claims by the same owner, attempt fences retry generations, and unexpired lease prevents late completion after recovery eligibility. Removing any one weakens stale-worker protection.

### Why use PostgreSQL time rather than process time?

Multiple workers can have skewed clocks. Database time gives claims, completion, retries, abandonment, and replay scheduling one transactional authority.

### Why enforce authorization below HTTP?

Application-service enforcement means a future CLI, job, or transport cannot bypass role, ownership, lifecycle, and non-disclosure rules merely by avoiding the HTTP handler.

### How does keyset traversal behave while new rows arrive?

The next cursor continues strictly after the last returned sort boundary, avoiding offset duplication/skip behavior. It is not a frozen snapshot; newly inserted rows ahead of the boundary appear on a later fresh traversal.

### Where will the design fail first?

Likely at the shared PostgreSQL boundary: connection count, outbox polling, growing append-only tables, autovacuum, or hot-item conflicts. Metrics, query plans, and load tests should identify which limit before partitioning, pooling, replicas, or a relay/broker.

### What changes before production?

Replace development tokens with verified OIDC/JWT, use managed secrets and TLS, private/authenticated admin ingress, HA PostgreSQL and tested restores, migration compatibility policy, rate limits, tracing, signed/SBOM-scanned builds, capacity tests, on-call runbooks, provider webhooks, and privacy/retention controls.

### What does Mailpit prove?

It proves the actual worker opened an SMTP connection, completed a message transaction accepted by the local server, and produced content/headers matching the durable row. It proves neither external provider behavior nor inbox delivery.
