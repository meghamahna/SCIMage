# Configuration

Every setting comes from an environment variable. Copy `.env.example` to `.env`
and fill it in — `.env` is gitignored, and `make` targets load it for you.

Generate secrets with `openssl rand -hex 32`. The server requires at least 16
characters for `SCIM_WEBHOOK_SECRET`.

## Core

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | Postgres connection string. Assembled from `POSTGRES_*` when absent. |
| `SCIM_ADDR` | Listen address. Defaults to `:8080`. |
| `SCIM_BASE_URL` | External base URL, used for `Location`, `meta.location` and the URL `scimage-admin tenant create` prints, behind a proxy. |

`SCIM_BASE_URL` matters behind a TLS-terminating proxy: the request arrives as
plain HTTP there, so links derived from the `Host` header would advertise `http`.

## Rate limiting

| Variable | Purpose |
| --- | --- |
| `SCIM_RATE_LIMIT` | Sustained requests per second per caller. Defaults to 20. |
| `SCIM_RATE_BURST` | Burst allowance. Defaults to 40. |

Sized for provisioning traffic: an identity provider pushing a bulk import sends
bursts, then goes quiet for hours. Set `SCIM_RATE_LIMIT=0` to opt out.

## Change delivery

| Variable | Purpose |
| --- | --- |
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
| --- | --- |
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
| --- | --- |
| `POSTGRES_USER` | Database user. |
| `POSTGRES_PASSWORD` | Database password. |
| `POSTGRES_DB` | Database name. |
| `POSTGRES_PORT` | Host port for the container. Defaults to 5432. |

## Tenants and tokens

There is no bearer-token environment variable: authentication is issued,
tenant by tenant, through `cmd/scimage-admin`, which connects to Postgres
directly rather than over the network — the privileged surface for creating a
tenant or minting a credential is never a network endpoint.

```bash
scimage-admin tenant create -name "Acme Corp"
scimage-admin tenant list
scimage-admin token issue -tenant <tenantID> -label "Okta prod" [-expires 90d]
scimage-admin token list -tenant <tenantID>
scimage-admin token revoke <keyID>
```

A token is shown once, at `issue` time, and only its `sha256` hash is ever
stored. `label` is what a `token list` row is recognised by later, so name it
after what's calling — "Okta prod", "Entra staging" — not who ran the command.

**Rotation with overlap.** A tenant can hold several live tokens at once, so
rotation doesn't require downtime: issue a new token, update it in the
identity provider, confirm traffic has moved (`token list` shows
`last_used_at`), then `token revoke` the old one. Revoking is immediate and
irreversible — a new token has to be issued if one is needed again.

Treat every issued token as a privileged credential — it authorizes changes
to that tenant's directory — and revoke it as soon as exposure is suspected.
