-- Retention prunes delivered rows on a slow sweep so the outbox stays bounded
-- (see store.PurgeDeliveredBefore). This partial index turns that periodic
-- DELETE into a range scan over just the delivered rows, rather than a
-- full-table scan that would get slower as the table fills with the very rows
-- it exists to remove.
CREATE INDEX idx_webhook_deliveries_delivered_at
    ON webhook_deliveries (delivered_at)
    WHERE status = 'delivered';
