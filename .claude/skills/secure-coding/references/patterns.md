# Secure coding patterns

## Secrets
```python
import os
API_KEY = os.environ["PAYMENTS_API_KEY"]  # fail fast if missing; never default to a real value
```
```ts
const apiKey = process.env.PAYMENTS_API_KEY;
if (!apiKey) throw new Error("PAYMENTS_API_KEY is not set");
```
- Commit a `.env.example` with **names and placeholder values only**.
- In production, prefer a secret manager (AWS Secrets Manager, GCP Secret Manager, Vault, 1Password) over `.env` files.

## SQL
```python
cur.execute("SELECT * FROM users WHERE email = %s", (email,))   # good
cur.execute(f"SELECT * FROM users WHERE email = '{email}'")     # never
```

## Shell
```python
subprocess.run(["git", "log", "--oneline", branch], check=True)  # good: arg array
subprocess.run(f"git log {branch}", shell=True)                    # never with user input
```

## Path traversal
```python
from pathlib import Path
BASE = Path("/srv/uploads").resolve()
def safe_path(name: str) -> Path:
    p = (BASE / name).resolve()
    if not p.is_relative_to(BASE):
        raise ValueError("path escapes upload root")
    return p
```

## SSRF guard
- Parse the URL, resolve DNS, and reject private, loopback and link-local ranges (`10/8`, `172.16/12`, `192.168/16`, `127/8`, `169.254/16`, `::1`, `fc00::/7`).
- Re-check after redirects, or disable redirects altogether.
- Prefer an allowlist of hosts.

## Passwords
- Use argon2id (preferred) or bcrypt (cost ≥ 12) through a maintained library.
- Compare tokens in constant time (`hmac.compare_digest`, `crypto.timingSafeEqual`).

## Sessions and cookies
- Set `Secure; HttpOnly; SameSite=Lax` (or `Strict`).
- Rotate the session id on login and on privilege change.
- Use short-lived access tokens with rotating refresh tokens.

## Logging
```python
log.info("login failed", extra={"user_id": user.id})       # good: id, no email/password
log.info(f"login failed for {email} with {password}")      # never
```

## Deserialization
- Use JSON with schema validation for untrusted data.
- Never call `pickle.loads`, `yaml.load` (use `safe_load`), or Java native deserialization on untrusted input.

## Security headers (web)
- `Content-Security-Policy` (no `unsafe-inline` where avoidable)
- `Strict-Transport-Security`
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `frame-ancestors` via CSP
