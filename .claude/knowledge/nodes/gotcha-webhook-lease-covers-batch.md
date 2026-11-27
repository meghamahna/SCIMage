---
id: gotcha-webhook-lease-covers-batch
kind: gotcha
title: Webhook claim lease must cover the whole batch; attempts count at claim
summary: The lease is Poll + Batch×Timeout so a slow batch does not double-POST; each claim burns an attempt, so a crash mid-batch costs one
tags: [webhook, lease, claim, attempts, double send, at-least-once, outbox, timeout, batch]
links: [related:module-webhook, related:data-model-webhook-outbox]
sources: [internal/webhook/dispatcher.go:189-200, internal/webhook/dispatcher.go:254-265, internal/store/outbox.go:77-83, internal/store/outbox.go:189-198, internal/webhook/signer.go:20-22]
updated: 2026-11-26
verified_commit: 0761c5b
---
## Facts
- Lease = Poll + Batch × Timeout, because drain sends the batch sequentially → internal/webhook/dispatcher.go:196-200
- A lease shorter than the batch lets queued rows come due and be POSTed twice → internal/webhook/dispatcher.go:189-194
- The store increments attempts inside ClaimDueDeliveries, not after the send → internal/store/outbox.go:189-198
- Consequence: a dispatcher that dies mid-batch leaves its unsent rows waiting out the full lease, and each claim consumes an attempt → internal/webhook/dispatcher.go:196-197, internal/store/outbox.go:77-83
- Unsent claims on ctx cancel are not released; they return when the lease expires → internal/webhook/dispatcher.go:260-263
- Receivers must dedupe on X-SCIMage-Delivery-Id, since delivery is at-least-once → internal/webhook/signer.go:20-22
