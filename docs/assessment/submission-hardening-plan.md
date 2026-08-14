# Submission hardening plan

Status: Accepted

Date: 2026-08-12

Baseline: `ed59c2c7394672531691cf49c964e62bcf265f21`

## Objective

I set the objective to preserve the recovered assessment commit, repair every
validated correctness, security, reliability, and verification gap, then
publish a private GitHub repository only after clean local, Docker Compose,
SMTP-transport, and security gates passed.

I treated the official assessment as the source of truth, selected the longer
Staff Engineer brief as the quality bar, and retained the original commit as
evidence. The hardening was additive and did not rewrite it.

## Accepted implementation decisions

I accepted these implementation decisions:

1. Kept the application boundary on `database/sql`, replaced the custom cgo
   and libpq driver with `github.com/jackc/pgx/v5/stdlib` v5.10.0, and built with
   Go 1.26.5; used `QueryExecModeExec` and decoded `timestamptz` in UTC.
2. Made the worker claim one notification immediately before each SMTP send.
   PostgreSQL time and a unique lease token determine ownership;
   `WORKER_BATCH_SIZE` is only a per-poll work limit.
3. Persisted safe SMTP failure codes and generic messages, never raw provider
   responses; recorded every replay in an append-only actor-attributed audit
   row.
4. Returned the immutable response snapshot for a matching create idempotency
   replay instead of rereading a later mutable representation.
5. Separated public business traffic from loopback/private operational
   listeners; kept metric label domains finite, capped retained series, and cached
   database-backed backlog gauges.
6. Preserved the exercised worker/Mailpit flow as deterministic proof of real
   SMTP protocol acceptance without claiming external inbox delivery or using
   real provider secrets.

## Work sequence

I followed this work sequence:

1. Modernized Go, pgx, Docker, Compose, CI actions, lint configuration, and
   dependency update policy.
   Made the final runtime package-manager-free `scratch`; repository gates
   require the exact pinned-toolchain build graph and inspect the actual image
   contents.
2. Added tests first for database behavior, notification claims and fencing,
   replay attribution, error redaction, idempotency snapshots, and indexes, then
   implemented the minimum changes needed to pass them.
3. Added tests first for operational listener separation, finite metric
   labels, series limits, valid Prometheus exposition, and cached backlog
   collection, then implemented the minimum changes needed to pass them.
4. Expanded the manager/worker authorization, lifecycle, concurrency,
   pagination, HTTP contract, SMTP TLS, and failure-path test matrices.
5. Made `demo.sh`, `verify-full`, and CI authoritative: no silent integration
   skips, no exhausted polling loops that report success, and guaranteed clean
   teardown.
6. Reconciled README, OpenAPI, ADRs, operations guidance, AI disclosure,
   verification evidence, and the technical-debrief guide with observed facts.
7. Ran the complete clean-state verification and a fresh standard security
   scan; fixed every reportable finding before packaging or publication.

## Release gates

I selected these release gates:

- Required formatting, module verification and tidy drift, vet,
  golangci-lint, unit,
  PostgreSQL integration, race, command build, OpenAPI, secret, and clean-diff
  checks to pass.
- Required a fresh Docker image to build without cgo or libpq.
- Required clean Compose migration, seed, API, worker, demo, Mailpit
  acceptance, durable `SENT`, authorization rejection, and cleanup to pass.
- Required final security coverage to be complete with no open reportable
  finding.
- Required the fresh ZIP, tar.gz, Git bundle, and SHA-256 manifest to agree on
  the final tree and the bundle to clone successfully.
- Required GitHub CLI to be explicitly switched to `eidrogo`; private repository
  creation and the `main` push would follow only after the preceding gates passed.

## Out of scope

I kept the following out of scope:

- External SMTP delivery and committed provider credentials.
- Production deployment, public visibility, release creation, and a
  license.
- Rewriting or squashing the recovered baseline commit.
