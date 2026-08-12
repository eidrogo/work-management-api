# Architecture Overview

## Component architecture

```mermaid
flowchart LR
    Client[Manager / Worker / Future Client]
    API[Go HTTP API<br/>cmd/api]
    Auth[Development Token Resolver<br/>replaceable identity boundary]
    Domain[Work-item Application Service<br/>authorization + lifecycle]
    PG[(PostgreSQL<br/>system of record)]
    Events[(work_item_events<br/>append-only)]
    Outbox[(notification_deliveries<br/>durable outbox)]
    Worker[Notification Worker<br/>cmd/worker]
    Attempts[(notification_attempts<br/>append-only)]
    SMTP[SMTP Server / Provider]
    Mailpit[Mailpit local sink]
    Metrics[Prometheus-compatible metrics]
    Ops[Health / logs / replay command]

    Client -->|Bearer token + JSON /v1| API
    API --> Auth
    API --> Domain
    Domain -->|explicit transaction| PG
    PG --- Events
    PG --- Outbox
    Worker -->|SKIP LOCKED lease| Outbox
    Worker -->|SMTP| SMTP
    SMTP -. local .-> Mailpit
    Worker --> Attempts
    API --> Metrics
    Worker --> Metrics
    API --> Ops
    Worker --> Ops
```

The API and worker are independently runnable processes but share one module, domain vocabulary, schema, release artifact, and operational conventions.

## Assignment and notification sequence

```mermaid
sequenceDiagram
    autonumber
    actor Manager
    participant API as HTTP API
    participant Auth as Authenticator
    participant Domain as Work-item Service
    participant DB as PostgreSQL
    participant Worker as Notification Worker
    participant SMTP as SMTP Server / Provider

    Manager->>API: PUT /v1/work-items/{id}/assignment<br/>Bearer + If-Match
    API->>Auth: Resolve token hash
    Auth->>DB: Read principal
    DB-->>Auth: Manager principal
    API->>Domain: Assign(actor, assignee, expected version)
    Domain->>DB: BEGIN
    Domain->>DB: Read work item and assignee
    Domain->>Domain: Authorize + validate lifecycle
    Domain->>DB: Conditional work-item UPDATE
    Domain->>DB: INSERT assignment history event
    Domain->>DB: INSERT PENDING delivery (unique dedup key)
    Domain->>DB: COMMIT
    DB-->>Domain: Updated item
    Domain-->>API: 200 + ETag
    API-->>Manager: Assignment committed

    loop Poll due work
        Worker->>DB: SELECT ... FOR UPDATE SKIP LOCKED
        DB-->>Worker: Leased delivery
        Worker->>SMTP: MAIL / RCPT / DATA
        alt SMTP accepts DATA
            SMTP-->>Worker: 250 accepted
            Worker->>DB: Mark SENT + append SENT attempt
        else Retryable 4xx/network failure
            SMTP-->>Worker: temporary/ambiguous failure
            Worker->>DB: Mark PENDING + next_attempt_at<br/>append RETRY_SCHEDULED attempt
        else Permanent or exhausted
            SMTP-->>Worker: permanent failure
            Worker->>DB: Mark FAILED<br/>append FAILED attempt
        end
    end

    opt Worker crashes while leased
        Worker-xSMTP: Process stops
        Note over DB: Lease expires
        Worker->>DB: Reclaim row, append ABANDONED attempt
    end
```

## Data ownership

- `work_items` is the current state optimized for operational reads.
- `work_item_events` is append-only mutation history.
- `notification_deliveries` is current delivery/lease/retry state.
- `notification_attempts` is append-only attempt history.
- Assignment update, assignment event, and initial delivery insert are one database transaction.
- Email transport never runs inside that transaction.
