# Graph Analytics

Once the knowledge graph accumulates structure, Athena mines it: which entities cluster together, and which entities *connect* clusters. Both run as one nightly job, external to the request path.

## Community detection

The job runs ArangoDB **Pregel Label Propagation** over the whole graph:

```
algorithm:  labelpropagation
vertices:   Identities, Concepts, Tools, Projects
edges:      MemoryEdges
maxGSS:     50          (max global supersteps)
result:     community_id written onto every vertex (store: true)
```

Label propagation converges on densely connected neighborhoods: a user's "Go backend work" entities end up in one community, their "home automation hobby" in another. The job is polled every 2 seconds through states `loading → running → storing → done`, with runtime, vertex, and edge counts recorded as metrics.

## Bridge entities

After communities are assigned, an AQL pass finds the connectors:

```aql
FOR node IN Tools  // repeated per vertex collection
  FILTER node.community_id != null
  LET connected = (
    FOR v IN 1..1 ANY node MemoryEdges
      FILTER v.community_id != null AND v.community_id != node.community_id
      RETURN DISTINCT v.community_id
  )
  LET score = LENGTH(connected)
  FILTER score > 0
  UPDATE node WITH { is_bridge: true, bridge_score: score }
```

A node's `bridge_score` is the number of *distinct foreign communities* it touches. High-bridge entities are the load-bearing concepts of a user's world: `docker` bridging a work community and a hobby community says something no single chain ever recorded.

## Execution model

!!! danger "Never run analytics in-process"
    Pregel loads the **entire graph into memory**. An in-process scheduler goroutine was added once and reverted after OOM risk analysis; the rule is now a hard invariant. Trigger analytics only via the admin endpoint from an external scheduler.

Production runs a Kubernetes CronJob daily at **03:00 UTC**:

```
POST /api/v1/admin/analytics/trigger
```

The endpoint returns immediately (`{"success": true, "message": "...triggered in background"}`) and the job runs asynchronously, up to ~10 minutes on large graphs. Operational setup, verification, and manual runs: [Running Graph Analytics](../guides/running-graph-analytics.md).

!!! note "Pregel availability fallback"
    On ArangoDB deployments where Pregel is unavailable (3.12+ removed it), the job proceeds against pre-computed `community_id` values maintained by an external job; bridge scoring works unchanged.

## What consumes this

`community_id` and `bridge_score` live on the graph nodes and flow into [`SearchMemory`](../guides/semantic-search.md) enrichment; they're also directly queryable by any downstream analytics reading `athena_ltm`. Inspect current state with `go run cmd/verify_analytics/main.go` ([Debug Tools](../guides/debug-tools.md)).
