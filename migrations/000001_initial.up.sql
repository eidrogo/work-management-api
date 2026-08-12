CREATE TABLE users (
    id UUID PRIMARY KEY,
    display_name VARCHAR(200) NOT NULL CHECK (btrim(display_name) <> ''),
    email VARCHAR(320) NOT NULL CHECK (btrim(email) <> ''),
    role VARCHAR(20) NOT NULL CHECK (role IN ('MANAGER', 'WORKER')),
    created_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX users_email_lower_uq ON users (lower(email));
CREATE INDEX users_role_id_idx ON users (role, id);

CREATE TABLE auth_tokens (
    token_hash CHAR(64) PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NULL
);
CREATE INDEX auth_tokens_user_id_idx ON auth_tokens (user_id);

CREATE TABLE work_items (
    id UUID PRIMARY KEY,
    title VARCHAR(200) NOT NULL CHECK (btrim(title) <> ''),
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(30) NOT NULL CHECK (status IN ('UNASSIGNED', 'ASSIGNED', 'IN_PROGRESS', 'COMPLETED')),
    creator_id UUID NOT NULL REFERENCES users(id),
    assignee_id UUID NULL REFERENCES users(id),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT work_items_status_assignee_ck CHECK (
        (status = 'UNASSIGNED' AND assignee_id IS NULL)
        OR (status IN ('ASSIGNED', 'IN_PROGRESS', 'COMPLETED') AND assignee_id IS NOT NULL)
    )
);
CREATE INDEX work_items_manager_status_page_idx ON work_items (status, created_at DESC, id DESC);
CREATE INDEX work_items_manager_page_idx ON work_items (created_at DESC, id DESC);
CREATE INDEX work_items_worker_queue_idx ON work_items (assignee_id, status, created_at DESC, id DESC);

CREATE TABLE idempotency_keys (
    actor_id UUID NOT NULL REFERENCES users(id),
    operation VARCHAR(100) NOT NULL,
    idempotency_key VARCHAR(200) NOT NULL,
    request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    resource_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor_id, operation, idempotency_key)
);

CREATE TABLE work_item_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id UUID NOT NULL UNIQUE,
    work_item_id UUID NOT NULL REFERENCES work_items(id) ON DELETE RESTRICT,
    actor_id UUID NOT NULL REFERENCES users(id),
    event_type VARCHAR(50) NOT NULL CHECK (event_type IN (
        'WORK_ITEM_CREATED','WORK_ITEM_ASSIGNED','WORK_ITEM_REASSIGNED',
        'WORK_ITEM_MARKED_IN_PROGRESS','WORK_ITEM_COMPLETED'
    )),
    from_status VARCHAR(30) NULL,
    to_status VARCHAR(30) NULL,
    previous_assignee_id UUID NULL REFERENCES users(id),
    new_assignee_id UUID NULL REFERENCES users(id),
    reason TEXT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX work_item_events_item_id_idx ON work_item_events (work_item_id, id);

CREATE TABLE notification_deliveries (
    id UUID PRIMARY KEY,
    work_item_id UUID NOT NULL REFERENCES work_items(id) ON DELETE RESTRICT,
    assignment_event_id BIGINT NOT NULL UNIQUE REFERENCES work_item_events(id) ON DELETE RESTRICT,
    recipient_email VARCHAR(320) NOT NULL CHECK (btrim(recipient_email) <> ''),
    recipient_name VARCHAR(200) NOT NULL CHECK (btrim(recipient_name) <> ''),
    event_type VARCHAR(80) NOT NULL CHECK (event_type = 'WORK_ITEM_ASSIGNED_EMAIL'),
    payload JSONB NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('PENDING', 'PROCESSING', 'SENT', 'FAILED')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    delivery_cycle INTEGER NOT NULL DEFAULT 1 CHECK (delivery_cycle > 0),
    cycle_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (cycle_attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NULL,
    last_error TEXT NULL,
    message_id VARCHAR(255) NOT NULL,
    provider_message_id VARCHAR(255) NULL,
    locked_by VARCHAR(255) NULL,
    locked_until TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_attempt_at TIMESTAMPTZ NULL,
    sent_at TIMESTAMPTZ NULL,
    deduplication_key VARCHAR(255) NOT NULL UNIQUE,
    CONSTRAINT notification_delivery_lease_ck CHECK (
        (status = 'PROCESSING' AND locked_by IS NOT NULL AND locked_until IS NOT NULL)
        OR (status <> 'PROCESSING' AND locked_by IS NULL AND locked_until IS NULL)
    ),
    CONSTRAINT notification_delivery_schedule_ck CHECK (
        (status IN ('PENDING', 'PROCESSING') AND next_attempt_at IS NOT NULL)
        OR (status IN ('SENT', 'FAILED') AND next_attempt_at IS NULL)
    ),
    CONSTRAINT notification_delivery_sent_ck CHECK (
        (status = 'SENT' AND sent_at IS NOT NULL)
        OR (status <> 'SENT' AND sent_at IS NULL)
    )
);
CREATE INDEX notification_deliveries_claim_idx ON notification_deliveries (next_attempt_at, created_at, id) WHERE status = 'PENDING';
CREATE INDEX notification_deliveries_expired_lease_idx ON notification_deliveries (locked_until, created_at, id) WHERE status = 'PROCESSING';
CREATE INDEX notification_deliveries_status_page_idx ON notification_deliveries (status, created_at DESC, id DESC);
CREATE INDEX notification_deliveries_work_item_idx ON notification_deliveries (work_item_id, created_at DESC, id DESC);

CREATE TABLE notification_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    notification_id UUID NOT NULL REFERENCES notification_deliveries(id) ON DELETE RESTRICT,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    worker_id VARCHAR(255) NOT NULL,
    outcome VARCHAR(30) NOT NULL CHECK (outcome IN ('SENT', 'RETRY_SCHEDULED', 'FAILED', 'ABANDONED')),
    error_message TEXT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    UNIQUE (notification_id, attempt_number)
);
CREATE INDEX notification_attempts_notification_idx ON notification_attempts (notification_id, attempt_number);

CREATE OR REPLACE FUNCTION enforce_work_item_assignee_role()
RETURNS trigger AS $$
BEGIN
    IF NEW.assignee_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM users WHERE id = NEW.assignee_id AND role = 'WORKER') THEN
        RAISE EXCEPTION 'work item assignee must reference a worker' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER work_items_enforce_assignee_role BEFORE INSERT OR UPDATE ON work_items FOR EACH ROW EXECUTE FUNCTION enforce_work_item_assignee_role();

CREATE OR REPLACE FUNCTION protect_assigned_worker_role()
RETURNS trigger AS $$
BEGIN
    IF NEW.role <> 'WORKER' AND EXISTS (SELECT 1 FROM work_items WHERE assignee_id = NEW.id) THEN
        RAISE EXCEPTION 'assigned users must retain the worker role' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER users_protect_assigned_worker_role BEFORE UPDATE OF role ON users FOR EACH ROW EXECUTE FUNCTION protect_assigned_worker_role();

CREATE OR REPLACE FUNCTION protect_work_item_invariants()
RETURNS trigger AS $$
BEGIN
    IF NEW.creator_id IS DISTINCT FROM OLD.creator_id THEN
        RAISE EXCEPTION 'work item creator is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'COMPLETED' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed work item is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER work_items_protect_invariants BEFORE UPDATE ON work_items FOR EACH ROW EXECUTE FUNCTION protect_work_item_invariants();

CREATE OR REPLACE FUNCTION reject_append_only_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = '23514';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER work_item_events_append_only BEFORE UPDATE OR DELETE ON work_item_events FOR EACH ROW EXECUTE FUNCTION reject_append_only_mutation();
CREATE TRIGGER notification_attempts_append_only BEFORE UPDATE OR DELETE ON notification_attempts FOR EACH ROW EXECUTE FUNCTION reject_append_only_mutation();
