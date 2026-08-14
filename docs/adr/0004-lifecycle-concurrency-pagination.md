# ADR 0004: Forward Lifecycle, Optimistic Versions, and Keyset Pagination

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I needed work to move forward, concurrent commands not to overwrite each other, and browsing to remain practical as volume grows.

## Decision

I chose `UNASSIGNED → ASSIGNED → IN_PROGRESS → COMPLETED`. I allow reassignment only while `ASSIGNED`, requiring a reason. I use version-conditional updates exposed as ETags and keyset cursors over `(created_at, id)` and event sequence.

## Alternatives considered

- I rejected last-write-wins because it silently loses intent.
- I rejected broad pessimistic locks because they increase contention and hide client staleness.
- I rejected offset pagination because it degrades and shifts under concurrent inserts.
- I rejected configurable workflows because they add unjustified complexity.

## Consequences

I accepted explicit conflicts for stale commands and forward-only cursor traversal. I will design administrative recovery commands explicitly later.

## Operational impact

I will monitor conflict rate and list-query latency. I use indexes to support manager, worker queue, history, and notification access paths.

## Revisit when

I will revisit this when measured contention or approved cancellation/reopen requirements change the lifecycle economics.
