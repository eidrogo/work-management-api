# Submission Self-Review

## 1. Review metadata and ancestry

- Review date: 2026-08-13 (America/Argentina/Buenos_Aires).
- Repository branch: `codex/submission-hardening`.
- Task 4 base: `8827e53bff9e5e52058d0fd7b1f0af1b777b6ab6`.
- Preserved hardening ancestry: Task 1 baseline `ed59c2c`, Task 2 `cf97716`, Task 3/base `8827e53`.
- Review scope: the complete submission tree, including final whole-branch reliability fixes atop the Task 4 base. A final hash is intentionally not embedded in a file that contributes to the hash.

## 2. Requirement traceability

| Requirement | Implementation | Evidence surface |
|---|---|---|
| Manager-defined, worker-executed lifecycle | `internal/workitems`, `internal/httpapi` | domain matrices plus `TestHTTPManagerWorkerLifecycleAndNotificationContract`; live demo |
| Role/ownership enforcement and non-disclosure | application services below transport | HTTP manager denial, Bob 404/queue exclusion, Alice-only progress in `scripts/demo.sh` |
| Atomic assignment, audit, and notification intent | explicit PostgreSQL transaction in work-item store | rollback/concurrency integration tests; lifecycle integration; migration constraints |
| Durable asynchronous SMTP | notification outbox/worker and `internal/platform/email` | real-wire plain, STARTTLS, implicit TLS, AUTH, certificate/timeout tests; Mailpit demo |
| Traceable attempts and replay | append-only attempt/replay tables; manager API and CLI | notification integration tests; manager detail in demo; operations runbook |
| Stable retry/idempotency/concurrency contract | immutable create snapshot, deterministic no-ops, ETags/`If-Match` | Task 2 persistence/domain tests; Task 4 HTTP headers and error matrix |
| Complete public API contract | `api/openapi.json`; exact public router; no admin routes | exhaustive 11-operation OpenAPI matrix, recursive required-header validation, and authenticated trailing-slash/no-call matrix |
| Private operational surface and bounded metrics | separate API/worker admin listeners, label cap, backlog cache | Task 3 tests plus live public/admin checks in `scripts/verify-compose.sh` |
| Authoritative clean verification | unique Compose project, ephemeral loopback overlay, owned child process groups | prompt INT/TERM regression, JSON normal/race no-skip audit, live demo, label cleanup; `verification.md` |
| Explainable architecture and limitations | README, ADRs, architecture, operations, debrief | this review and `docs/debrief.md` |

## 3. Lifecycle, authorization, and invariants

The implemented lifecycle is closed to `UNASSIGNED -> ASSIGNED -> IN_PROGRESS -> COMPLETED`, with reasoned reassignment only while `ASSIGNED`. Managers own creation/assignment/inspection but never worker progress. Workers can see and progress only their current assignment; a different worker receives a non-disclosing 404. Creator immutability, worker-only assignees, state/assignee consistency, append-only histories, and completed immutability are enforced in service logic and database guards. HTTP tests cover all public operations, malformed transport/precondition inputs before mutation, request correlation, and stable mapped errors.

## 4. Transactions, concurrency, and idempotency

Assignment update, event append, and initial delivery insert share one transaction and have a forced rollback test. Expected versions make conflicting assignments/transitions explicit. Create idempotency stores an immutable first-response snapshot, so a replay does not silently change after later mutations. Same-assignee and same-target transition retries are deterministic no-ops with replay metadata/headers. Notification claims fetch one PostgreSQL time, lock at most one candidate from each ordered partial index, and select one row immediately before send; owner, fresh UUID token, attempt, and unexpired lease jointly fence completion. Notification detail reads current state and histories in one read-only repeatable-read transaction; deterministic traced interleavings prove a concurrent completion or replay cannot create a mixed response. Concurrent claim, cross-state order, expiry, stale-completion, retry, and replay behavior are covered. Signal lifecycle tests prove active completion within grace and bounded force-cancel/lease recovery while reaping worker and admin goroutines.

## 5. Notification, SMTP, replay, and privacy

The worker uses the production SMTP adapter in the live stack. Tests exercise plain, verified STARTTLS (including the post-TLS EHLO), implicit TLS, real AUTH success, real 535 rejection, certificate rejection in both TLS modes, greeting timeout, cancellation of a blocked post-DATA transaction, and header-injection rejection before dialing. Domain/HTTP tests reject CR/LF titles with zero persistence; accepted 200-rune worst-case Unicode titles keep their full body title while a bounded subject remains within the SMTP header-line limit. Exact MIME and real line-enforcing TCP tests cover 1,000-rune ASCII/emoji reasons, 200-rune ASCII/two-byte/four-byte sender and recipient names, long addresses, decoded full content, and the 998-octet ceiling. Persisted/client-visible failures are restricted to allowlisted codes and fixed generic messages. Provider, authentication, certificate, and credential text is not durable API data.

`SENT` is deliberately defined as SMTP server acceptance after the completed message transaction. Generic SMTP retains an acceptance-before-database-update duplicate window and cannot prove inbox delivery. Mailpit proves local protocol acceptance and current message content only. HTTP replay attributes an authenticated manager and request ID; the database-access CLI actor is required but self-asserted. Replay is reasoned, append-only, atomic with state, and capped at cycle 10.

## 6. API, OpenAPI, and listener consistency

The public router exposes exactly 11 method/path pairs across ten `/v1` templates. Authenticated collection, item, notification, and action paths with trailing slashes return the correlated stable 404 envelope before any application call. The contract test closes the documented operation set, requires the top-level bearer scheme without operation overrides, exact response statuses, JSON error envelopes, closed error schemas, response request IDs, 401 challenges, ETags, and idempotent replay headers. Every required header and nested schema reference is recursively resolved as a local reference; dangling, cyclic, non-local, and malformed references fail, while X-Request-ID bounds, the exact bearer challenge, quoted positive ETags, and replay `true` semantics are pinned. Operational paths are absent from the document and return stable public 404 envelopes. The API pre-binds public and admin sockets before serving; an occupied admin address closes the public listener. Readiness passes a two-second deadline to the dependency probe.

## 7. Schema, indexes, and migrations

Migration tests apply into isolated schemas, validate checks/foreign keys/triggers, apply the local down migration, and reapply successfully. Direct SQL tests cover worker-assignee and role-change protection, immutable creator/completed rows, and append-only work-item/attempt histories. Existing Task 2 tests cover notification replay append-only state and outbox/idempotency constraints. Large default-planner JSON `EXPLAIN` tests reject sequential scans and sort nodes, require both claim indexes, and require each unfiltered/status/work-item notification page index; tied traversal covers all page shapes.

## 8. Observability, operations, and security

Administrative routes are isolated on API and worker admin listeners; Compose host publications are explicit IPv4 loopback. HTTP labels use closed method/route values and a 1,024-series retained cap. Backlog gauges use five-second single-refresh/last-good caching with an explicit scrape-error signal. Operations documentation now includes listener semantics, safe notification diagnosis/replay, worker recovery, migration/checksum guidance, backup/restore expectations, destructive cleanup warnings, troubleshooting, and production gaps.

Production safeguards already present include rejection of development authentication in production, PostgreSQL `verify-full` requirements, verified SMTP policy, strict/bounded JSON, parameterized SQL, SHA-256 local token lookup pinned by an independent known digest and exact store-argument test, bounded timeouts, safe error contracts, loopback local publications, pinned dependencies/images/actions, and secret-pattern checks. These do not replace threat modeling, external identity, managed secrets, private ingress, rate limits, HA/restore exercises, or load/security testing.

## 9. Accepted debt and non-goals

- Generic SMTP is at least once and has no delivery/bounce/open proof.
- CLI replay identity is self-asserted by a database/config operator.
- Metrics are process-local and counters reset on restart.
- Admin endpoints are unauthenticated and rely on a network boundary.
- Worker sending is sequential per process; horizontal workers are supported.
- No tenant dimension, cancel/delete/unassign/reopen, active-work reassignment, general edit/correction flow, production identity, provider webhooks, or retention/archive mechanism.
- No load/soak evidence establishes production capacity.
- `make build` leaves an intentionally ignored `bin/` directory; the clean gate means tracked and unignored cleanliness, not absence of all generated artifacts.

## 10. Resolved and remaining findings

Resolved during hardening:

- transition replays returned idempotent metadata without the documented replay header;
- wrapped exported sentinels could fall through to unstable internal error mapping;
- the OpenAPI contract omitted runtime 503s, 401 challenges, closed error schemas, and transition replay headers;
- normal local tests could appear green while database tests skipped;
- the demo could report success after poll exhaustion and could match stale mail;
- fixed Compose projects/ports and unchecked teardown made concurrent or failed verification ambiguous;
- CI duplicated a fixed PostgreSQL service instead of using the single Compose authority;
- SMTP TLS/AUTH/certificate behavior and several database boundary invariants lacked direct coverage;
- delivery, listener, verification, and AI-assistance documentation contained claims broader than executable evidence;
- the verifier deferred trapped signals while foreground children ran; all long Compose/test/demo children are now isolated, signaled as owned process groups, promptly reaped, and covered on the CI `compose-smoke` path;
- undocumented trailing-slash item, notification, and action aliases could reach the application instead of returning the stable 404 contract;
- required OpenAPI header keys could point to dangling references without failing the contract gate;
- authentication hashing coverage compared the production helper to itself instead of pinning SHA-256 and the exact store boundary independently.
- worker signals canceled active SMTP work instead of draining it; the lifecycle now stops claims separately, drains within the configured budget, force-cancels only at expiry, and reaps both components;
- notification claims and page ordering defeated their intended indexes; bounded per-state claim selection and qualified list boundaries now have large default-planner plan regressions;
- persisted titles could form invalid or overlong SMTP Subject headers; single-line validation, zero-side-effect HTTP evidence, bounded subject rendering, and SMTP defense in depth close the gap;
- persisted worker IDs were not validated against their 255-character columns; one UTF-8/rune/control contract now gates startup, worker construction, and claim storage.
- forced grace cancellation could replace a genuine sender/database completion error with `context.Canceled`; operation errors now take precedence over sampled context state, send/completion errors are joined when completion observes cancellation, and the coordinator retains both the grace deadline and worker error.
- accepted reasons and display names could exceed SMTP physical-line limits and become permanently undeliverable; bounded transfer encoding/header folding plus pre-dial validation now preserve full decoded content within 998 octets.
- notification detail could combine an old delivery row with newly committed attempt/replay history; one repeatable-read aggregate snapshot and deterministic concurrent regressions close the inconsistency.

No additional in-scope findings were identified after the independent follow-up and focused correction; candidate evidence is recorded in [`verification.md`](verification.md), and required post-commit authority is captured in the external final fix reports. The accepted debt above remains deliberate and prevents a production-readiness claim.

## 11. Verification evidence

Only actually executed commands and exact observed results are recorded in [`docs/assessment/verification.md`](verification.md). Raw transient logs are intentionally outside the repository and are referenced by the final fix report.
