# ADR 0004: Forward Lifecycle, Optimistic Versions, and Keyset Pagination

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I needed work to move forward, concurrent commands not to overwrite each other, and browsing to remain practical as volume grows.

## Decision

I chose `UNASSIGNED → ASSIGNED → IN_PROGRESS → COMPLETED`. The lifecycle allows reassignment only while `ASSIGNED` and requires a reason. The API exposes version-conditional updates as ETags, and keyset cursors cover `(created_at, id)` and event sequence.

## Alternatives considered

I rejected:

- last-write-wins because it silently loses intent;
- broad pessimistic locks because they increase contention and hide client staleness;
- offset pagination because it degrades and shifts under concurrent inserts; and
- configurable workflows because they add unjustified complexity.

## Consequences

I accepted explicit conflicts for stale commands and forward-only cursor traversal. Administrative recovery commands remain future design work.

## Operational impact

I will monitor conflict rate and list-query latency. Indexes support manager, worker queue, history, and notification access paths.

## Revisit when

I will revisit this when measured contention or approved cancellation/reopen requirements change the lifecycle economics.
