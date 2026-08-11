-- The outbound half of a mutation, queued in the same transaction as the change
-- and its audit entry. A committed change is always queued and a rolled-back
-- one never is — the same reasoning that puts the audit row inside the
-- transaction. Sending straight from the handler after COMMIT cannot give that:
-- the process can die between the two, and there is no way to tell afterwards
-- whether the event went out.

CREATE TABLE webhook_deliveries (
    id              BIGSERIAL PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    event_type      TEXT NOT NULL,
    -- No foreign key to users, for the same reason audit_log has none: the
    -- event has to outlive a row that gets hard-deleted.
    target_id       UUID,
    payload         JSONB NOT NULL,

    -- pending -> delivered, or pending -> dead_letter once the attempts run out.
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INT NOT NULL DEFAULT 0,

    -- Doubles as the retry schedule and the in-flight lease: a dispatcher
    -- claims a row by pushing this forward, so a second dispatcher, or this one
    -- after a crash, won't send the same event concurrently.
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    last_error      TEXT,
    delivered_at    TIMESTAMPTZ,

    CONSTRAINT webhook_deliveries_status CHECK (status IN ('pending', 'delivered', 'dead_letter'))
);

-- The dispatcher's only hot query: due and still pending. Partial, so delivered
-- rows stop costing anything to skip as the table grows.
CREATE INDEX idx_webhook_deliveries_due
    ON webhook_deliveries (next_attempt_at)
    WHERE status = 'pending';

-- Reviewing the dead-letter queue is the other question worth answering fast.
CREATE INDEX idx_webhook_deliveries_dead_letter
    ON webhook_deliveries (created_at DESC)
    WHERE status = 'dead_letter';
