# Ten-Minute Technical Debrief

I use this guide as a speaking outline, not a claim of production readiness. I recorded the executable evidence in [`assessment/verification.md`](assessment/verification.md).

## 0:00–0:45 — Scenario, scope, and non-goals

I designed this service as an operational ownership ledger. Managers create and route work; workers see and advance only work currently assigned to them. I focused the assessment on trusted identity boundaries, durable state, concurrency, traceability, and failure visibility. I deliberately deferred production identity issuance, tenancy, general editing, cancellation/reopen, cloud deployment, and provider delivery webhooks.

## 0:45–1:45 — Lifecycle and authorization

```text
UNASSIGNED -> ASSIGNED -> IN_PROGRESS -> COMPLETED
                 \-> ASSIGNED (manager reassignment with reason)
```

I enforce that managers create, list, read, assign/reassign, inspect history, and inspect/replay notifications. Managers cannot start or complete. A worker lists their own queue, reads their own item, and performs only `ASSIGNED -> IN_PROGRESS -> COMPLETED`. Another worker receives a non-disclosing 404. Completed work is immutable in both service policy and database guards.

## 1:45–2:30 — Modular monolith and listener boundaries

I chose one Go module and PostgreSQL schema to produce separate API and worker processes. This keeps assignment, history, and outbox creation in one transaction without a distributed dual write while allowing worker scaling independent of API request handling. I gave the API an authenticated public listener and a separately bound admin listener; the worker has its own admin listener. I leave health and metrics unauthenticated, exclude them from OpenAPI, publish them to loopback in Compose, and still require private production ingress.

## 2:30–3:30 — Atomic assignment, event, and outbox

I designed assignment to begin an explicit PostgreSQL transaction that validates the authenticated manager, assignee role, lifecycle, and expected version, then conditionally updates `work_items`, appends `work_item_events`, and inserts a unique `PENDING` delivery with a stable `Message-ID`. Commit makes all three visible together. SMTP never runs inside the request transaction, so provider latency cannot hold locks or roll back ownership.

## 3:30–4:45 — ClaimNext, database time, fencing, and recovery

I designed each send iteration to fetch PostgreSQL `clock_timestamp()` once, read at most one eligible row from each ordered partial index under `FOR UPDATE SKIP LOCKED`, and choose the earlier eligibility/creation/UUID boundary in Go. The claim gets a fresh database UUID token. Completion requires the same delivery, owner, token, attempt number, and an unexpired lease. A crashed worker cannot later complete stale work. After expiry, another worker appends an `ABANDONED` attempt and reclaims with a new token. Batch size limits sequential iterations; it does not reserve many leases while one SMTP call runs.

## 4:45–5:45 — SMTP boundary, safe failures, and replay

I designed the adapter to support plain SMTP for local Mailpit, verified STARTTLS, and implicit TLS, with optional AUTH and bounded/cancelable operations. I set one cumulative 1 MiB budget below the TLS layer to bound all server response and TLS-record reads for the entire session. I reject CR/LF in titles before persistence and retain long Unicode titles' full body text while rune-safely bounding the subject. The SMTP layer prebuilds the full MIME payload before dialing, validates effective display names, folds structured headers, and quoted-printable encodes the complete body so maximum accepted names and 1,000-rune reasons remain under the 998-octet physical-line ceiling. I define `SENT` as the server accepting the completed SMTP transaction, not inbox placement or final delivery. Acceptance followed by a worker crash before the database update can cause a resend; the stable `Message-ID` helps reconciliation but cannot make generic SMTP exactly once. I persist only allowlisted codes and fixed generic messages. A manager can replay a terminal failure with a reason; replay attribution and state commit together and stop at cycle 10. I explicitly treat CLI actor identity as self-asserted by an operator with database/config access.

## 5:45–6:30 — Idempotency and optimistic concurrency

I designed create to store actor, operation, client key, request hash, resource, and the immutable first-response JSON. A matching retry returns that original representation even after later mutations; a different payload under the same key fails. Assignment and transitions use ETag/`If-Match` versions. Same-assignee and same-target repeats are deterministic no-ops; stale conflicting commands are explicit rather than last-write-wins.

## 6:30–7:15 — Keyset indexes and query behavior

I use `(created_at DESC, id DESC)` boundaries for work and notification lists and per-item sequence order for history. I use opaque cursors to seek into indexes rather than offsets. I verify that large tied-timestamp tests traverse pages without duplicates or omissions and use default-planner JSON `EXPLAIN` evidence that rejects sequential scans and all sort nodes for each notification query shape. I do not promise a frozen snapshot across inserts between pages; the contract is stable forward traversal from the supplied boundary. A single notification detail, unlike a multi-page traversal, reads current state and both histories from one read-only repeatable-read snapshot.

## 7:15–8:15 — Observability and administrative behavior

Both processes expose liveness, database readiness, and Prometheus text metrics on admin listeners. HTTP method/route labels are closed, retained `(method, route, status)` keys are capped at 1,024, and rejected keys increment a label-free drop counter. Notification backlog queries use a five-second last-good cache: one refresh proceeds, concurrent scrapes receive completed state, and failures retain gauges while setting an explicit scrape-error metric. Counters are process-local and reset on restart.

## 8:15–9:15 — Failure scenarios, trade-offs, and debt

- I expect a PostgreSQL outage to make both processes unready and block the source of truth.
- I leave assignments committed during an SMTP outage while retryable/failed deliveries remain visible.
- I expect worker death to pause delivery until restart or expired-lease recovery.
- I surface hot-item races as `CONCURRENT_MODIFICATION` and require a refetch.
- I expect the first scaling pressure to be PostgreSQL connections, polling/index churn, event growth, or hot rows; I would measure before adding a broker or service split.
- Before production, I would add verified external identity, secret management, HA/restore exercises, private operational ingress, provider webhooks, rate limits, load/failure testing, and retention policy.

## 9:15–10:00 — Live demo and evidence

Fast local reviewer flow:

```bash
cp .env.example .env
docker compose up --build -d
./scripts/demo.sh
docker compose down --volumes --remove-orphans
```

I use the demo to prove a current manager-created/assigned item, manager progress denial, other-worker non-disclosure, assigned-worker completion and ordered history, one current Mailpit SMTP-acceptance increment with recipient/subject/body, matching normalized `Message-ID`, and one durable `SENT` attempt. It does not prove an external inbox.

Authoritative gate:

```bash
make verify-full
```

I recorded commands and exact observed results in [`assessment/verification.md`](assessment/verification.md) and requirement traceability in [`assessment/self-review.md`](assessment/self-review.md).

## Likely interviewer questions

### Why PostgreSQL and an outbox instead of synchronous SMTP or a broker?

I chose PostgreSQL and an outbox because the ownership update, audit event, and notification intent need one atomic boundary. PostgreSQL already supplies that boundary. I rejected synchronous SMTP because it adds remote latency and ambiguous rollback; I would not add a broker until measured queue pressure or separate platform ownership justifies it, because it does not remove the database/broker dual write unless an outbox still exists.

### Why is delivery not exactly once?

I cannot make generic SMTP exactly once because it has no application idempotency key. The server can accept `DATA` and the worker can die before recording `SENT`, so recovery may resend. I use database intent and completion fencing, while external delivery remains at least once.

### What do owner, token, attempt, and lease expiry each add?

I use owner to identify the claimant, a fresh unpredictable token to fence different claims by the same owner, attempt to fence retry generations, and unexpired lease to prevent late completion after recovery eligibility. I would not remove any one because it would weaken stale-worker protection.

### Why use PostgreSQL time rather than process time?

I chose PostgreSQL time because multiple workers can have skewed clocks. Database time gives claims, completion, retries, abandonment, and replay scheduling one transactional authority.

### Why enforce authorization below HTTP?

I enforce authorization in the application service so a future CLI, job, or transport cannot bypass role, ownership, lifecycle, and non-disclosure rules merely by avoiding the HTTP handler.

### How does keyset traversal behave while new rows arrive?

I designed the next cursor to continue strictly after the last returned sort boundary, avoiding offset duplication/skip behavior. I do not offer a frozen snapshot; newly inserted rows ahead of the boundary appear on a later fresh traversal.

### Where will the design fail first?

I expect the design to fail first at the shared PostgreSQL boundary: connection count, outbox polling, growing append-only tables, autovacuum, or hot-item conflicts. I would use metrics, query plans, and load tests to identify which limit before partitioning, pooling, replicas, or a relay/broker.

### What changes before production?

I would replace development tokens with verified OIDC/JWT and add managed secrets and TLS, private/authenticated admin ingress, HA PostgreSQL and tested restores, a migration compatibility policy, rate limits, tracing, signed/SBOM-scanned builds, capacity tests, on-call runbooks, provider webhooks, and privacy/retention controls.

### What does Mailpit prove?

I can demonstrate that the actual worker opened an SMTP connection, completed a message transaction accepted by the local server, and produced content/headers matching the durable row. I cannot demonstrate external provider behavior or inbox delivery with Mailpit.
