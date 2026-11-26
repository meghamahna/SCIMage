---
id: module-webhook
kind: module
title: Webhook dispatcher and signing
summary: Drains the webhook outbox to one configured HTTPS endpoint with HMAC-signed POSTs, retries, dead letters and retention pruning
tags: [webhook, webhooks, dispatcher, outbox, delivery, retry, backoff, dead letter, signing, hmac, SCIM_WEBHOOK_URL, SCIM_WEBHOOK_SECRET]
links: [depends_on:data-model-webhook-outbox, depends_on:module-store, related:decision-audit-in-transaction, related:decision-webhook-signed-payload, decided_by:decision-webhook-retry-classification, related:gotcha-webhook-lease-covers-batch, related:module-console, related:entrypoint-cmd-server-main]
sources: [internal/webhook/dispatcher.go:21-30, internal/webhook/dispatcher.go:74-126, internal/webhook/dispatcher.go:204-234, internal/webhook/dispatcher.go:254-303, internal/webhook/signer.go:16-26, internal/webhook/status.go:14-44]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Package delivers SCIM change events from the outbox to one application endpoint; it does not generate events → internal/webhook/dispatcher.go:1-3
- The `Queue` interface (claim, mark delivered, reschedule, dead letter, purge) is what the dispatcher needs; `*store.Store` satisfies it, so tests need no database → internal/webhook/dispatcher.go:21-32
- Optional: no dispatcher runs unless SCIM_WEBHOOK_URL is set; the store only queues events when it is set (WithChangeEvents) → internal/webhook/dispatcher.go:74-77, cmd/server/main.go:86-89
- Endpoint must be https. http needs SCIM_WEBHOOK_ALLOW_HTTP=1 and logs a plaintext warning with scheme/host/path only → internal/webhook/dispatcher.go:95-107
- SCIM_WEBHOOK_SECRET must be at least 16 chars, or startup fails; no unsigned sends → internal/webhook/dispatcher.go:109-112
- Other env: SCIM_WEBHOOK_MAX_ATTEMPTS (default 6), SCIM_WEBHOOK_RETENTION_DAYS (default 30; 0 keeps rows forever) → internal/webhook/dispatcher.go:117, internal/webhook/dispatcher.go:128-141
- Defaults: poll 5s, batch 20, HTTP timeout 10s, backoff 5s doubling to 5min cap, retention sweep hourly → internal/webhook/dispatcher.go:58-72
- Loop: drain claimed batch, then wait on poll ticker or retention ticker until ctx is cancelled → internal/webhook/dispatcher.go:204-234
- Batch is sent sequentially; each row's attempt → MarkDelivered, Reschedule or DeadLetter → internal/webhook/dispatcher.go:254-303
- Redirects are not followed (payload is signed for the configured endpoint only) → internal/webhook/dispatcher.go:172-180
- Request headers: Content-Type json, User-Agent SCIMage, X-SCIMage-Event, X-SCIMage-Delivery-Id, X-SCIMage-Signature → internal/webhook/dispatcher.go:314-318, internal/webhook/signer.go:16-26
- Response body read is capped at 2 KiB and stored as last_error text; URL errors reduce to the host only → internal/webhook/dispatcher.go:326-356
- Retention prune runs on its own cadence and only deletes delivered rows older than the cutoff → internal/webhook/dispatcher.go:236-252
- Shutdown: dispatcher context is cancelled after the HTTP listeners drain; wg.Wait ensures it stops first → cmd/server/main.go:100-104, cmd/server/main.go:190-197
- Console status view is secret-free and strips the query string from the endpoint → internal/webhook/status.go:9-13, internal/webhook/status.go:46-54
- Receiver helper `Verify` checks signature, timestamp tolerance and constant-time MAC compare → internal/webhook/signer.go:68-95
