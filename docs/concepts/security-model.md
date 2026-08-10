# Security Model

Athena ships with **all authentication disabled**: a deliberate local-development default that must be flipped before any real deployment. Three independent mechanisms can be enabled in any combination; each solves a different problem.

## The three mechanisms

| Mechanism | Proves | Enable with | Credential |
|---|---|---|---|
| **API key** | The caller is a trusted service | `MEMORY_OS_REQUIRE_API_KEY=true` | `X-API-Key: <key>` or `Authorization: Bearer <key>` |
| **JWT** | *Who* the request acts for | `MEMORY_OS_REQUIRE_JWT=true` | `X-JWT-Token: <token>` or `Authorization: JWT <token>` |
| **mTLS** | The transport peer's identity | `MEMORY_OS_REQUIRE_MTLS=true` | Client certificate signed by the configured CA |

Checks run in order (mTLS at connection time, then API key, then JWT) and every **enabled** check must pass. `/health` and `/metrics` always bypass auth (liveness probes and scrapers need no credentials).

### API key

A single shared secret (`MEMORY_OS_API_KEY`) compared against the header. It authenticates *services*, not users: it says "this caller is allowed to talk to Athena" and nothing about tenancy.

### JWT: where identity comes from

Tokens are validated against `MEMORY_OS_JWT_SECRET` (HMAC) and must carry:

| Claim | Required | Becomes |
|---|---|---|
| `tenant_id` | yes | The request's tenant scope |
| `user_id` | yes | The request's user scope |
| `agent_id` | optional | Agent scope (may also come from the request) |
| `exp` | yes | Standard expiry |

**With JWT enabled, scope fields in request bodies are ignored.** `tenant_id`/`user_id` always resolve from the token, so a compromised client cannot read or write another tenant's memory by editing a payload. This is the mechanism behind the isolation guarantees in [Sessions & Identity](sessions-and-identity.md).

### mTLS

For zero-trust networks: the server verifies client certificates against a CA bundle, rejecting unauthenticated transport before HTTP is even parsed. Certificate/key/CA paths are configured alongside `MEMORY_OS_ENABLE_TLS`; see the [Configuration Reference](../reference/configuration.md).

## Recommended postures

| Environment | Posture |
|---|---|
| Local dev | Everything off (the default) |
| Single-tenant internal | API key |
| Multi-tenant platform | API key **+ JWT**: key gates the door, token scopes the data |
| Regulated / zero-trust | mTLS + JWT |

!!! danger "JWT is the only tenant boundary"
    API keys alone leave scope resolution to request bodies: any caller with the key can name any tenant. If more than one tenant's data lives in your deployment, JWT (or an equivalently trusted upstream gateway injecting identity) is mandatory, not optional.

## What auth does not cover

- **No per-endpoint authorization.** Any authenticated caller can call anything, including `POST /api/v1/admin/analytics/trigger`. Restrict admin routes at your ingress/network layer.
- **No key rotation machinery.** The API key is one static value; rotate by redeploying.
- **Encryption at rest** belongs to the datastores (MongoDB/ArangoDB/Milvus/Redis/blob store), not Athena.

## Data-layer isolation

Auth resolves identity; isolation is then enforced structurally. Every Redis key, Mongo document, Milvus vector, and ArangoDB node/edge is written and queried with its `(tenant, user, agent)` scope ([Sessions & Identity](sessions-and-identity.md)). There is no code path that queries across tenants.
