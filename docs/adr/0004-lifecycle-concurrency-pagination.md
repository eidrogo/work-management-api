# ADR 0004: Forward Lifecycle, Optimistic Versions, and Keyset Pagination

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Work should move forward, concurrent commands must not overwrite each other, and browsing must remain practical as volume grows.

## Decision

Use `UNASSIGNED → ASSIGNED → IN_PROGRESS → COMPLETED`. Allow reassignment only while `ASSIGNED`, requiring a reason. Use version-conditional updates exposed as ETags. Use keyset cursors over `(created_at, id)` and event sequence.

## Alternatives considered

- Last-write-wins: silently loses intent.
- Broad pessimistic locks: increases contention and hides client staleness.
- Offset pagination: degrades and shifts under concurrent inserts.
- Configurable workflows: unjustified complexity.

## Consequences

Stale commands produce explicit conflicts. Cursor traversal is forward-only. Administrative recovery commands must be designed explicitly later.

## Operational impact

Monitor conflict rate and list-query latency. Indexes support manager, worker queue, history, and notification access paths.

## Revisit when

Measured contention or approved cancellation/reopen requirements change the lifecycle economics.
