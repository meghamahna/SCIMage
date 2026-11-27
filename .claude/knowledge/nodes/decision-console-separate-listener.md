---
id: decision-console-separate-listener
kind: decision
title: Console runs on its own opt-in listener
summary: Admin console is a second http.Server, started only when CONSOLE_ADDR is set, so it never shares a socket with tenant SCIM traffic
tags: [decision, console, listener, CONSOLE_ADDR, loopback, separate port, admin surface, why]
links: [decided_by:module-console, related:module-console, related:module-scim]
sources: [cmd/server/main.go:36-44, cmd/server/main.go:145-158, internal/console/server.go:1-16]
updated: 2026-11-26
verified_commit: 794ebee
---
## Facts
- Console is off unless CONSOLE_ADDR is set; consoleServer returns nil otherwise → cmd/server/main.go:36-44
- Why: the privileged admin surface must not share a socket with internet-facing tenant traffic → cmd/server/main.go:145-149
- Recommended bind is 127.0.0.1:8090, so it is not reachable off-host without a tunnel → cmd/server/main.go:36-39, cmd/server/main.go:147-149
- Why opt-in: a full-mutation admin surface stays off unless an operator turns it on → cmd/server/main.go:36-39
- Console mutations reuse store.* calls, so audit guarantees carry over; the console does not add its own write path → internal/console/server.go:14-16
