# Failure Modes, Operations, and Production Roadmap

## Implemented operational behavior

- JSON logs and request IDs.
- API and worker liveness/readiness endpoints.
- Database-backed readiness with timeouts.
- HTTP and SMTP timeouts.
- Prometheus-compatible request, authorization, mutation, delivery, backlog, and age metrics.
- One-row notification claims with UUID-fenced expiring leases and abandoned-attempt recovery.
- Bounded retries and explicit terminal failure.
- Allowlisted failure codes with fixed generic persisted messages.
- Reasoned manager API and CLI replay with immutable attribution and a 10-cycle ceiling.
- Migration checksums and advisory locking.

## Failure modes

| Failure | Effect | Detection | Recovery |
|---|---|---|---|
| SMTP outage | Assignments commit; email backlog grows | failure rate, pending count, oldest pending age | Restore provider; replay terminal failures |
| Worker stopped | Delivery pauses | worker health, no attempt rate, backlog age | Restart or scale worker; expired leases recover |
| PostgreSQL unavailable | API and worker readiness fail | readiness and DB monitoring | Restore/fail over database |
| Hot work item | Explicit `CONCURRENT_MODIFICATION` | conflict logs/metrics | Refetch and retry intended command |
| Poison recipient/payload | Delivery becomes `FAILED` | failed gauge and attempts | Correct data/configuration and replay |
| Provider accepted email before crash | Possible duplicate on retry | abandoned attempt plus provider evidence | Reconcile manually; use provider idempotency later |

## SLO candidates

- 99.9% monthly API availability, excluding valid 4xx.
- 95% of reads below 300 ms and mutations below 500 ms at agreed load.
- 99% of assignment emails accepted by the configured provider within two minutes when healthy.
- Zero successful assignments without a matching durable delivery row.

## Alert candidates

Page on database unavailability, growing notification age, no successful attempts while backlog exists, migration failure, or broad assignment failure. Handle isolated invalid recipients and moderate conflict increases during business hours.

## Notification recovery

1. List failed deliveries through `/v1/notifications?status=FAILED`.
2. Inspect `/v1/notifications/{id}` including attempts, safe failure code/message, and replay history.
3. Correct the cause.
4. Replay through one of the explicit attributed channels:

   Authenticated manager HTTP request:

   ```bash
   curl -X POST "$API/v1/notifications/$NOTIFICATION_ID/replay" \
     -H "Authorization: Bearer $MANAGER" \
     -H 'Content-Type: application/json' \
     -H 'X-Request-ID: incident-2026-08-12-001' \
     --data '{"reason":"Recipient configuration was corrected"}'
   ```

   Database-access CLI (the operator identity is required and self-asserted, not authenticated by the CLI):

   ```bash
   go run ./cmd/replay-notification \
     "$NOTIFICATION_ID" \
     "operator@example.com" \
     "Recipient configuration was corrected"
   ```

5. Confirm a new delivery cycle and eventual `SENT`.

Reasons are trimmed and limited to 1000 Unicode code points. HTTP persists the authenticated manager ID and current request ID; CLI persists its supplied operator identity and a generated request ID. The state change and replay event are atomic and replay events are append-only. A delivery has cycles `1..10`, so at most nine manual replays are possible; cycle-10 failures return `NOTIFICATION_REPLAY_LIMIT_REACHED` and require investigation rather than another send.

Only failure codes and fixed generic messages are stored. Obtain provider-specific diagnostics from the provider's access-controlled operational console or ephemeral transport telemetry; do not copy provider/authentication/certificate text into delivery records or replay reasons.

`LEASE_DURATION` must exceed one `SMTP_TIMEOUT`. `WORKER_BATCH_SIZE` limits completed claim/send iterations per poll; it does not reserve a batch of leases.

The initial migration is intentionally edited for this clean-schema submission. An existing schema or Compose volume with the old version-1 checksum must be recreated; do not alter `schema_migrations` to bypass the checksum.

## Before production

Replace development authentication with OIDC/JWT verification, use managed HA PostgreSQL, secret management, TLS, private operational endpoints, rate limiting, distributed tracing, load/failure testing, runbooks, on-call ownership, provider delivery webhooks, signed artifacts, SBOM/dependency scanning, tenant isolation, retention policy, and explicit administrative correction commands.
