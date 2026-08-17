# Connecting Okta

This guide points Okta's SCIM provisioning at a SCIMage tenant. It assumes the
server is deployed behind TLS and reachable from Okta, and that you have run the
[setup](../README.md) so `scimage-admin` can talk to the database.

SCIMage is the SCIM *service provider*; Okta is the *client*. Okta reads the
discovery documents first, so what it offers to configure reflects what this
server actually supports.

## 1. Create the tenant and a token

Each Okta app provisions into one SCIMage tenant, over its own URL with its own
token. Get the admin CLI with `go install ./cmd/scimage-admin` (puts
`scimage-admin` on your PATH), or run it in place with `go run ./cmd/scimage-admin`.

```bash
scimage-admin tenant create -name "Acme Corp"
# → prints the TENANT ID and the SCIM BASE URL

scimage-admin token issue -tenant <tenantID> -label "Okta prod"
# → prints the token ONCE. Copy it now; it is not stored anywhere.
```

The **SCIM base URL** is `https://<your-host>/scim/v2/<tenantID>` (set
`SCIM_BASE_URL` on the server so the printed value and the `Location` links it
returns are correct). The **token** is the OAuth bearer token Okta will send.

## 2. Configure the SCIM connection in Okta

In the Okta Admin Console, open your SCIM 2.0 app (or create one via
**Applications → Create App Integration → SCIM 2.0**), then **Provisioning →
Integration → Configure API Integration**:

| Field | Value |
| --- | --- |
| SCIM connector base URL | `https://<your-host>/scim/v2/<tenantID>` |
| Unique identifier field for users | `userName` |
| Authentication Mode | OAuth 2 Bearer Token |
| OAuth Bearer Token | the token from step 1 |
| Supported provisioning actions | Push New Users, Push Profile Updates, Push Groups |

Click **Test Connector Configuration**. Okta reads `/ServiceProviderConfig` and
confirms bearer auth, filtering and PATCH are advertised.

## 3. Enable provisioning to the app

On **Provisioning → To App**, enable:

- **Create Users:** Okta `POST`s the new user; SCIMage returns `201` with a
  `Location`. A duplicate `userName` (case-insensitive) is a `409`.
- **Update User Attributes:** sent as `PUT`/`PATCH`.
- **Deactivate Users:** sent as `PATCH {"op":"replace","path":"active","value":
  false}`, which emits a `user.deactivated` change event. SCIMage soft-deletes:
  the row is kept as inactive, preserving the audit trail.

## 4. Attribute mappings

SCIMage models a deliberately minimal, typed core: `userName`,
`name.givenName`, `name.familyName`, a primary `email`, `active`, and
`externalId`. Okta's default SCIM mappings line up with these out of the box.

To sync anything beyond that core (`displayName`, `title`, phone numbers, the
enterprise extension, a custom field), register it on the tenant first, then map
Okta to it:

```bash
scimage-admin attribute register -tenant <tenantID> -name displayName
```

The server must also be started with `SCIM_EXTENDED_ATTRIBUTES=1`. Registered
attributes appear in the tenant's `/Schemas` document, so they become mappable in
Okta. See [CONFIGURATION.md](CONFIGURATION.md#extensible-attributes). An attribute
Okta sends that is neither modeled nor registered is accepted and dropped, not an
error, so an over-broad default mapping won't break provisioning. It just won't
persist.

## 5. Groups

On the **Push Groups** tab, push the groups you want mirrored. SCIMage creates a
Group with its members, validating each member against this tenant's users. A
member id that doesn't exist, or belongs to another tenant, rejects the whole
push rather than silently dropping it. Membership changes arrive as `PATCH` and
emit `group.replaced`.

## What Okta cannot use here

The discovery document is honest about the edges, so Okta won't offer these:

- **Import (read) from SCIMage** isn't a supported flow: this is a push-only
  provisioning target.
- **Bulk, sorting and ETags** are not supported.
- **Filtering** is limited to `userName eq` and `externalId eq`, which is what
  Okta uses to reconcile before a create. Other filters return `invalidFilter`.

## Troubleshooting

- **Test connector fails:** check the base URL includes the `/scim/v2/<tenantID>`
  path, the token is the full `scimage_…` string, and the tenant in the token
  matches the tenant in the URL (a token for one tenant is a `401` on another's
  path).
- **A user won't create:** a `409` means the `userName` already exists for this
  tenant (case-insensitively). Turn on `SCIM_LOG_REQUESTS=1` on the server to see
  exactly what Okta sent (the log path is `0600`; bodies carry user attributes).
- **An attribute isn't persisting:** it's outside the core and not registered.
  Register it and set `SCIM_EXTENDED_ATTRIBUTES=1`.
