# Semantic Search

`SearchMemory` answers questions against everything a user's agents have ever discussed: a vector search over cognitive chains, enriched with facts from the knowledge graph.

## The call

```bash
curl -X POST http://localhost:8080/api/v1/sessions/$SESSION/context/search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "what deployment platform does the user prefer?",
    "limit": 5
  }'
```

| Field | Required | Meaning |
|---|---|---|
| `query` | yes | Natural-language question or phrase; empty returns `InvalidArgument` |
| `limit` | no | Max results |
| `similarity_threshold` | no | Minimum cosine similarity for chain results (for example `0.5`); unset returns all |
| `filter` | no | Metadata filter map |

## What it searches

1. The query is embedded with the configured [embedding model](llm-providers.md).
2. The user's active **cognitive chains** (filtered by `filter` first) are scored exactly against the query: their vectors are read from Milvus and compared by cosine similarity (−1 to 1, higher is closer). Results are ordered most similar first, and chains below `similarity_threshold` are dropped. Embeddings are normalised to unit length, so scores mean the same whichever embedding provider is configured.
3. Entities in the top chains seed a **graph traversal**: 1–2 hops through `MemoryEdges`, `confidence >= 0.5`, sorted by `weight` then `heat_score`, enriching results with related facts ([details](../concepts/promotion-and-knowledge-graph.md#retrieval)).

Results carry the chain summary, topic, similarity score, and any graph enrichment.

## Scoping: user-wide by design

Search is scoped to the session's **user**, across *all* their agents. What the user told the support agent is findable by the sales agent (within the same tenant). This is the deliberate contrast with `GetContext`, whose STM window is agent-scoped; see [Sessions & Identity](../concepts/sessions-and-identity.md).

## What search cannot see

- **The live STM window.** Very recent turns may not have formed chains yet; the window belongs to `GetContext`. A complete "memory read" is `GetContext` + `SearchMemory`.
- **Archived chains.** Their vectors are deleted at archival ([lifecycle](../concepts/archival-and-lifecycle.md)).
- **Chains that failed the quality gate.** Small talk never becomes searchable memory.

## Searching well

- **Ask in sentences.** Embeddings match meaning; "what programming language does the user like?" beats "language".
- **Expect the gist, not the transcript.** Results are chain summaries. If you need verbatim history, keep your own log; Athena's MTM is deliberately compressed.
- **Search warms memory.** Returned chains have their heat recalled, extending their lifetime and hardening their [recall strength](../concepts/heat-and-decay.md).

## Latency envelope

One embedding call (network round-trip to your LLM provider) plus a Milvus ANN query plus an optional AQL traversal. The embedding dominates; budget accordingly and cache repeated queries client-side if you fan out searches per turn.
