# Configuration

Every setting comes from an environment variable. Copy `.env.example` to `.env`
and fill it in — `.env` is gitignored, and `make` targets load it for you.

Generate secrets with `openssl rand -hex 32`. The server requires at least 16
characters for both `SCIM_TOKEN` and `SCIM_WEBHOOK_SECRET`.

## Core

| Variable | Purpose |
|---|---|
| `SCIM_TOKEN` | Bearer token every request presents. Required, and validated at startup. |
| `DATABASE_URL` | Postgres connection string. Assembled from `POSTGRES_*` when absent. |
| `SCIM_ADDR` | Listen address. Defaults to `:8080`. |
| `SCIM_BASE_URL` | External base URL, used for `Location` and `meta.location` behind a proxy. |

`SCIM_BASE_URL` matters behind a TLS-terminating proxy: the request arrives as
plain HTTP there, so links derived from the `Host` header would advertise `http`.

## Rate limiting

| Variable | Purpose |
|---|---|
| `SCIM_RATE_LIMIT` | Sustained requests per second per caller. Defaults to 20. |
| `SCIM_RATE_BURST` | Burst allowance. Defaults to 40. |

Sized for provisioning traffic: an identity provider pushing a bulk import sends
bursts, then goes quiet for hours. Set `SCIM_RATE_LIMIT=0` to opt out.

## Change delivery

| Variable | Purpose |
|---|---|
| `SCIM_WEBHOOK_URL` | Endpoint for change events. Set it to turn change delivery on. |
| `SCIM_WEBHOOK_SECRET` | HMAC signing secret. Required alongside a webhook URL. |
| `SCIM_WEBHOOK_ALLOW_HTTP` | Set to `1` to allow a plaintext endpoint. |
| `SCIM_WEBHOOK_MAX_ATTEMPTS` | Attempts before a delivery is dead-lettered. Defaults to 6. |

Leaving `SCIM_WEBHOOK_URL` unset keeps change delivery off, and the store skips
queueing events — the queue stays empty while nothing is draining it.

A URL configured with no secret is a startup error, so every event that goes out
is signed. `SCIM_WEBHOOK_ALLOW_HTTP=1` is meant for a local receiver during
development: events carry user attributes, and the signature protects
authenticity rather than confidentiality.

Six attempts on the default doubling backoff spans roughly five minutes of
receiver downtime. See [ARCHITECTURE.md](ARCHITECTURE.md#retry-and-dead-letter)
for what retries and what parks immediately.

## Logging

| Variable | Purpose |
|---|---|
| `LOG_DIR` | Directory for dated log files. Defaults to `logs/`; set it empty for stdout only. |
| `LOG_LEVEL` | `debug`, `info`, `warn` or `error`. Defaults to `info`. |
| `SCIM_LOG_REQUESTS` | Set to `1` to record request bodies. Off by default. |

`SCIM_LOG_REQUESTS=1` records full request bodies, which is how a client's actual
behaviour gets diagnosed. Those entries carry user attributes, so the directory
is created `0700` and files `0600`. In a container, set `LOG_DIR=` empty and let
the runtime collect stdout.

## Database

Used when `DATABASE_URL` is absent, and by `docker-compose.yml`.

| Variable | Purpose |
|---|---|
| `POSTGRES_USER` | Database user. |
| `POSTGRES_PASSWORD` | Database password. |
| `POSTGRES_DB` | Database name. |
| `POSTGRES_PORT` | Host port for the container. Defaults to 5432. |

## Rotating the bearer token

1. Generate a new token: `openssl rand -hex 32`
2. Set it as `SCIM_TOKEN` and restart the server.
3. Update the token in your identity provider.

Treat `SCIM_TOKEN` as a privileged credential — it authorizes directory changes —
and rotate it whenever exposure is suspected. Overlap-window rotation, which
keeps the server running through a rotation, arrives with issued tokens in
Phase 10.
