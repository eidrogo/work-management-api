# ADR 0006: Maintained pgx `database/sql` Driver

- **Status:** Accepted
- **Date:** 2026-08-12
- **Supersedes:** [ADR 0005](0005-libpq-assessment-adapter.md)

## Context

I initially built the assessment with a narrow cgo adapter over system `libpq` because module downloads were unavailable. That adapter imposed a C toolchain and operating-system client-library dependency, and I determined that maintaining a PostgreSQL protocol driver is outside this service's scope. Application code already depends only on `database/sql`.

## Decision

I chose `github.com/jackc/pgx/v5/stdlib` v5.10.0 behind the existing `database/sql` boundary. I parse connection URLs with pgx, use `QueryExecModeExec` to avoid implicit persistent statement caching, preserve connection and pool settings, and register a `TimestamptzCodec` with `ScanLocation: time.UTC` on every physical connection.

I pinned Go 1.26.5 as the repository toolchain and build ordinary binaries and tests with cgo disabled. Race tests may still use the Go race detector's cgo requirement.

## Alternatives considered

- I rejected retaining the custom `libpq` adapter because it preserves the original build but leaves a bespoke protocol boundary and cgo supply-chain burden.
- I rejected using pgx native pools throughout because, although well maintained, they needlessly change command, service, store, and transaction interfaces.
- I rejected pgx's default cached-statement mode because it is efficient for some workloads but changes the original driver's execution behavior and can be less proxy-friendly.

## Consequences

I accepted a maintained pure-Go PostgreSQL implementation and removed runtime `libpq`, headers, and `pkg-config`. I accepted a pinned third-party Go dependency. pgx concrete errors and cancellation details can differ from the removed adapter; I require callers to continue to depend on standard errors and SQLSTATE behavior rather than driver-specific types.

## Operational impact

I require module integrity and tidy-drift checks in local verification and CI. I use PostgreSQL integration tests to cover session parameters, core type decoding, transaction options, cancellation, and post-cancellation pool health. I require the UTC codec hook in addition to the server `TimeZone=UTC` setting.

## Revisit when

I will revisit this when measured workloads justify a different query execution mode, PgBouncer topology changes connection requirements, or application boundaries need pgx-native features that cannot be expressed through `database/sql`.
