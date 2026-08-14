# Submission hardening plan

Status: Accepted

Date: 2026-08-12

Baseline: `ed59c2c7394672531691cf49c964e62bcf265f21`

## Objective

My objective was to preserve the recovered assessment commit, repair every
validated correctness, security, reliability, and verification gap, then
publish a private GitHub repository only after clean local, Docker Compose,
SMTP-transport, and security gates passed.

I treated the official assessment as the source of truth and selected the
longer Staff Engineer brief as the quality bar. I retained the original commit
as evidence; my hardening was additive and did not rewrite it.

## Accepted implementation decisions

1. I kept the application boundary on `database/sql`, replaced the custom cgo
   and libpq driver with `github.com/jackc/pgx/v5/stdlib` v5.10.0, and built with
   Go 1.26.5. I used `QueryExecModeExec` and decoded `timestamptz` in UTC.
2. I made the worker claim one notification immediately before each SMTP send.
   PostgreSQL time and a unique lease token determine ownership;
   `WORKER_BATCH_SIZE` is only a per-poll work limit.
3. I persisted safe SMTP failure codes and generic messages, never raw provider
   responses. I recorded every replay in an append-only actor-attributed audit
   row.
4. I returned the immutable response snapshot for a matching create idempotency
   replay instead of rereading a later mutable representation.
5. I separated public business traffic from loopback/private operational
   listeners. I kept metric label domains finite, capped retained series, and
   cached database-backed backlog gauges.
6. I preserved Mailpit as deterministic proof of real SMTP protocol acceptance.
   I did not claim external inbox delivery or use real provider secrets.

## Work sequence

I followed this work sequence:

1. I modernized Go, pgx, Docker, Compose, CI actions, lint configuration, and
   dependency update policy.
   I made the final runtime package-manager-free `scratch`; repository gates
   require the exact pinned-toolchain build graph and inspect the actual image
   contents.
2. I added tests first for database behavior, notification claims and fencing,
   replay attribution, error redaction, idempotency snapshots, and indexes, then
   implemented the minimum changes needed to pass them.
3. I added tests first for operational listener separation, finite metric
   labels, series limits, valid Prometheus exposition, and cached backlog
   collection, then implemented the minimum changes needed to pass them.
4. I expanded the manager/worker authorization, lifecycle, concurrency,
   pagination, HTTP contract, SMTP TLS, and failure-path test matrices.
5. I made `demo.sh`, `verify-full`, and CI authoritative: no silent integration
   skips, no exhausted polling loops that report success, and guaranteed clean
   teardown.
6. I reconciled README, OpenAPI, ADRs, operations guidance, AI disclosure,
   verification evidence, and the technical-debrief guide with observed facts.
7. I ran the complete clean-state verification and a fresh standard security
   scan. I fixed every reportable finding before packaging or publication.

## Release gates

I selected these release gates:

- I required formatting, module verification and tidy drift, vet,
  golangci-lint, unit,
  PostgreSQL integration, race, command build, OpenAPI, secret, and clean-diff
  checks to pass.
- I required a fresh Docker image to build without cgo or libpq.
- I required clean Compose migration, seed, API, worker, demo, Mailpit
  acceptance, durable `SENT`, authorization rejection, and cleanup to pass.
- I required final security coverage to be complete with no open reportable
  finding.
- I required the fresh ZIP, tar.gz, Git bundle, and SHA-256 manifest to agree on
  the final tree and the bundle to clone successfully.
- I required GitHub CLI to be explicitly switched to `eidrogo`; I would create
  the private repository and push `main` only after the preceding gates passed.

## Out of scope

I kept the following out of scope:

- I excluded external SMTP delivery and committed provider credentials.
- I excluded production deployment, public visibility, release creation, and a
  license.
- I excluded rewriting or squashing the recovered baseline commit.
