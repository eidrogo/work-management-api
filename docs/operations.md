# Failure Modes, Operations, and Production Roadmap

## Implemented operational behavior

- JSON logs and request IDs.
- API and worker liveness/readiness endpoints.
- Database-backed readiness with timeouts.
- HTTP and SMTP timeouts.
- Prometheus-compatible request, authorization, mutation, delivery, backlog, and age metrics.
- Expiring notification leases and abandoned-attempt recovery.
- Bounded retries and explicit terminal failure.
- Manager API and CLI replay.
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
2. Inspect `/v1/notifications/{id}` including attempts and last error.
3. Correct the cause.
4. Replay through the API or `cmd/replay-notification`.
5. Confirm a new delivery cycle and eventual `SENT`.

## Before production

Replace development authentication with OIDC/JWT verification, use a maintained PostgreSQL Go driver, managed HA PostgreSQL, secret management, TLS, private operational endpoints, rate limiting, distributed tracing, load/failure testing, runbooks, on-call ownership, provider delivery webhooks, signed artifacts, SBOM/dependency scanning, tenant isolation, retention policy, and explicit administrative correction commands.
