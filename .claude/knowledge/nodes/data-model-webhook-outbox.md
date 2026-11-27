---
id: data-model-webhook-outbox
kind: data-model
title: Webhook outbox (webhook_deliveries)
summary: Transactional outbox of change events, with pending/delivered/dead_letter states, a claim lease and partial indexes for the dispatcher
tags: [webhook, outbox, webhook_deliveries, delivery, dead letter, retry, lease, at-least-once, event, dispatcher, replay]
links: [related:decision-audit-in-transaction, related:module-store, related:gotcha-webhook-lease-covers-batch]
sources: [migrations/000004_create_webhook_deliveries.up.sql:8-42, migrations/000005_multi_tenancy.up.sql:56, migrations/000011_webhook_delivered_retention.up.sql:1-8, internal/store/outbox.go:24-38, internal/store/outbox.go:77-83, internal/store/outbox.go:105-110, internal/store/outbox.go:179-226, internal/store/outbox.go:276-288, internal/store/outbox.go:343-377]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Table columns: id BIGSERIAL, created_at, event_type, target_id (no FK), payload JSONB, status, attempts, next_attempt_at, last_error, delivered_at, tenant_id → migrations/000004_create_webhook_deliveries.up.sql:8-31, migrations/000005_multi_tenancy.up.sql:56
- status CHECK: pending, delivered, dead_letter → migrations/000004_create_webhook_deliveries.up.sql:30
- next_attempt_at is both the retry schedule and the in-flight lease → migrations/000004_create_webhook_deliveries.up.sql:22-25
- Partial index for due pending rows → migrations/000004_create_webhook_deliveries.up.sql:35-37
- Partial index for dead letters, newest first → migrations/000004_create_webhook_deliveries.up.sql:40-42
- Partial index on delivered_at for the retention purge → migrations/000011_webhook_delivered_retention.up.sql:6-8
- Event types: user.created, user.replaced, user.deactivated, group.created, group.replaced, group.deleted → internal/store/outbox.go:24-38
- Rows are inserted by enqueueChange and enqueueGroupChange inside the mutation's transaction, and only when WithChangeEvents is set → internal/store/outbox.go:109-110, internal/store/outbox.go:149-150
- ClaimDueDeliveries bumps attempts and pushes next_attempt_at forward in one UPDATE, selecting rows with FOR UPDATE SKIP LOCKED → internal/store/outbox.go:192-207
- Attempts are counted at claim time, so a crashing dispatcher still burns an attempt → internal/store/outbox.go:189-191
- Terminal transitions (delivered, dead_letter) are guarded on status = pending → internal/store/outbox.go:222-226
- Delivery is at-least-once and not deduplicated; receivers key on the delivery id → internal/store/outbox.go:105-108
- NextAttemptAt is display-only; the claim logic never reads it → internal/store/outbox.go:81-83
- PurgeDeliveredBefore deletes delivered rows older than the cutoff; pending and dead_letter rows are kept → internal/store/outbox.go:276-288
- ReplayDeadLetter resets a dead letter to pending with attempts 0, and writes a webhook.replay admin audit row in the same transaction → internal/store/outbox.go:343-377
- The HMAC signature is recomputed at send time, not stored, so replays carry a fresh timestamp → internal/store/outbox.go:343-345
