---
id: data-model-webhook-outbox
kind: data-model
title: Webhook outbox (webhook_deliveries)
summary: Transactional outbox of change events, with pending/delivered/dead_letter states, a claim lease and partial indexes for the dispatcher
tags: [webhook, outbox, webhook_deliveries, delivery, dead letter, retry, lease, at-least-once, event, dispatcher, replay]
links: [related:decision-audit-in-transaction, related:module-store]
sources: [migrations/000004_create_webhook_deliveries.up.sql:8-42, migrations/000005_multi_tenancy.up.sql:56, migrations/000011_webhook_delivered_retention.up.sql:1-8, internal/store/outbox.go:24-40, internal/store/outbox.go:105-136, internal/store/outbox.go:180-226, internal/store/outbox.go:271-290, internal/store/outbox.go:340-377]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Table columns: id BIGSERIAL, created_at, event_type, target_id (no FK), payload JSONB, status, attempts, next_attempt_at, last_error, delivered_at, tenant_id → migrations/000004_create_webhook_deliveries.up.sql:8-31, migrations/000005_multi_tenancy.up.sql:56
- status CHECK: pending, delivered, dead_letter → migrations/000004_create_webhook_deliveries.up.sql:30
- next_attempt_at is both the retry schedule and the in-flight lease → migrations/000004_create_webhook_deliveries.up.sql:22-25
- Partial index for due pending rows → migrations/000004_create_webhook_deliveries.up.sql:35-37
- Partial index for dead letters, newest first → migrations/000004_create_webhook_deliveries.up.sql:40-42
- Partial index on delivered_at for the retention purge → migrations/000011_webhook_delivered_retention.up.sql:6-8
- Event types: user.created, user.replaced, user.deactivated, group.created, group.replaced, group.deleted → internal/store/outbox.go:24-40
- Rows are inserted by enqueueChange/enqueueGroupChange in the mutation's transaction, only when WithChangeEvents is set → internal/store/outbox.go:109-136
- ClaimDueDeliveries bumps attempts and pushes next_attempt_at forward, using FOR UPDATE SKIP LOCKED → internal/store/outbox.go:192-219
- Attempts are counted at claim time so a crashing dispatcher still burns an attempt → internal/store/outbox.go:189-191
- Terminal transitions are guarded on status = pending → internal/store/outbox.go:222-226
- Delivery is at-least-once, not deduplicated; receivers key on the delivery id → internal/store/outbox.go:105-108
- PurgeDeliveredBefore deletes old delivered rows (retention sweep) → internal/store/outbox.go:280-288
- ReplayDeadLetter resets a dead letter to pending with attempts 0, and writes webhook.replay to admin audit in one transaction → internal/store/outbox.go:346-377
- Signature is recomputed at send time, not stored, so replays carry a fresh timestamp → internal/store/outbox.go:340-345
