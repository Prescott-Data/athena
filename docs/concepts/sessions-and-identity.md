# Sessions & Identity

Everything in Athena is scoped by a three-level identity hierarchy. Understanding it is a prerequisite for every other page.

## The hierarchy

```
tenant_id  →  user_id  →  agent_id
```

| Level | Meaning | Example |
|---|---|---|
| `tenant_id` | The isolation boundary: a customer, organization, or platform | `tenant_acme` |
| `user_id` | A human (or principal) within the tenant | `user_123` |
| `agent_id` | One of possibly many agents serving that user | `support-bot` |

The hierarchy is embedded in every artifact Athena creates:

- Redis STM keys: `stm:{tenantId}:{userId}:{agentId}`
- Task queues: `memory_processing_queue:v1:{tenantId}:{userId}:{agentId}`
- MongoDB documents, Milvus vectors, and ArangoDB nodes/edges all carry the scope fields and every query filters on them

## Scoping rules by tier

**STM is agent-scoped.** Each agent has its own window, so two agents serving the same user don't pollute each other's working memory.

**MTM and LTM are user-scoped.** Cognitive chains and graph knowledge belong to the *user*, across agents. `SearchMemory` deliberately searches all of a user's chains regardless of which agent created them: what a user told the support agent is retrievable by the sales agent, inside the same tenant.

!!! danger "Tenant is the hard wall"
    Nothing crosses `tenant_id`: not searches, not analytics, not graph traversals. User-scoping shares memory *within* a tenant only.

## Sessions

A **session** is a handle for a sequence of interactions, created with `CreateSession`:

```bash
POST /api/v1/sessions
{"tenant_id": "acme", "user_id": "user_123", "agent_id": "support-bot", "metadata": {...}}
```

The returned `session_id` goes into the path of every subsequent call (`/api/v1/sessions/{session_id}/...`). Sessions resolve to their `(tenant, user, agent)` scope on every request; the memory itself is keyed by scope, not by session, so a new session with the same identity picks up exactly where the old one left off. That is the point: **sessions are ephemeral, memory is not.**

Sessions are stored in MongoDB with their metadata. `GetSession` and `DeleteSession` exist in the API surface but are not yet implemented <span class="nx-badge nx-badge-beta">Planned</span>; see the [API Reference](../reference/api.md).

## Where identity comes from

With authentication disabled (the default), the identity in the request body is trusted as-is. That is acceptable only in development.

With **JWT auth enabled**, `tenant_id` and `user_id` are taken from token claims and the values in request bodies are ignored. A client cannot claim someone else's scope by editing a payload. `agent_id` may come from an optional claim or the request. With **API-key auth**, the key authenticates the caller but identity still comes from the request, so pair it with JWT when tenant isolation matters. Precedence and configuration: [Security Model](security-model.md).

## Choosing your granularity

- **One `agent_id` per distinct assistant persona.** Don't reuse one agent ID for functionally different bots; their STM windows will interleave.
- **Use metadata, not identity, for request-level context** (conversation channel, locale, app version).
- **Multi-agent workflows** that share one logical task should share `workflow_id`/`execution_id` in event metadata; the pipeline uses these for [coalescing](memory-tiers.md#stm-short-term-memory) and to avoid false topic breaks between steps of the same execution.
