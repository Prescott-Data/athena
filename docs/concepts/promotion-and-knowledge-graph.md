# Promotion & the Knowledge Graph

Promotion is where Athena stops remembering *conversations* and starts knowing *facts*. On a 30-minute ticker, the promoter scans active chains, recomputes their [heat](heat-and-decay.md), and for every chain at or above the promotion threshold, extracts structured knowledge into the `athena_ltm` graph in ArangoDB.

## Triple extraction

The chain's summary is sent to the LLM with a **strict JSON schema** (structured outputs: the model cannot return anything malformed; schema violations increment `memos_extractor_schema_failures_total` and abort that chain's promotion).

The schema enforces the ontology:

**Nodes**: exactly four labels:

| Label | Holds | Examples |
|---|---|---|
| `Identities` | Anything with agency or persona | `sangalo`, `prescott_data`, `athena` |
| `Tools` | Concrete software, hardware, languages | `go`, `arangodb`, `docker` |
| `Projects` | Scoped initiatives and repos | `nexus_protocol` |
| `Concepts` | Abstract ideas and methodologies | `spaced_repetition`, `authentication` |

Node IDs are lowercase `snake_case`, enforced by the schema.

**Edges**: a whitelisted relation vocabulary:

| Relation | Meaning |
|---|---|
| `USES` | Actively utilizing a tool |
| `WORKS_ON` | Building or developing something |
| `BUILT_FOR_CLIENT` | Developing on behalf of another identity |
| `STRUGGLES_WITH` | Difficulty with a tool/concept/project |
| `EXHIBITS` | Displays a personality trait or behavior |
| `EXPRESSED_INTEREST` | Curiosity or desire to learn |
| `RELATES_TO` | Generic fallback; **must** carry `context_nuance` |

Every edge carries `context_nuance` (a free-text qualifier) and a `confidence` in $[0,1]$ assigned by the LLM. Relations outside the whitelist are auto-corrected to `RELATES_TO` with the original intent preserved in the nuance.

## Idempotent writes: the UPSERT contract

Re-observing a fact must strengthen it, not duplicate it. Nodes UPSERT on `_key`:

```aql
UPSERT { _key: @key }
INSERT { _key: @key, name: @name, created_at: @now, last_seen: @now }
UPDATE { last_seen: @now }
IN Tools   // or Identities / Concepts / Projects
```

Edges UPSERT on the `(_from, _to, relation)` triple:

```aql
UPSERT { _from: @from, _to: @to, relation: @relation }
INSERT { ..., confidence: @confidence, weight: 1, heat_score: @heat, ... }
UPDATE {
  confidence: (OLD.confidence + @confidence) / 2,   // running average
  weight: OLD.weight + 1,                           // frequency counter
  context_nuance: @context_nuance,                  // overwritten
  heat_score: @heat, last_seen: @now
}
IN MemoryEdges
```

The semantics per field:

- **`weight`**: how many times this fact has been independently observed. The primary retrieval sort key.
- **`confidence`**: a simple running average of old and new. (Deliberately *not* an EMA; an EMA variant was reverted after skewing production data.)
- **`context_nuance`**: latest observation wins.
- **`heat_score`**: snapshot of the source chain's heat at promotion time.

## Retrieval

`SearchMemory` enriches its results by traversing the graph from entities matched in the query:

```aql
FOR v, e IN 1..2 ANY @startNode MemoryEdges
  FILTER e.confidence >= 0.5
  SORT e.weight DESC, e.heat_score DESC
  LIMIT 50
  RETURN DISTINCT { node: v, edge: e }
```

Three properties worth internalizing: low-confidence edges (< 0.5, hardcoded) are invisible to retrieval even though they exist in the graph; frequency beats recency (`weight` sorts before `heat_score`); and traversal depth is 1–2 hops, so Athena surfaces neighborhoods, not transitive closures.

## Scope and safety

Graph nodes and edges are tenant/user-scoped like everything else ([Sessions & Identity](sessions-and-identity.md)). Promotion is idempotent and crash-safe: a re-promoted chain just re-UPSERTs. The graph is never written synchronously from an API call; only the promoter and the [analytics job](graph-analytics.md) touch it.

!!! note "`athena_ltm` only"
    All graph writes target the `athena_ltm` database. If your ArangoDB instance hosts databases for other applications, Athena will never touch them; equally, nothing else should write into `athena_ltm`.
