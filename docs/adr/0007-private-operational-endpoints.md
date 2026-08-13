# ADR 0007: Private Operational Endpoints and Bounded Metrics

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

The API originally served unauthenticated health and metrics endpoints on the same listener as authenticated business traffic. That made accidental public exposure easy. HTTP metric labels also accepted caller-controlled method and route values, duration samples omitted status and could produce duplicate series identifiers, and every metrics scrape queried PostgreSQL for notification backlog state.

## Decision

Run the API's business and operational surfaces on separate listeners. `HTTP_ADDR` remains the public business listener and `API_ADMIN_ADDR` is the API administrative listener, with a direct-run default of `127.0.0.1:9090`. The worker administrative listener defaults to `127.0.0.1:9091`. Administrative endpoints remain unauthenticated and rely on the network boundary. Docker Compose binds the in-container administrative listeners to `:9090` and `:9091`, while every published host port binds explicitly to IPv4 loopback. Operational endpoints are excluded from the public OpenAPI contract.

Restrict HTTP method labels to `GET`, `POST`, `PUT`, or `OTHER`, and route labels to the ten business templates plus `/unknown`. Retain at most 1,024 distinct `(method, route, status)` keys. Continue updating retained keys, drop new keys beyond the cap, and increment label-free `work_management_http_series_dropped_total`. Include status on both request-count and duration samples and render every map-backed family deterministically.

Cache notification backlog values for five seconds per process. One expired scrape refreshes synchronously; concurrent scrapes serve the last completed state without waiting. A failed refresh retains the last good value, throttles retries for five seconds, and sets `work_management_metrics_scrape_error` until a later refresh succeeds.

## Alternatives considered

- Retain one listener: operationally simple, but preserves the accidental-exposure boundary.
- Authenticate operational endpoints in the application: adds credentials and failure modes while still sharing the public socket.
- Disable metrics: removes exposure but also removes required operational visibility.
- Add a Prometheus client or cache dependency: unnecessary for the fixed metric set and constant-size cache required here.

## Consequences

The API owns another port and must pre-bind, serve, and drain two servers under one lifecycle. Deployments outside local loopback still require private-network policy or an authenticated operational proxy. Backlog gauges may be five seconds stale during healthy operation and indefinitely stale across refresh failures, but the error gauge makes that explicit. Process-local counters reset on restart. Rejected HTTP label keys are never retained.

## Operational impact

Scrape only the API and worker administrative listeners. Alert on `work_management_metrics_scrape_error` and `work_management_http_series_dropped_total`. Never publish an administrative listener on a public interface. Configure private ingress or an authenticated proxy in production rather than treating loopback defaults as authorization.

## Revisit when

An authenticated operational proxy or service mesh is available, cross-instance aggregation is required, or richer histogram behavior justifies a maintained metrics client.

## Related decisions

- [ADR 0001: Modular monolith with separate API and worker processes](0001-modular-monolith.md)
- [ADR 0002: PostgreSQL transactional outbox](0002-transactional-outbox.md)
