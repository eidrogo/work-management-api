# Submission Self-Review

## 1. Review metadata and ancestry

- Review date: 2026-08-13 (America/Argentina/Buenos_Aires).
- Repository branch: `feature/submission-hardening`.
- Task 4 base: `8827e53bff9e5e52058d0fd7b1f0af1b777b6ab6`.
- Preserved hardening ancestry: Task 1 baseline `ed59c2c`, Task 2 `cf97716`, Task 3/base `8827e53`.
- Review scope: I reviewed the complete submission tree, including final whole-branch reliability fixes atop the Task 4 base. I intentionally did not embed a final hash in a file that contributes to the hash.

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

I implemented a lifecycle closed to `UNASSIGNED -> ASSIGNED -> IN_PROGRESS -> COMPLETED`, with reasoned reassignment only while `ASSIGNED`. Managers own creation/assignment/inspection but never worker progress. Workers can see and progress only their current assignment; a different worker receives a non-disclosing 404. I enforce creator immutability, worker-only assignees, state/assignee consistency, append-only histories, and completed immutability in service logic and database guards. I wrote HTTP tests covering all public operations, malformed transport/precondition inputs before mutation, request correlation, and stable mapped errors. I made idempotency keys reject invalid UTF-8 before ID generation or a transaction; title, description, assignment reason, and replay reason reject PostgreSQL-incompatible U+0000 after their existing normalization and authorization boundaries. I used live tests to prove authentication/authorization precedence, field-specific 400 responses, no side effects or false 500 telemetry, and successful valid Unicode follow-ups.

## 4. Transactions, concurrency, and idempotency

I made the assignment update, event append, and initial delivery insert share one transaction and added a forced rollback test. I used expected versions to make conflicting assignments/transitions explicit. I made create idempotency store an immutable first-response snapshot, so a replay does not silently change after later mutations. I made same-assignee and same-target transition retries deterministic no-ops with replay metadata/headers. Notification claims fetch one PostgreSQL time, lock at most one candidate from each ordered partial index, and select one row immediately before send; owner, fresh UUID token, attempt, and unexpired lease jointly fence completion. I made notification detail read current state and histories in one read-only repeatable-read transaction; deterministic traced interleavings prove a concurrent completion or replay cannot create a mixed response. I covered concurrent claim, cross-state order, expiry, stale-completion, retry, and replay behavior. I used signal lifecycle tests to prove active completion within grace and bounded force-cancel/lease recovery while reaping worker and admin goroutines.

## 5. Notification, SMTP, replay, and privacy

The worker uses the production SMTP adapter in the live stack. I exercised plain, verified STARTTLS (including pre/post-upgrade EHLO and one cumulative transport quota), implicit TLS with AUTH, real 535 rejection, certificate rejection in both TLS modes, greeting timeout, cancellation of a blocked post-DATA transaction, and header-injection rejection before dialing. Real malicious servers send oversized single-line and multiline responses at greeting, EHLO, STARTTLS, AUTH, envelope, DATA, and post-DATA stages; one sticky 1 MiB raw-transport budget bounds the complete session and maps exhaustion to a permanent safe protocol failure. A payload write failure does not invoke the net/smtp DATA closer's additional response read, and the accepted post-DATA path closes without QUIT so an already accepted message is not retried. I wrote domain/HTTP tests that reject CR/LF titles with zero persistence; accepted 200-rune worst-case Unicode titles keep their full body title while a bounded subject remains within the SMTP header-line limit. I used exact MIME and real line-enforcing TCP tests to cover 1,000-rune ASCII/emoji reasons, 200-rune ASCII/two-byte/four-byte sender and recipient names, long addresses, decoded full content, and the 998-octet ceiling. I restricted persisted/client-visible failures to allowlisted codes and fixed generic messages. I kept provider, authentication, certificate, and credential text out of durable API data.

I deliberately defined `SENT` as SMTP server acceptance after the completed message transaction. Generic SMTP retains an acceptance-before-database-update duplicate window and cannot prove inbox delivery. Mailpit proves local protocol acceptance and current message content only. I made HTTP replay attribute an authenticated manager and request ID; the database-access CLI actor is required but self-asserted. I made replay reasoned, append-only, atomic with state, and capped at cycle 10.

## 6. API, OpenAPI, and listener consistency

I made the public router expose exactly 11 method/path pairs across ten `/v1` templates. Authenticated collection, item, notification, and action paths with trailing slashes return the correlated stable 404 envelope before any application call. I closed the documented operation set in the contract test and required the top-level bearer scheme without operation overrides, exact response statuses, JSON error envelopes, closed error schemas, response request IDs, 401 challenges, ETags, and idempotent replay headers. I recursively resolve every required header and nested schema reference as a local reference; dangling, cyclic, non-local, and malformed references fail, while I pin X-Request-ID bounds, the exact bearer challenge, quoted positive ETags, replay `true` semantics, and the four exact U+0000-excluding persisted-text patterns. Removal or weakening of any of those four patterns fails a mutation gate. I kept operational paths out of the document and configured the public router to return stable 404 envelopes for them. The API pre-binds public and admin sockets before serving; an occupied admin address closes the public listener. Readiness passes a two-second deadline to the dependency probe.

## 7. Schema, indexes, and migrations

I wrote migration tests that apply into isolated schemas, validate checks/foreign keys/triggers, apply the local down migration, and reapply successfully. I used direct SQL tests to cover worker-assignee and role-change protection, immutable creator/completed rows, and append-only work-item/attempt histories. I retained the Task 2 tests covering notification replay append-only state and outbox/idempotency constraints. I wrote large default-planner JSON `EXPLAIN` tests that reject sequential scans and sort nodes, require both claim indexes, and require each unfiltered/status/work-item notification page index; tied traversal covers all page shapes.

## 8. Observability, operations, and security

I isolated administrative routes on API and worker admin listeners; Compose host publications are explicit IPv4 loopback. I made HTTP labels use closed method/route values and a 1,024-series retained cap. I implemented five-second single-refresh/last-good caching for backlog gauges with an explicit scrape-error signal. I documented listener semantics, safe notification diagnosis/replay, worker recovery, migration/checksum guidance, backup/restore expectations, destructive cleanup warnings, troubleshooting, and production gaps.

I implemented production safeguards that include rejection of development authentication in production, PostgreSQL `verify-full` requirements, verified SMTP policy, strict/bounded JSON, parameterized SQL, SHA-256 local token lookup pinned by an independent known digest and exact store-argument test, bounded timeouts and SMTP input bytes, safe error contracts, loopback local publications, pinned dependencies/images/actions plus a digest-pinned Dockerfile syntax frontend, and secret-pattern checks. I made the offline syntax gate case-insensitively cover tracked `Dockerfile`, `Dockerfile.*`, and `*.Dockerfile` basename forms. I added a second mutation gate that permits only the exact digest-pinned toolchain → internal build → scratch-runtime graph: it allows only the `go.sum`-locked module resolution step and rejects package-manager, unlocked downloader, VCS, remote ADD, and external COPY inputs. I made the actual image gate prove a numeric nonroot user, fixed entrypoint/CA environment, six static commands, byte-identical tracked migrations, the pinned-toolchain CA bundle, and no other layer paths. I placed these gates before direct Docker builds, local `up`, and Compose smoke; the Compose run proves all four application entrypoints and both readiness probes. Dependabot covers `FROM` images, while maintainers manually verify and refresh the parser-frontend OCI index digest with the documented Buildx procedure. I do not treat these safeguards as replacements for threat modeling, external identity, managed secrets, private ingress, rate limits, HA/restore exercises, or load/security testing.

## 9. Accepted debt and non-goals

I accepted the following debt and non-goals:

- Generic SMTP is at least once and has no delivery/bounce/open proof.
- CLI replay identity is self-asserted by a database/config operator.
- Metrics are process-local and counters reset on restart.
- Admin endpoints are unauthenticated and rely on a network boundary.
- Worker sending is sequential per process; horizontal workers are supported.
- No tenant dimension, cancel/delete/unassign/reopen, active-work reassignment, general edit/correction flow, production identity, provider webhooks, or retention/archive mechanism.
- No load/soak evidence establishes production capacity.
- `make build` leaves an intentionally ignored `bin/` directory; the clean gate means tracked and unignored cleanliness, not absence of all generated artifacts.

## 10. Resolved and remaining findings

I resolved the following findings during hardening:

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
- escaped JSON U+0000 in durable title, description, assignment-reason, and replay-reason fields could reach PostgreSQL and become a misclassified 500; one shared narrow pre-persistence check, field-specific domain errors, exact OpenAPI patterns, and live no-side-effect/follow-up regressions close the gap without broadening other control-character policy.

I identified no additional in-scope findings after the independent follow-up and focused correction. I recorded candidate evidence in [`verification.md`](verification.md), and the external final fix reports capture the required post-commit authority. I deliberately accepted the debt above and do not make a production-readiness claim.

## 11. Verification evidence

I recorded only actually executed commands and exact observed results in [`docs/assessment/verification.md`](verification.md). I intentionally kept raw transient logs outside the repository and referenced them from the final fix report.
