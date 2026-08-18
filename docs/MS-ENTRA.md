# Connecting Microsoft Entra ID

This guide points Entra ID (formerly Azure AD) provisioning at a SCIMage tenant.
SCIMage's core CRUD, filtering and PATCH are validated against
[Microsoft's Entra SCIM validator](https://scimvalidator.microsoft.com/).

SCIMage is the SCIM *service provider*; Entra is the *client*. It reads the
discovery documents first, so what it can configure reflects what this server
supports.

## 1. Create the tenant and a token

Each Entra enterprise application provisions into one SCIMage tenant. Get the
admin CLI with `go install ./cmd/scimage-admin` (puts `scimage-admin` on your
PATH), or run it in place with `go run ./cmd/scimage-admin`.

```bash
scimage-admin tenant create -name "Acme Corp"
# → prints the TENANT ID and the SCIM BASE URL

scimage-admin token issue -tenant <tenantID> -label "Entra prod"
# → prints the token ONCE. Copy it now; it is not stored anywhere.
```

The **Tenant URL** Entra needs is `https://<your-host>/scim/v2/<tenantID>` (set
`SCIM_BASE_URL` on the server so that value and the returned `Location` links are
correct). The **Secret Token** is the token from this command.

## 2. Configure provisioning in Entra

In the Entra admin center, open your enterprise application, then **Provisioning →
Get started**, set **Provisioning Mode** to **Automatic**, and under **Admin
Credentials**:

| Field | Value |
| --- | --- |
| Tenant URL | `https://<your-host>/scim/v2/<tenantID>` |
| Secret Token | the token from step 1 |

Click **Test Connection**. Entra reads `/ServiceProviderConfig`, then issues a
filtered `GET /Users?filter=userName eq "…"` to confirm reconciliation works,
both of which SCIMage supports. A green result means the credentials and the URL
are right.

## 3. Attribute mappings

SCIMage models a minimal, typed core: `userName`, `name.givenName`,
`name.familyName`, a primary `email`, `active`, and `externalId`. Entra ships a
large default `customappsso` mapping that includes many attributes this server
doesn't model as columns.

Two ways to handle the extras:

- **Trim the mapping** to the modeled core plus `externalId`. Anything left over
  is accepted and dropped rather than erroring, so an over-broad mapping won't
  break provisioning. It just won't persist.
- **Register the attributes you need** on the tenant and keep the mapping:

  ```bash
  scimage-admin attribute register -tenant <tenantID> -name displayName
  # for the enterprise extension, register the whole URN:
  scimage-admin attribute register -tenant <tenantID> \
    -name "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"
  ```

  `SCIM_EXTENDED_ATTRIBUTES=1` must be set on the server; registering and
  `/Schemas` advertising are covered in
  [CONFIGURATION.md](CONFIGURATION.md#-extensible-attributes).

> **Interop note.** Entra sometimes sends `emails[].primary` as the string
> `"true"` rather than a JSON boolean. SCIMage accepts either and always returns a
> real boolean, so this doesn't need any special mapping on the Entra side.

## 4. Deactivation

Entra deprovisions with `PATCH {"op":"replace","path":"active","value":false}`
(and reads the user back both by id and by filter, which are expected to agree).
SCIMage emits `user.deactivated` and soft-deletes: the row is kept as inactive,
so the audit trail keeps its subject.

## 5. Groups

If you assign groups to the application, Entra provisions them as SCIM Groups with
members. SCIMage validates each member against this tenant's users in the same
statement that writes it, so a stale or foreign member id rejects the mutation
rather than being dropped. Membership changes arrive as `PATCH` and emit
`group.replaced`.

## What Entra cannot use here

The discovery document declares these unsupported, so Entra won't rely on them:

- **Bulk, sorting and ETags** are not supported.
- **Filtering** is limited to `userName eq` / `externalId eq` for users and
  `displayName eq` for groups, the reconciliation lookups Entra actually sends.
  Other expressions return `invalidFilter`.

## Troubleshooting

- **Test Connection fails**: confirm the Tenant URL includes the
  `/scim/v2/<tenantID>` path, the Secret Token is the full `scimage_…` string, and
  the token's tenant matches the URL's tenant (a mismatch is a `401`).
- **Provisioning logs show attribute errors**: usually an over-broad mapping.
  Trim it to the core, or register the extra attributes and set
  `SCIM_EXTENDED_ATTRIBUTES=1`.
- **To see exactly what Entra sent**, set `SCIM_LOG_REQUESTS=1` on the server. The
  request-body log is written to a `0600` file since bodies carry user attributes.
