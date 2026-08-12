# Phase 2 — Architecture Alternatives

| Decision | Alternatives considered | Selected approach and rationale |
|---|---|---|
| Application architecture | In-process goroutine, modular monolith, microservices | Modular monolith with API and worker processes preserves one transaction model while allowing independent scaling. |
| Persistence | Memory/SQLite, PostgreSQL, event store | PostgreSQL supplies constraints, transactions, row locking, durable queues, and indexed pagination. |
| Notification delivery | Synchronous SMTP, memory queue, transactional outbox, broker | Transactional outbox prevents lost intent without introducing a broker and dual-write problem. |
| Authentication | User headers, local JWT issuer, opaque tokens | Hashed opaque development tokens demonstrate a replaceable trust boundary without building identity. |
| Audit | Logs only, append-only history, full event sourcing | Current-state tables plus append-only mutation/attempt history provide traceability without reconstruction complexity. |
| Concurrency | Last-write-wins, row locks, optimistic versions | Optimistic versions make stale clients explicit and keep transactions short. |
| Worker claims | Single worker, mutex, PostgreSQL leases, broker | `SKIP LOCKED` and expiring leases support multiple workers with current infrastructure. |
| Pagination | Offset, keyset | Keyset pagination is stable and proportional to page size. |
