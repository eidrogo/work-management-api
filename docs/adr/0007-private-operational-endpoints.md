# ADR 0007: Private Operational Endpoints and Bounded Metrics

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

I originally served unauthenticated health and metrics endpoints on the same listener as authenticated business traffic, which made accidental public exposure easy. I also accepted caller-controlled method and route values for HTTP metric labels; duration samples omitted status and could produce duplicate series identifiers, and every metrics scrape queried PostgreSQL for notification backlog state.

## Decision

I chose to run the API's business and operational surfaces on separate listeners. `HTTP_ADDR` remains the public business listener and `API_ADMIN_ADDR` is the API administrative listener, with a direct-run default of `127.0.0.1:9090`. The worker administrative listener defaults to `127.0.0.1:9091`. I keep administrative endpoints unauthenticated and rely on the network boundary. I made Docker Compose bind the in-container administrative listeners to `:9090` and `:9091`, while every published host port binds explicitly to IPv4 loopback. I exclude operational endpoints from the public OpenAPI contract.

I restrict HTTP method labels to `GET`, `POST`, `PUT`, or `OTHER`, and route labels to the ten business templates plus `/unknown`. I retain at most 1,024 distinct `(method, route, status)` keys. I continue updating retained keys, drop new keys beyond the cap, and increment label-free `work_management_http_series_dropped_total`. I include status on both request-count and duration samples and render every map-backed family deterministically.

I cache notification backlog values for five seconds per process. One expired scrape refreshes synchronously; concurrent scrapes serve the last completed state without waiting. A failed refresh retains the last good value, throttles retries for five seconds, and sets `work_management_metrics_scrape_error` until a later refresh succeeds.

## Alternatives considered

- I rejected retaining one listener because, although operationally simple, it preserves the accidental-exposure boundary.
- I rejected authenticating operational endpoints in the application because it adds credentials and failure modes while still sharing the public socket.
- I rejected disabling metrics because it removes exposure but also removes required operational visibility.
- I rejected adding a Prometheus client or cache dependency because it is unnecessary for the fixed metric set and constant-size cache required here.

## Consequences

I accepted another API port and made the API pre-bind, serve, and drain two servers under one lifecycle. I require deployments outside local loopback to use private-network policy or an authenticated operational proxy. I accepted that backlog gauges may be five seconds stale during healthy operation and indefinitely stale across refresh failures, while the error gauge makes that explicit. Process-local counters reset on restart. Rejected HTTP label keys are never retained.

## Operational impact

I will scrape only the API and worker administrative listeners. I will alert on `work_management_metrics_scrape_error` and `work_management_http_series_dropped_total`. I will never publish an administrative listener on a public interface. I will configure private ingress or an authenticated proxy in production rather than treating loopback defaults as authorization.

## Revisit when

I will revisit this when an authenticated operational proxy or service mesh is available, cross-instance aggregation is required, or richer histogram behavior justifies a maintained metrics client.

## Related decisions

- [ADR 0001: Modular monolith with separate API and worker processes](0001-modular-monolith.md)
- [ADR 0002: PostgreSQL transactional outbox](0002-transactional-outbox.md)
