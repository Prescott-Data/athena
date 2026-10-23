# Security Model

Athena holds the memory of many tenants in one deployment, so it must know who is calling and confine each caller to its own tenant. Authentication answers the first question; one authorisation layer answers the second, for every call on every port.

Athena **refuses to start without authentication**. Running open requires `MEMORY_OS_ALLOW_UNAUTHENTICATED=true`, which is for local development only: any caller can then read and write every tenant's memory.

## Credentials

| Mechanism | Identifies | Enable with | Sent as |
|---|---|---|---|
| **API key** | A tenant, or a trusted backend service | `MEMORY_OS_REQUIRE_API_KEY=true` and `MEMORY_OS_API_KEYS` | `X-API-Key: <key>` or `Authorization: Bearer <key>` |
| **JWT** | A tenant, user and optionally agent | `MEMORY_OS_REQUIRE_JWT=true` and `MEMORY_OS_JWT_SECRET` | `X-JWT-Token: <token>` or `Authorization: JWT <token>` |
| **mTLS** | The transport peer (HTTP only) | `MEMORY_OS_REQUIRE_MTLS=true` | Client certificate |

Every enabled mechanism must pass. mTLS secures the channel but names no tenant, so it is used together with an API key or JWT. Client certificates are verified by the HTTP server; when mTLS is required, the gRPC port listens on loopback only (for the REST gateway), so it cannot be used to skip the certificate check.

Tenant, user and agent IDs may not contain quotes, backslashes or control characters (`CreateSession` returns `InvalidArgument`): they are used inside vector-store filter expressions.

### API keys: tenant keys and service keys

`MEMORY_OS_API_KEYS` lists the accepted keys and what each may act for:

```bash
MEMORY_OS_API_KEYS="k_acme_9f2…=acme,k_globex_41c…=globex,k_backend_77e…=*"
```

- A **tenant key** (`key=acme`) is confined to that tenant. Sessions it creates belong to `acme` whatever the request body says, and other tenants' sessions do not exist for it.
- A **service key** (`key=*`) belongs to a trusted backend, such as an agent framework that serves many tenants, which names the tenant in each request. Give service keys only to services you would trust with every tenant's memory.

`MEMORY_OS_API_KEY` (a single key, from earlier releases) is still read and treated as a service key. There is no default key.

### JWT: identity from the token

Tokens are verified with `MEMORY_OS_JWT_SECRET` (HMAC; there is no default, and the placeholder `your-secret-key` of earlier releases is refused) and must carry:

| Claim | Required | Confines the caller to |
|---|---|---|
| `tenant_id` | yes | That tenant |
| `user_id` | yes | That user's sessions |
| `agent_id` | no | That agent's sessions |
| `exp` | recommended | Expired tokens are rejected |

With an API key and a JWT together, the token decides the identity, and a tenant key's tenant must equal the token's.

## Authorisation

Every call (REST, which the gateway forwards to gRPC with the credentials, and direct gRPC on port 9090) passes one interceptor that applies these rules:

| Call | Tenant key or JWT | Service key |
|---|---|---|
| `CreateSession` | Tenant, and user/agent if the token names them, come from the credentials; a body naming anyone else is refused (`PermissionDenied`) | Identity from the request |
| Calls on a session (`/sessions/{id}/…`) | Allowed only if the session's tenant (and user/agent, when the token names them) match; otherwise `NotFound`, so other tenants' session IDs cannot be probed | Allowed |
| `TriggerGraphAnalytics` | `PermissionDenied` | Allowed |
| `/health` (liveness) | No credentials needed | No credentials needed |
| `HealthCheck` (`/api/v1/health`) | Allowed: any valid credentials (its errors can name internal hosts) | Allowed |
| `/metrics` (aggregated across tenants) | `403` | Allowed; `MEMORY_OS_METRICS_PUBLIC=true` opens it to anyone |

## Data-layer isolation

Within a tenant, memory is scoped by user and agent ([Sessions & Identity](sessions-and-identity.md)):

- **STM** (Redis), **events and chains** (MongoDB) and **vectors** (Milvus) are keyed and queried by tenant, user and agent.
- **Chain merging and archival** only consider chains of the same tenant and user.
- **The knowledge graph** (ArangoDB) is per tenant and user: node keys carry a hashed scope prefix and every node and edge records its tenant and user, so the same entity in two tenants is two nodes, and searches and traversals never leave the caller's graph. Each node and edge also records the chains it was extracted from, so erasing memory removes the graph knowledge no remaining chain supports. Nodes written by releases before this scoping carry no tenant and are never returned; they still hold extracted knowledge, so remove them after upgrading with `go run ./cmd/ltm-purge-unscoped` (`-dry-run` counts them first).

## Recommended postures

| Environment | Posture |
|---|---|
| Local development | `MEMORY_OS_ALLOW_UNAUTHENTICATED=true`, or one service key |
| Backend serving many tenants (e.g. an agent framework) | A service key held only by that backend, on a private network |
| Tenants or end users calling Athena directly | Tenant keys, or JWTs minted by your auth service |
| Regulated / zero-trust | mTLS plus JWT |

## Not covered

- **Key rotation** is by configuration: add the new key to `MEMORY_OS_API_KEYS`, move clients, then remove the old one.
- **Encryption at rest** belongs to the datastores (MongoDB, ArangoDB, Milvus, Redis, blob store).
