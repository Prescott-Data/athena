# Running Graph Analytics

Community detection and bridge scoring run as a scheduled job outside the request path. This guide covers triggering, scheduling, and verifying it; what the job computes is in [Graph Analytics](../concepts/graph-analytics.md).

## The one rule

!!! danger "Always trigger externally"
    Pregel loads the entire graph into memory. Never wire analytics to an in-process ticker or goroutine; trigger it over HTTP from an external scheduler. This rule exists because the in-process variant was tried and reverted for OOM risk.

## Manual trigger

```bash
curl -X POST http://athena:8080/api/v1/admin/analytics/trigger
```

```json
{"success": true, "message": "Graph analytics job (Community Detection + Bridge Entities) triggered in background"}
```

The endpoint returns immediately; the job runs asynchronously for up to ~10 minutes on large graphs. Progress and completion are visible in server logs and metrics.

## Scheduling with Kubernetes

The recommended production pattern is a nightly CronJob in a low-traffic window:

```yaml title="analytics-cronjob.yaml"
apiVersion: batch/v1
kind: CronJob
metadata:
  name: athena-graph-analytics
spec:
  schedule: "0 3 * * *"          # daily, off-peak
  concurrencyPolicy: Forbid      # never two Pregel runs at once
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: trigger
              image: curlimages/curl:latest
              args:
                - -fsS
                - -X
                - POST
                - http://athena:8080/api/v1/admin/analytics/trigger
```

`concurrencyPolicy: Forbid` matters: overlapping Pregel runs multiply memory pressure on ArangoDB. Any scheduler that can POST works the same way (systemd timer, GitHub Actions, Cloud Scheduler).

!!! note "Protect the endpoint"
    Admin routes share the global auth policy; there is no separate admin authorization. If auth is enabled, the scheduler needs credentials (add the header to the job); either way, consider restricting `/api/v1/admin/*` at your ingress.

## Verifying a run

After the job completes:

```bash
go run cmd/verify_analytics/main.go
```

This inspects the LTM graph state: nodes now carry `community_id`, and connector nodes carry `is_bridge: true` with a `bridge_score`. In metrics, check:

```
memos_analytics_pregel_duration_seconds
memos_analytics_bridges_found_total
```

You can also query directly in the ArangoDB UI (database `athena_ltm`):

```aql
FOR n IN Concepts
  FILTER n.is_bridge == true
  SORT n.bridge_score DESC
  LIMIT 10
  RETURN {name: n.name, communities: n.bridge_score}
```

## Cadence guidance

Daily is right for most deployments: community structure changes on the timescale of days of accumulated promotions, not minutes. Run it more often only if downstream consumers act on `community_id` freshness, and never more often than the job's own runtime.

!!! note "Pregel availability"
    On ArangoDB builds without Pregel (3.12+ removed it), the job skips label propagation and proceeds with existing `community_id` values (maintained by an external job); bridge scoring still runs.
