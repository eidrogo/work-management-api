# ADR 0001: Modular Monolith with Separate API and Worker Processes

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

The system needs one transactional source of truth, an HTTP API, asynchronous email delivery, a small current team, and a credible path to higher volume and more engineers. The assessment does not require independent business domains or separate team ownership.

## Decision

Use one Go module and one PostgreSQL schema. Build separate `cmd/api` and `cmd/worker` processes that share domain, persistence, configuration, logging, and notification code. Package boundaries follow business capabilities rather than technical layers alone.

## Alternatives considered

1. **One API process with an in-process goroutine.** Simpler deployment, but notification availability and scaling would be coupled to API replicas. Process restarts could complicate graceful delivery.
2. **Independent work-item and notification microservices.** Allows independent ownership and scaling, but introduces network contracts, distributed tracing, deployment coordination, and a distributed consistency problem for assignment/outbox creation.
3. **Serverless functions.** Reduces server management but complicates local parity, long-lived worker polling, and transaction ownership.

## Consequences

- Assignment update, history, and outbox insertion remain one local database transaction.
- API and worker can scale and deploy independently within one release.
- Shared code reduces semantic drift.
- A bad shared release may affect both processes.
- PostgreSQL remains a shared scaling and failure boundary.

## Operational impact

Operate two long-running processes, two health/metrics endpoints, and one database. Rollouts should preserve schema compatibility with both current and previous process versions.

## Revisit when

- Separate teams own the capabilities and need independent release contracts.
- Worker load materially harms API database performance despite tuning.
- Regulatory or blast-radius requirements demand isolation.
- A broker or provider-specific service becomes a distinct platform capability.
