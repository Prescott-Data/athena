# Enabling Authentication

Athena will not start without authentication unless `MEMORY_OS_ALLOW_UNAUTHENTICATED=true` is set (local development only). This guide is the recipes; the reasoning and the authorisation rules are in [Security Model](../concepts/security-model.md).

## Recipe 1: a backend that serves many tenants

An agent framework or platform backend that stores memory for many tenants holds one **service key** and names the tenant in each request:

```bash title="environment"
MEMORY_OS_REQUIRE_API_KEY=true
MEMORY_OS_API_KEYS="$(openssl rand -hex 32)=*"
```

```bash
curl -H "X-API-Key: $KEY" -X POST http://athena:8080/api/v1/sessions \
  -d '{"tenant_id": "acme", "user_id": "user_123", "agent_id": "support-bot"}'
```

A service key can act for every tenant. Keep it in the backend's secret store and keep Athena on a private network.

## Recipe 2: one key per tenant

When tenants (or per-tenant services) call Athena themselves, give each a **tenant key**:

```bash title="environment"
MEMORY_OS_REQUIRE_API_KEY=true
MEMORY_OS_API_KEYS="<acme key>=acme,<globex key>=globex"
```

A tenant key creates sessions in its own tenant (the body's `tenant_id` may be omitted; a different one is refused with 403) and sees only its tenant's sessions (others return 404).

## Recipe 3: JWT for per-user identity

```bash title="environment"
MEMORY_OS_REQUIRE_JWT=true
MEMORY_OS_JWT_SECRET=<shared HMAC secret>
```

Your auth service mints short-lived tokens carrying the scope:

```json
{
  "tenant_id": "acme",
  "user_id": "user_123",
  "agent_id": "support-bot",
  "exp": 1791234567
}
```

Clients send `X-JWT-Token: <token>` (or `Authorization: JWT <token>`). The token is the identity: `CreateSession` takes tenant, user and agent from it and refuses a body naming anyone else, and calls on a session succeed only for that session's tenant, user and (if the token names one) agent.

Minting example (Go):

```go
token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
    "tenant_id": "acme",
    "user_id":   userID,
    "agent_id":  agentID,
    "exp":       time.Now().Add(15 * time.Minute).Unix(),
})
signed, err := token.SignedString([]byte(os.Getenv("MEMORY_OS_JWT_SECRET")))
```

API key and JWT can be required together; the token then decides identity, and a tenant key must match the token's tenant.

## Recipe 4: mTLS (transport identity)

```bash title="environment"
MEMORY_OS_ENABLE_TLS=true
MEMORY_OS_REQUIRE_MTLS=true
# plus certificate, key, and CA bundle paths; see the Configuration Reference
```

mTLS authenticates the HTTP channel but names no tenant: combine it with JWT or API keys. With mTLS required, the gRPC port is bound to loopback and external clients use the REST API.

## gRPC clients

Direct gRPC calls (port 9090) are authenticated and authorised exactly like REST (unless mTLS is required, which keeps gRPC on loopback). Send the credentials as metadata: `x-api-key`, `x-jwt-token`, or `authorization`.

## Verifying your setup

```bash
# 401: no credentials
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://athena:8080/api/v1/sessions -d '{}'

# 404: a tenant key reading another tenant's session
curl -s -o /dev/null -w "%{http_code}\n" -H "X-API-Key: $ACME_KEY" http://athena:8080/api/v1/sessions/$GLOBEX_SESSION/context

# 200 regardless of auth config
curl -s -o /dev/null -w "%{http_code}\n" http://athena:8080/health
```

Denied calls are logged with the method and the caller's tenant and user.
