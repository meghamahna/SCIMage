---
id: decision-webhook-retry-classification
kind: decision
title: Webhook retries only transient failures and dead-letters the rest
summary: Network errors, 408, 429 and 5xx retry on doubling backoff; other 4xx dead-letter at once, and attempts are capped by SCIM_WEBHOOK_MAX_ATTEMPTS
tags: [webhook, retry, backoff, jitter, dead letter, dead_letter, 4xx, 5xx, 429, max attempts, replay]
links: [decided_by:module-webhook, depends_on:data-model-webhook-outbox, related:gotcha-webhook-lease-covers-batch]
sources: [internal/webhook/dispatcher.go:274-303, internal/webhook/dispatcher.go:305-342, internal/webhook/dispatcher.go:358-386]
updated: 2026-11-28
verified_commit: 924cf37
---
## Facts
- Retryable: 408, 429 and any 5xx; a transport error is retried too → internal/webhook/dispatcher.go:364-370, internal/webhook/dispatcher.go:320-323
- Other 4xx and bad request build are permanent and go straight to dead letter, because identical bytes get the same answer → internal/webhook/dispatcher.go:358-363, internal/webhook/dispatcher.go:311-312, internal/webhook/dispatcher.go:338-341
- A 3xx counts as a non-2xx failure because redirects are not followed; it is permanent → internal/webhook/dispatcher.go:360-363, internal/webhook/dispatcher.go:172-180
- Dead letter when permanent or attempts ≥ MaxAttempts; otherwise reschedule → internal/webhook/dispatcher.go:283-303
- Backoff: BaseBackoff doubled per attempt, capped at MaxBackoff, plus up to 20% jitter → internal/webhook/dispatcher.go:372-386
- Doubling stops at the cap so a large base cannot overflow negative → internal/webhook/dispatcher.go:374-377
- Defaults (6 attempts, 5s doubling backoff) give roughly three minutes of outage tolerance → internal/webhook/dispatcher.go:55-61
- Dead letters are replayed by an operator (scimage-admin webhook replay) → cmd/scimage-admin/main.go:56-57, cmd/scimage-admin/main.go:77-78
