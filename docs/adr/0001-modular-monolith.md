# ADR 0001: Modular Monolith with Separate API and Worker Processes

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I needed one transactional source of truth, an HTTP API, asynchronous email delivery, support for a small current team, and a credible path to higher volume and more engineers. I did not need independent business domains or separate team ownership for this assessment.

## Decision

I chose one Go module and one PostgreSQL schema. I built separate `cmd/api` and `cmd/worker` processes that share domain, persistence, configuration, logging, and notification code. I made package boundaries follow business capabilities rather than technical layers alone.

## Alternatives considered

1. **One API process with an in-process goroutine.** I rejected this simpler deployment because notification availability and scaling would be coupled to API replicas, and process restarts could complicate graceful delivery.
2. **Independent work-item and notification microservices.** I rejected this because, although it allows independent ownership and scaling, it introduces network contracts, distributed tracing, deployment coordination, and a distributed consistency problem for assignment/outbox creation.
3. **Serverless functions.** I rejected this because it reduces server management but complicates local parity, long-lived worker polling, and transaction ownership.

## Consequences

- I accepted that assignment update, history, and outbox insertion remain one local database transaction.
- I accepted that the API and worker can scale and deploy independently within one release.
- I accepted shared code to reduce semantic drift.
- I accepted that a bad shared release may affect both processes.
- I accepted PostgreSQL as a shared scaling and failure boundary.

## Operational impact

I will operate two long-running processes, three listeners (API public, API admin, and worker admin), two unauthenticated administrative surfaces, and one database. I require a private network boundary for the administrative listeners. I will preserve schema compatibility with both current and previous API/worker versions during rollouts.

## Revisit when

- I will revisit this when separate teams own the capabilities and need independent release contracts.
- I will revisit this when worker load materially harms API database performance despite tuning.
- I will revisit this when regulatory or blast-radius requirements demand isolation.
- I will revisit this when a broker or provider-specific service becomes a distinct platform capability.

## Related decisions

- [ADR 0002: PostgreSQL transactional outbox](0002-transactional-outbox.md)
- [ADR 0007: Private operational endpoints and bounded metrics](0007-private-operational-endpoints.md)
