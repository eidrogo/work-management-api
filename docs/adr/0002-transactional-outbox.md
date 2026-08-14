# ADR 0002: PostgreSQL Transactional Outbox for Assignment Email

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I required successful assignments to have durable notification intent, while SMTP cannot participate in the database transaction.

## Decision

I chose to update the work item, append the assignment event, and insert a unique `PENDING` delivery in one PostgreSQL transaction. PostgreSQL `clock_timestamp()` supplies the queue schedule.

A separate worker loops up to its configured per-poll send limit. Immediately before each send it claims exactly one due row with `FOR UPDATE SKIP LOCKED`. Each claim receives a fresh database UUID lease token, and completion succeeds only for the same delivery, owner, token, attempt number, and an unexpired lease. PostgreSQL time controls due checks, lease boundaries, abandonment, completion, retry scheduling, and replay.

The process classifies SMTP failures but persists only an allowlisted code and its fixed generic message. Provider responses, authentication detail, certificate text, and credentials never become delivery or attempt data.

A manager can replay a terminal failure only with a reason. HTTP records the authenticated manager and request ID; the database-access CLI records a required self-asserted operator identity and generated request ID. Replay state and its append-only audit event commit atomically, and delivery cycles stop at 10.

## Alternatives considered

I rejected:

- synchronous SMTP inside the request because of remote latency and rollback ambiguity;
- sending after commit without an outbox because process crashes lose notifications; and
- an external broker because it still requires solving the database/broker dual write and adds unnecessary infrastructure.

## Consequences

I accepted atomic assignment, durable intent, and at-least-once delivery. Single-row claims and token fencing remove ordinary batch-expiry, stale-owner, and process-clock-skew completion paths. A crash after provider acceptance but before the fenced `SENT` update can cause a duplicate, so the stable `Message-ID` remains unchanged across recovery.

I intentionally made manual replay an operator action rather than an automatic state transition, with immutable attribution for accountability and a cycle ceiling to bound repeated sends. CLI identity is self-asserted by an operator who already has database/config access, not authenticated.

## Operational impact

I will monitor pending count, oldest pending age, terminal failures, safe failure codes, attempt outcomes, expired leases, and replay-cycle exhaustion. Failed deliveries remain replayable without deleting attempt or replay history.

## Revisit when

I will revisit this when queue volume materially burdens PostgreSQL or a provider offers idempotent sends and signed delivery webhooks.

## Related decisions

- [ADR 0001: Modular monolith with separate API and worker processes](0001-modular-monolith.md)
- [ADR 0007: Private operational endpoints and bounded metrics](0007-private-operational-endpoints.md)
