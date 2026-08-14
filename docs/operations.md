# Failure Modes, Operations, and Production Roadmap

## Implemented operational behavior

- JSON logs and request IDs.
- Separate API-public, API-admin, and worker-admin listeners.
- Database-backed readiness with a two-second probe deadline.
- Bounded HTTP and SMTP operations.
- Prometheus-compatible request, authorization, mutation, delivery, backlog, and age metrics.
- One-row notification claims with PostgreSQL-time scheduling, UUID-fenced expiring leases, and abandoned-attempt recovery.
- Signal-driven worker drain: stop claims immediately, drain one active SMTP/fenced completion, then force-cancel only at `SHUTDOWN_TIMEOUT` and reap both worker/admin goroutines.
- Startup worker-ID validation matching the 255-character PostgreSQL columns, repeated at worker/store boundaries.
- Bounded retries, explicit terminal failure, safe persisted error fields, and reasoned replay with immutable attribution and a 10-cycle ceiling.
- Migration checksums and advisory locking.

## Listener and readiness contract

| Process surface | Direct-run default | Compose host default | Routes |
|---|---|---|---|
| API public | `HTTP_ADDR=:8080` | `127.0.0.1:8081` | authenticated `/v1/*` only |
| API admin | `API_ADMIN_ADDR=127.0.0.1:9090` | `127.0.0.1:9090` | `/health/live`, `/health/ready`, `/metrics` |
| Worker admin | `WORKER_HTTP_ADDR=127.0.0.1:9091` | `127.0.0.1:9091` | `/health/live`, `/health/ready`, `/metrics` |

Liveness means the process is serving, and readiness means PostgreSQL is available. The public listener returns the normal JSON `RESOURCE_NOT_FOUND` envelope for `/health/live`, `/health/ready`, and `/metrics`; those routes are intentionally outside the public OpenAPI contract.

Administrative endpoints remain unauthenticated. Bind them to a private interface or put them behind authenticated operational infrastructure in production. Compose binds the in-container admin sockets to `:9090` and `:9091` so forwarding works, while all host publications are IPv4 loopback. Loopback defaults are not a production network policy.

Local checks:

```bash
curl --fail http://127.0.0.1:9090/health/live
curl --fail http://127.0.0.1:9090/health/ready
curl --fail http://127.0.0.1:9091/health/live
curl --fail http://127.0.0.1:9091/health/ready
curl --fail http://127.0.0.1:9090/metrics
curl --fail http://127.0.0.1:9091/metrics
```

## Mutation log correlation

Each authenticated semantic create, assignment, start, complete, or notification replay attempt emits exactly one handler-level JSON record with message `api mutation`, separate from the generic `http request` access record. Every mutation record includes `request_id`, the server-authenticated `actor_id`, an `operation` from `create`, `assignment`, `start`, `complete`, or `notification_replay`, and `outcome` (`success` or `failure`). Successful work-item mutations add the returned `work_item_id` and `idempotent`; a successful notification replay adds the route `notification_id` and returned `work_item_id`. Failures add only numeric `status`, stable `error_code`, and a syntactically valid route resource ID when one is available.

Do not put bearer tokens, idempotency keys, email/display names, titles, descriptions, assignment/replay reasons, bodies, notification payloads, or provider/SMTP/SQL details in these records. The append-only work-item history remains the durable domain audit trail but has no request-ID column; use the live mutation record when correlating a client request to its authenticated actor and resource.

## Metrics bounds and interpretation

HTTP metrics accept only methods `GET`, `POST`, `PUT`, and `OTHER`, and the documented business templates plus `/unknown`. Each process retains at most 1,024 `(method, route, status)` keys. A rejected new key increments label-free `work_management_http_series_dropped_total`; existing keys continue updating and rejected keys are not retained.

Notification backlog gauges use a five-second per-process cache. One expired scrape refreshes synchronously while concurrent scrapes receive the previous completed state. A failed refresh retains the last good backlog values, sets `work_management_metrics_scrape_error` to 1, and waits five seconds before retrying. When that error is 1, backlog gauges can be older than five seconds.

Alert candidates include database unreadiness, rising oldest-pending age, no successful attempts while backlog exists, migration failure, broad mutation failure, sustained scrape error, and any increase in dropped HTTP series. Process-local counters reset on restart.

## Notification diagnosis and recovery

1. List terminal deliveries:

   ```bash
   curl --fail --silent \
     "$API/v1/notifications?status=FAILED" \
     -H "Authorization: Bearer $MANAGER" | jq
   ```

2. Inspect `/v1/notifications/{id}`. Use the safe code/message, attempts, schedule, current cycle, and replay history. These fields come from one repeatable-read database snapshot. Lease owner/expiry can aid diagnosis, but the UUID lease token is never public.
3. Correct the cause. Provider-specific responses belong in access-controlled provider telemetry, not replay reasons or durable error rows.
4. Replay through one explicit channel.

Authenticated manager HTTP replay:

```bash
curl --fail --silent \
  -X POST "$API/v1/notifications/$NOTIFICATION_ID/replay" \
  -H "Authorization: Bearer $MANAGER" \
  -H 'Content-Type: application/json' \
  -H 'X-Request-ID: incident-2026-08-12-001' \
  --data '{"reason":"Recipient configuration was corrected"}' | jq
```

Database-access CLI replay:

```bash
go run ./cmd/replay-notification \
  "$NOTIFICATION_ID" \
  "operator@example.com" \
  "Recipient configuration was corrected"
```

The HTTP path stores the authenticated manager and request ID. The CLI requires an operator identity but does not authenticate it; the value is self-asserted by someone who already has database/config access. Replay state and its append-only audit event commit atomically. Reasons are trimmed and limited to 1,000 Unicode code points. Cycles are limited to `1..10`, so a cycle-10 failure is terminal and returns `NOTIFICATION_REPLAY_LIMIT_REACHED` rather than sending again.

`SENT` means the configured SMTP server accepted the message after `DATA`. It is not proof of inbox placement, final delivery, an open, or freedom from a later bounce. A worker crash after SMTP acceptance but before the fenced database update can result in a duplicate on lease recovery; the stable `Message-ID` helps reconciliation but is not an SMTP idempotency key.

## Worker outage and lease recovery

A stopped worker does not roll back assignments: pending rows remain durable and backlog age increases. Restart or scale workers after restoring dependencies. Another worker can reclaim only after PostgreSQL time reaches the lease expiry; recovery appends `ABANDONED`, uses a fresh database UUID token, and starts a new attempt. Stale owners/tokens/attempts and expired leases cannot complete a row. `LEASE_DURATION` must exceed `SMTP_TIMEOUT`. `WORKER_BATCH_SIZE` limits sequential claim/send iterations per poll; it does not reserve a batch of leases.

On `SIGINT`/`SIGTERM`, the worker stops new claims and concurrently shuts down its admin server. An already active SMTP transaction and its fenced database completion retain a live context for up to `SHUTDOWN_TIMEOUT`. A clean drain exits successfully. At the deadline, SMTP I/O is interrupted, the admin server is force-closed, and both goroutines are reaped before the database closes; the row can remain `PROCESSING` until normal lease recovery. When deadline cancellation intersects an unhandled sender or completion failure, the process error includes the deadline and preserves the returned operation errors; `context.Canceled` is synthesized only when the current operation returned no error. Size the shutdown budget for the configured SMTP timeout and expected database completion latency. `WORKER_ID` must be valid UTF-8, 1–255 Unicode code points after trimming, and free of control characters; invalid configured values prevent startup before readiness.

`SMTP_FROM_NAME` and effective parsed sender/recipient display names must be valid UTF-8, at most 200 Unicode code points, and free of control characters. Invalid configured sender names prevent startup before dependencies/listeners; the adapter repeats this validation before any SMTP dial. Message bodies are quoted-printable encoded without discarding the assignment reason, and generated physical header/body lines are bounded to 998 octets. A sticky cumulative 1 MiB read budget wraps the raw SMTP transport before plain, implicit-TLS, or STARTTLS selection, bounding peer-controlled response and TLS-record input for the whole session. A limit hit is a permanent `SMTP_PROTOCOL_ERROR`; after the post-DATA `250` acceptance boundary the client closes directly rather than turning a `QUIT` failure into a duplicate-producing retry.

## Failure modes

| Failure | Effect | Detection | Recovery |
|---|---|---|---|
| SMTP outage | Assignment commits; delivery backlog grows | failure rate, pending count, oldest age | Restore provider; replay terminal failures after diagnosis |
| Worker stopped | Delivery pauses | worker liveness, no attempt rate, backlog age | Restart/scale; let expired leases recover |
| PostgreSQL unavailable | API and worker readiness fail | admin readiness and DB monitoring | Restore/fail over database before routing traffic |
| Hot work item | `CONCURRENT_MODIFICATION` | conflict logs/metrics | Refetch current version and retry intended command |
| Invalid recipient/payload | Delivery becomes `FAILED` | failed gauge and safe attempt code | Correct cause, then attributed replay |
| SMTP accepted before crash | Possible duplicate after recovery | abandoned attempt plus provider evidence | Reconcile stable Message-ID; provider idempotency/webhooks later |
| Backlog refresh fails | Last-good gauges become stale | `work_management_metrics_scrape_error == 1` | Restore DB access and confirm a later scrape clears it |
| HTTP metric cap reached | New label keys are dropped | dropped-series counter | Inspect unexpected methods/routes/statuses; review before raising cap |

## Migration, backup, restore, and rollback

Before a production migration, take a database-native backup, verify it can be restored into an isolated target, record the application and migration versions, and confirm the migration is backward compatible with the previously deployed API and worker. Example tooling shape:

```bash
pg_dump --format=custom --no-owner --no-acl "$DATABASE_URL" > work-management.dump
createdb "$RESTORE_DATABASE"
pg_restore --exit-on-error --no-owner --no-acl \
  --dbname "$RESTORE_DATABASE" work-management.dump
```

Credentials do not belong in shell history or checked-in files. The production restore procedure must be rehearsed with the actual managed-database mechanism, access controls, encryption, retention, and recovery objectives.

Apply forward migrations once before rolling out processes:

```bash
go run ./cmd/migrate up
```

The runner serializes migration application with an advisory lock and rejects checksum drift. Prefer a forward corrective migration and application rollback compatible with the migrated schema. The destructive command below is for disposable local development only:

```bash
go run ./cmd/migrate down 1
```

The initial migration was intentionally edited for this clean-schema assessment. An old schema or Compose volume with a different version-1 checksum must be recreated; never edit `schema_migrations` to bypass the check.

## Local stack cleanup and troubleshooting

Inspect before changing state:

```bash
docker compose ps --all
docker compose logs --no-color --tail 300 migrate seed api worker postgres mailpit
```

Stop while keeping the named PostgreSQL volume:

```bash
docker compose down --remove-orphans
```

Delete the local database and all project volumes only when data loss is intended:

```bash
docker compose down --volumes --remove-orphans
```

If a fixed local port is occupied, change only the relevant host variable in `.env` (`API_PORT`, `API_ADMIN_PORT`, `WORKER_PORT`, `POSTGRES_PORT`, `SMTP_PORT`, or `MAILPIT_UI_PORT`). Do not widen the host IP. For authoritative verification, use `make verify-full`; it creates a unique project with Docker-assigned loopback ports and proves its labeled containers, networks, and volumes are removed on success, injected failure, and termination.

Common checks:

- **Container healthcheck fails:** run `/app/healthcheck 127.0.0.1:<admin-port>` inside the container. It intentionally rejects DNS names, non-loopback addresses, redirects, proxies, non-200 readiness, oversized responses, and timeouts. The public listener is never probed.

- **API or worker never becomes ready:** inspect the migrate/seed exit code and PostgreSQL health before application logs.
- **Assignment commits but no mail appears:** inspect notification state/attempts, worker logs, Mailpit `/api/v1/info`, and the worker admin metrics; do not infer inbox delivery.
- **Repeated `CONCURRENT_MODIFICATION`:** confirm the client sends the latest ETag value in `If-Match` rather than blindly retrying.
- **Migration checksum mismatch:** use the expected clean schema/volume or ship a new migration; do not rewrite recorded checksums.
- **Readiness is healthy but public `/health/*` is 404:** that is the intended listener boundary; query the admin port.

## SLO candidates

- Target 99.9% monthly API availability, excluding valid 4xx.
- Target 95% of reads below 300 ms and mutations below 500 ms at agreed load.
- Target 99% of assignment messages accepted by the configured SMTP server within two minutes when dependencies are healthy.
- Target zero successful assignments without a matching durable delivery row.

## Before production

Before production: replace development authentication with externally verified OIDC/JWT and add secret management and rotation, TLS PostgreSQL/SMTP, managed HA PostgreSQL, tested restore/runbook ownership, private network policy or an authenticated proxy for admin listeners, rate limiting, distributed tracing, load/failure testing, on-call procedures, provider delivery/bounce webhooks, signed artifacts, SBOM/dependency scanning, tenant isolation, and explicit retention/correction policies.
