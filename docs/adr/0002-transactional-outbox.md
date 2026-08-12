# ADR 0002: PostgreSQL Transactional Outbox for Assignment Email

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

A successful assignment must not exist without durable notification intent, while SMTP cannot participate in the database transaction.

## Decision

Update the work item, append the assignment event, and insert a unique `PENDING` delivery in one PostgreSQL transaction. A separate worker claims due rows using `FOR UPDATE SKIP LOCKED`, sends through SMTP, records each attempt, and updates delivery state.

## Alternatives considered

- Synchronous SMTP inside the request: remote latency and rollback ambiguity.
- Send after commit without an outbox: process crashes lose notifications.
- External broker: still requires solving the database/broker dual write and adds unnecessary infrastructure.

## Consequences

The assignment and durable intent are atomic. Delivery is at least once; a crash after provider acceptance but before the `SENT` update can cause a duplicate.

## Operational impact

Monitor pending count, oldest pending age, terminal failures, attempt outcomes, and expired leases. Failed deliveries can be replayed without deleting history.

## Revisit when

Queue volume materially burdens PostgreSQL or a provider offers idempotent sends and signed delivery webhooks.
