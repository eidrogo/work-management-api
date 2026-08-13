# ADR 0002: PostgreSQL Transactional Outbox for Assignment Email

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

A successful assignment must not exist without durable notification intent, while SMTP cannot participate in the database transaction.

## Decision

Update the work item, append the assignment event, and insert a unique `PENDING` delivery in one PostgreSQL transaction. PostgreSQL `clock_timestamp()` supplies the queue schedule.

A separate worker loops up to its configured per-poll send limit. Immediately before each send it claims exactly one due row with `FOR UPDATE SKIP LOCKED`. Each claim receives a fresh database UUID lease token; completion is accepted only for the same delivery, owner, token, attempt number, and an unexpired lease. PostgreSQL time controls due checks, lease boundaries, abandonment, completion, retry scheduling, and replay.

SMTP failures are classified in process, but only an allowlisted code and its fixed generic message are persisted. Provider responses, authentication detail, certificate text, and credentials are never delivery or attempt data.

A manager can replay a terminal failure only with a reason. HTTP records the authenticated manager and request ID; the database-access CLI records a required self-asserted operator identity and generated request ID. Replay state and its append-only audit event commit atomically. Delivery cycles are limited to 10.

## Alternatives considered

- Synchronous SMTP inside the request: remote latency and rollback ambiguity.
- Send after commit without an outbox: process crashes lose notifications.
- External broker: still requires solving the database/broker dual write and adds unnecessary infrastructure.

## Consequences

The assignment and durable intent are atomic. Single-row claims and token fencing remove ordinary batch-expiry, stale-owner, and process-clock-skew completion paths. Delivery remains at least once: a crash after provider acceptance but before the fenced `SENT` update can cause a duplicate, so the stable `Message-ID` is retained across recovery.

Manual replay is intentionally an operator action rather than an automatic state transition. Its immutable attribution improves accountability, while the cycle ceiling bounds repeated sends. CLI identity is self-asserted by an operator who already has database/config access; it is not described as authenticated.

## Operational impact

Monitor pending count, oldest pending age, terminal failures, safe failure codes, attempt outcomes, expired leases, and replay-cycle exhaustion. Failed deliveries can be replayed without deleting attempt or replay history.

## Revisit when

Queue volume materially burdens PostgreSQL or a provider offers idempotent sends and signed delivery webhooks.
