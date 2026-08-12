# Submission hardening plan

Status: Accepted

Date: 2026-08-12

Baseline: `ed59c2c7394672531691cf49c964e62bcf265f21`

## Objective

Preserve the recovered assessment commit, repair every validated correctness,
security, reliability, and verification gap, then publish a private GitHub
repository only after clean local, Docker Compose, SMTP-transport, and security
gates pass.

The official assessment is the source of truth. The longer Staff Engineer
brief is the selected quality bar. The original commit is retained as evidence;
hardening is additive and must not rewrite it.

## Accepted implementation decisions

1. Keep the application boundary on `database/sql`, replace the custom cgo and
   libpq driver with `github.com/jackc/pgx/v5/stdlib` v5.10.0, and build with
   Go 1.26.5. Use `QueryExecModeExec` and decode `timestamptz` in UTC.
2. Claim one notification immediately before each SMTP send. PostgreSQL time
   and a unique lease token determine ownership; `WORKER_BATCH_SIZE` is only a
   per-poll work limit.
3. Persist safe SMTP failure codes and generic messages, never raw provider
   responses. Record every replay in an append-only actor-attributed audit row.
4. Return the immutable response snapshot for a matching create idempotency
   replay instead of rereading a later mutable representation.
5. Separate public business traffic from loopback/private operational
   listeners. Keep metric label domains finite, cap retained series, and cache
   database-backed backlog gauges.
6. Preserve Mailpit as deterministic proof of real SMTP protocol acceptance.
   Do not claim external inbox delivery and do not use real provider secrets.

## Work sequence

1. Modernize Go, pgx, Docker, Compose, CI actions, lint configuration, and
   dependency update policy.
2. Add tests first for database behavior, notification claims and fencing,
   replay attribution, error redaction, idempotency snapshots, and indexes;
   implement the minimum changes needed to pass them.
3. Add tests first for operational listener separation, finite metric labels,
   series limits, valid Prometheus exposition, and cached backlog collection;
   implement the minimum changes needed to pass them.
4. Expand the manager/worker authorization, lifecycle, concurrency, pagination,
   HTTP contract, SMTP TLS, and failure-path test matrices.
5. Make `demo.sh`, `verify-full`, and CI authoritative: no silent integration
   skips, no exhausted polling loops that report success, and guaranteed clean
   teardown.
6. Reconcile README, OpenAPI, ADRs, operations guidance, AI disclosure,
   verification evidence, and the technical-debrief guide with observed facts.
7. Run the complete clean-state verification and a fresh standard security
   scan. Fix every reportable finding before packaging or publication.

## Release gates

- Formatting, module verification and tidy drift, vet, golangci-lint, unit,
  PostgreSQL integration, race, command build, OpenAPI, secret, and clean-diff
  checks all pass.
- A fresh Docker image builds without cgo or libpq.
- Clean Compose migration, seed, API, worker, demo, Mailpit acceptance, durable
  `SENT`, authorization rejection, and cleanup all pass.
- Final security coverage is complete and contains no open reportable finding.
- Fresh ZIP, tar.gz, Git bundle, and SHA-256 manifest agree on the final tree and
  the bundle clones successfully.
- GitHub CLI is explicitly switched to `eidrogo`; the private repository is
  created and `main` is pushed only after the preceding gates pass.

## Out of scope

- External SMTP delivery or committed provider credentials.
- Production deployment, public visibility, release creation, or a license.
- Rewriting or squashing the recovered baseline commit.
