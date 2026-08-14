# ADR 0002: PostgreSQL Transactional Outbox for Assignment Email

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I required successful assignments to have durable notification intent, while SMTP cannot participate in the database transaction.

## Decision

I chose to update the work item, append the assignment event, and insert a unique `PENDING` delivery in one PostgreSQL transaction. I use PostgreSQL `clock_timestamp()` to supply the queue schedule.

I use a separate worker that loops up to its configured per-poll send limit. Immediately before each send it claims exactly one due row with `FOR UPDATE SKIP LOCKED`. I give each claim a fresh database UUID lease token and accept completion only for the same delivery, owner, token, attempt number, and an unexpired lease. I use PostgreSQL time to control due checks, lease boundaries, abandonment, completion, retry scheduling, and replay.

I classify SMTP failures in process, but persist only an allowlisted code and its fixed generic message. I never treat provider responses, authentication detail, certificate text, or credentials as delivery or attempt data.

I allow a manager to replay a terminal failure only with a reason. I make HTTP record the authenticated manager and request ID, and the database-access CLI record a required self-asserted operator identity and generated request ID. I make replay state and its append-only audit event commit atomically. I limit delivery cycles to 10.

## Alternatives considered

- I rejected synchronous SMTP inside the request because of remote latency and rollback ambiguity.
- I rejected sending after commit without an outbox because process crashes lose notifications.
- I rejected an external broker because it still requires solving the database/broker dual write and adds unnecessary infrastructure.

## Consequences

I accepted atomic assignment and durable intent. I use single-row claims and token fencing to remove ordinary batch-expiry, stale-owner, and process-clock-skew completion paths. I accepted at-least-once delivery: a crash after provider acceptance but before the fenced `SENT` update can cause a duplicate, so I retain the stable `Message-ID` across recovery.

I intentionally made manual replay an operator action rather than an automatic state transition. I accepted its immutable attribution to improve accountability and its cycle ceiling to bound repeated sends. I describe CLI identity as self-asserted by an operator who already has database/config access, not authenticated.

## Operational impact

I will monitor pending count, oldest pending age, terminal failures, safe failure codes, attempt outcomes, expired leases, and replay-cycle exhaustion. I allow failed deliveries to be replayed without deleting attempt or replay history.

## Revisit when

I will revisit this when queue volume materially burdens PostgreSQL or a provider offers idempotent sends and signed delivery webhooks.

## Related decisions

- [ADR 0001: Modular monolith with separate API and worker processes](0001-modular-monolith.md)
- [ADR 0007: Private operational endpoints and bounded metrics](0007-private-operational-endpoints.md)
