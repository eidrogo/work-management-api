# ADR 0001: Modular Monolith with Separate API and Worker Processes

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I needed one transactional source of truth, an HTTP API, asynchronous email delivery, support for a small current team, and a credible path to higher volume and more engineers—not independent business domains or separate team ownership for this assessment.

## Decision

I chose one Go module and one PostgreSQL schema, built separate `cmd/api` and `cmd/worker` processes that share domain, persistence, configuration, logging, and notification code, and made package boundaries follow business capabilities rather than technical layers alone.

## Alternatives considered

I rejected these alternatives:

1. **One API process with an in-process goroutine:** this deployment is simpler, but notification availability and scaling would be coupled to API replicas, and process restarts could complicate graceful delivery.
2. **Independent work-item and notification microservices:** this split allows independent ownership and scaling but introduces network contracts, distributed tracing, deployment coordination, and a distributed consistency problem for assignment/outbox creation.
3. **Serverless functions:** this model reduces server management but complicates local parity, long-lived worker polling, and transaction ownership.

## Consequences

I accepted these consequences:

- assignment update, history, and outbox insertion remain one local database transaction;
- the API and worker can scale and deploy independently within one release;
- sharing code to reduce semantic drift;
- a bad shared release may affect both processes; and
- PostgreSQL remains a shared scaling and failure boundary.

## Operational impact

I will operate two long-running processes, three listeners (API public, API admin, and worker admin), two unauthenticated administrative surfaces, and one database; require a private network boundary for the administrative listeners; and preserve schema compatibility with both current and previous API/worker versions during rollouts.

## Revisit when

I will revisit this decision when:

- separate teams own the capabilities and need independent release contracts;
- worker load materially harms API database performance despite tuning;
- regulatory or blast-radius requirements demand isolation; or
- a broker or provider-specific service becomes a distinct platform capability.

## Related decisions

- [ADR 0002: PostgreSQL transactional outbox](0002-transactional-outbox.md)
- [ADR 0007: Private operational endpoints and bounded metrics](0007-private-operational-endpoints.md)
