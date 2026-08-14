# ADR 0005: Narrow `database/sql` Adapter over System `libpq`

- **Status:** Superseded by [ADR 0006](0006-pgx-stdlib-driver.md)
- **Date:** 2026-08-12

## Context

The construction environment exposed Go and `libpq`, but module-proxy access was not reliable. I still needed real PostgreSQL transactions and `SKIP LOCKED` semantics; this historical decision is superseded by ADR 0006.

## Decision

I chose a narrow cgo `database/sql/driver` over the public `libpq` C API while keeping SQL and application code on standard `database/sql` boundaries.

## Alternatives considered

I rejected:

- pgx, although it was my normal preference, because it was unavailable during construction;
- SQLite/in-memory because it would not demonstrate PostgreSQL semantics; and
- vendoring a partial third-party driver because of the larger review and supply-chain risk.

## Consequences

I accepted that builds require cgo, `pkg-config`, and `libpq` headers/runtime, and that the adapter is smaller than a mature driver and increases maintenance burden.

## Operational impact

Operationally, I would patch the base image when PostgreSQL client libraries receive security updates and make CI compile with cgo and run PostgreSQL integration tests.

## Revisit when

This historical decision was revisited by replacing the adapter with a maintained pinned driver and rerunning transaction, cancellation, pool, and concurrency tests; see ADR 0006.
