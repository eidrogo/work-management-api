# ADR 0005: Narrow `database/sql` Adapter over System `libpq`

- **Status:** Accepted for the assessment; replacement required before production
- **Date:** 2026-08-12

## Context

The construction environment exposed Go and `libpq`, but module-proxy access was not reliable. The implementation still needed real PostgreSQL transactions and `SKIP LOCKED` semantics.

## Decision

Use a narrow cgo `database/sql/driver` over the public `libpq` C API. Keep SQL and application code on standard `database/sql` boundaries.

## Alternatives considered

- pgx: preferred normally, but unavailable during construction.
- SQLite/in-memory: would not demonstrate PostgreSQL semantics.
- Vendoring a partial third-party driver: larger review and supply-chain risk.

## Consequences

Builds require cgo, `pkg-config`, and `libpq` headers/runtime. The adapter is smaller than a mature driver and increases maintenance burden.

## Operational impact

Patch the base image when PostgreSQL client libraries receive security updates. CI must compile with cgo and run PostgreSQL integration tests.

## Revisit when

Before production, replace the adapter with a maintained pinned driver and rerun transaction, cancellation, pool, and concurrency tests.
