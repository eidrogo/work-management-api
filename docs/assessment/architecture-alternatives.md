# Phase 2 — Architecture Alternatives

| Decision | Alternatives considered | Selected approach and rationale |
|---|---|---|
| Application architecture | In-process goroutine, modular monolith, microservices | Modular monolith with API and worker processes preserves one transaction model while allowing independent scaling. |
| Persistence | Memory/SQLite, PostgreSQL, event store | PostgreSQL supplies constraints, transactions, row locking, durable queues, and indexed pagination. |
| Notification delivery | Synchronous SMTP, memory queue, transactional outbox, broker | Transactional outbox prevents lost intent without introducing a broker and dual-write problem. |
| Authentication | User headers, local JWT issuer, opaque tokens | Hashed opaque development tokens demonstrate a replaceable trust boundary without building identity. |
| Audit | Logs only, append-only history, full event sourcing | Current-state tables plus append-only mutation/attempt history provide traceability without reconstruction complexity. |
| Concurrency | Last-write-wins, row locks, optimistic versions | Optimistic versions make stale clients explicit and keep transactions short. |
| Worker claims | Single worker, mutex, PostgreSQL leases, broker | Claim exactly one `SKIP LOCKED` row immediately before send; PostgreSQL time and a fresh UUID fenced by owner, attempt, and unexpired lease support multiple workers without process-clock authority. |
| Pagination | Offset, keyset | Keyset pagination is stable and proportional to page size. |
| Operational exposure | One public listener, authenticated in-app ops routes, separate private admin listeners | Separate API-admin and worker-admin listeners keep unauthenticated health/metrics off the public API; loopback is the local default and production still needs private ingress or an authenticated proxy. |
