---
id: decision-webhook-signed-payload
kind: decision
title: Webhook signature covers timestamp, delivery id, event and body
summary: Each delivery's HMAC-SHA256 covers the timestamp, delivery id and event type as well as the body, so receivers can reject replays and forged routing
tags: [webhook, signature, hmac, sha256, replay, timestamp, signing, verify, X-SCIMage-Signature]
links: [decided_by:module-webhook, related:module-webhook]
sources: [internal/webhook/signer.go:38-66, internal/webhook/signer.go:68-95]
updated: 2026-11-26
verified_commit: 78e4d28
---
## Facts
- Header format is `t=<unix>,v1=<hex>` under X-SCIMage-Signature → internal/webhook/signer.go:16-18, internal/webhook/signer.go:48-51
- MAC is HMAC-SHA256 over newline-terminated timestamp, delivery id and event, then the raw body → internal/webhook/signer.go:58-66
- Why the timestamp is inside the MAC: a receiver can reject old stamps, and a captured request cannot be re-stamped without the secret → internal/webhook/signer.go:40-47
- Why the delivery id is covered: deduplicating on an unsigned id would let a replay pass under a fresh id → internal/webhook/signer.go:44-46
- Why the event type is covered: routing on an unsigned header is no better than not checking it → internal/webhook/signer.go:46-47
- Field separators are unambiguous because only the body is arbitrary and it comes last → internal/webhook/signer.go:53-57
- Verify rejects skew beyond tolerance in either direction and compares with hmac.Equal → internal/webhook/signer.go:83-93
- Signing is an authenticity control, not confidentiality; HTTPS is still required for the payload → internal/webhook/dispatcher.go:92-94
- Secret minimum (16 chars) matches the bearer token floor → internal/webhook/signer.go:28-30
