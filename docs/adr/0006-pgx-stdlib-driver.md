# ADR 0006: Maintained pgx `database/sql` Driver

- **Status:** Accepted
- **Date:** 2026-08-12
- **Supersedes:** [ADR 0005](0005-libpq-assessment-adapter.md)

## Context

The assessment was initially built with a narrow cgo adapter over system `libpq` because module downloads were unavailable. That adapter imposed a C toolchain and operating-system client-library dependency, and maintaining a PostgreSQL protocol driver is outside this service's scope. Application code already depends only on `database/sql`.

## Decision

Use `github.com/jackc/pgx/v5/stdlib` v5.10.0 behind the existing `database/sql` boundary. Parse connection URLs with pgx, use `QueryExecModeExec` to avoid implicit persistent statement caching, preserve connection and pool settings, and register a `TimestamptzCodec` with `ScanLocation: time.UTC` on every physical connection.

Pin Go 1.26.5 as the repository toolchain and build ordinary binaries and tests with cgo disabled. Race tests may still use the Go race detector's cgo requirement.

## Alternatives considered

- Retain the custom `libpq` adapter: preserves the original build but leaves a bespoke protocol boundary and cgo supply-chain burden.
- Use pgx native pools throughout: well maintained, but needlessly changes command, service, store, and transaction interfaces.
- Use pgx's default cached-statement mode: efficient for some workloads, but changes the original driver's execution behavior and can be less proxy-friendly.

## Consequences

The project gains a maintained pure-Go PostgreSQL implementation and removes runtime `libpq`, headers, and `pkg-config`. It adds a pinned third-party Go dependency. pgx concrete errors and cancellation details can differ from the removed adapter; callers must continue to depend on standard errors and SQLSTATE behavior rather than driver-specific types.

## Operational impact

Module integrity and tidy-drift checks are required in local verification and CI. PostgreSQL integration tests cover session parameters, core type decoding, transaction options, cancellation, and post-cancellation pool health. The UTC codec hook is required in addition to the server `TimeZone=UTC` setting.

## Revisit when

Measured workloads justify a different query execution mode, PgBouncer topology changes connection requirements, or application boundaries need pgx-native features that cannot be expressed through `database/sql`.
