# Enabling Authentication

Athena starts with all auth off. This guide is the recipes; the reasoning and threat model live in [Security Model](../concepts/security-model.md).

## Recipe 1: API key (service gate)

```bash title="environment"
MEMORY_OS_REQUIRE_API_KEY=true
MEMORY_OS_API_KEY=$(openssl rand -hex 32)
```

Clients send the key on every request, either header form:

```bash
curl -H "X-API-Key: $KEY" http://athena:8080/api/v1/sessions ...
curl -H "Authorization: Bearer $KEY" http://athena:8080/api/v1/sessions ...
```

Missing or wrong key returns **401**. Store the key in your secret manager, inject it as an env var, and rotate by redeploying (there is no rotation API; overlap windows require running two replicas with different keys behind your ingress).

## Recipe 2: JWT (identity and tenancy)

```bash title="environment"
MEMORY_OS_REQUIRE_JWT=true
MEMORY_OS_JWT_SECRET=<shared HMAC secret>
```

Your platform's auth service mints short-lived tokens carrying the scope:

```json
{
  "tenant_id": "acme",
  "user_id": "user_123",
  "agent_id": "support-bot",
  "exp": 1791234567
}
```

Clients send `X-JWT-Token: <token>` (or `Authorization: JWT <token>`). With JWT enabled, **scope in request bodies is ignored**; the token is the identity. `tenant_id`, `user_id`, and `exp` are required; expired or tampered tokens return 401.

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

!!! danger "Multi-tenant deployments need JWT"
    An API key alone lets any caller name any tenant in the request body. If more than one tenant's data is in the deployment, enable JWT (or terminate identity at a trusted gateway in front of Athena).

## Recipe 3: mTLS (transport identity)

```bash title="environment"
MEMORY_OS_ENABLE_TLS=true
MEMORY_OS_REQUIRE_MTLS=true
# plus certificate, key, and CA bundle paths; see the Configuration Reference
```

The server presents its certificate and verifies client certificates against the CA. Connections without a valid client cert are rejected before any HTTP is parsed. Combine with JWT for per-request identity on top of the authenticated channel.

## Combining mechanisms

Enabled checks run in order (mTLS, then API key, then JWT) and all must pass:

| Posture | Enable |
|---|---|
| Local development | nothing (default) |
| Internal single tenant | API key |
| Multi-tenant platform | API key + JWT |
| Zero-trust network | mTLS + JWT |

`/health` and `/metrics` always bypass auth, so probes and Prometheus need no credentials.

## Verifying your setup

```bash
# should be 401 (no credentials)
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://athena:8080/api/v1/sessions -d '{}'

# should be 200 regardless of auth config
curl -s -o /dev/null -w "%{http_code}\n" http://athena:8080/health
```

Auth outcomes are logged; failed attempts appear in server logs with the mechanism that rejected them.

!!! note "Admin endpoints"
    There is no per-endpoint authorization: any authenticated caller can hit `POST /api/v1/admin/analytics/trigger`. Restrict admin paths at your ingress or network policy layer.
