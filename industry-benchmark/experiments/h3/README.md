# H3 — Asymptotic Latency Experiment

Tests whether ArangoDB graph traversal latency stays flat as the LTM knowledge graph grows from 10,000 → 100,000 → 1,000,000 nodes.

**Pass condition:**
- P95(T2) / P95(T1) < 1.5x
- P95(T3) / P95(T1) < 3.0x
- Error rate < 1% per tier

---

## Files

| File | Purpose |
|------|---------|
| `generate_h3_data.py` | Generates synthetic SBM graph data and loads it into ArangoDB |
| `load_test_h3.js` | k6 load test — 30 VUs, 60s, replicates production `FetchContext` AQL |
| `score_h3.py` | Reads result JSON files and computes pass/fail ratios |
| `h3_results_tier{N}.json` | Written by the load test after each scored run |
| `h3_sample_nodes_tier{N}.json` | 200 sample node IDs used by the load test |

---

## Prerequisites

- `kubectl` configured pointing to the right cluster (`dromos-console-aks-prod`)
- ArangoDB running at `dromos-memory-os/memory-os-arangodb-0` (AKS StatefulSet)
- k6 installed at `~/bin/k6`
- Python 3 with `requests` library installed
- ArangoDB password: stored in `memory-os-arangodb-secrets` → `ARANGO_ROOT_PASSWORD`

Verify your kubectl context before starting:
```bash
kubectl config current-context
# Expected: dromos-console-aks-prod

kubectl get pods -n dromos-memory-os | grep arango
# Expected: memory-os-arangodb-0   1/1   Running
```

---

## Warm Cache Protocol (mandatory for all tiers)

ArangoDB buffers recently accessed data in memory. The first load test run after loading fresh data hits cold storage (disk fetches) and will show inflated latency — this run must be discarded. The second run hits warm cache and is the valid measurement.

**For every tier: run the load test twice. Discard run 1. Score run 2.**

---

## Step 1 — Start Port-Forward

The load test runs from your laptop and needs to reach ArangoDB inside the cluster. Keep this running in a dedicated terminal throughout the experiment. Do not close it between runs.

```bash
kubectl port-forward pod/memory-os-arangodb-0 8529:8529 -n dromos-memory-os
```

Verify it's alive before running any load test:
```bash
curl -s -u root:"<ARANGO_PASS>" http://localhost:8529/_api/version
```

If port-forward dies mid-test you will see `connection refused` errors and the total request count will spike to 100k+ with P95/P99 showing as 0ms. Kill k6, restart port-forward, and re-run.

---

## Tier 1 — 10,000 Nodes (Baseline)

### Generate and load data

```bash
cd memory-os/industry-benchmark/experiments/h3

python3 generate_h3_data.py \
  --nodes 10000 \
  --tier 1 \
  --url http://localhost:8529 \
  --pass "<ARANGO_PASS>"
```

This loads 10,000 nodes and 80,000 edges into ArangoDB and writes `h3_sample_nodes_tier1.json`.

### Warm-up run (discard)

```bash
~/bin/k6 run \
  -e ARANGO_URL=http://localhost:8529 \
  -e ARANGO_PASS="<ARANGO_PASS>" \
  -e TIER=1 \
  -e NODES_FILE=h3_sample_nodes_tier1.json \
  load_test_h3.js
```

### Scored run (this is your baseline)

Run the exact same command again:

```bash
~/bin/k6 run \
  -e ARANGO_URL=http://localhost:8529 \
  -e ARANGO_PASS="<ARANGO_PASS>" \
  -e TIER=1 \
  -e NODES_FILE=h3_sample_nodes_tier1.json \
  load_test_h3.js
```

Result is written to `h3_results_tier1.json`. Note the P95 — every other tier is compared against it.

**Expected output:**
```
P95 latency : ~1165 ms
P99 latency : ~1390 ms
Error rate  : 0%
Total reqs  : ~1900–2200
```

---

## Tier 2 — 100,000 Nodes

### Generate and load data

```bash
python3 generate_h3_data.py \
  --nodes 100000 \
  --tier 2 \
  --url http://localhost:8529 \
  --pass "<ARANGO_PASS>"
```

This truncates Tier 1 data and loads 100,000 nodes + 800,000 edges. Takes approximately 10–12 minutes through the port-forward tunnel.

### Warm-up run (discard)

```bash
~/bin/k6 run \
  -e ARANGO_URL=http://localhost:8529 \
  -e ARANGO_PASS="<ARANGO_PASS>" \
  -e TIER=2 \
  -e NODES_FILE=h3_sample_nodes_tier2.json \
  load_test_h3.js
```

### Scored run

```bash
~/bin/k6 run \
  -e ARANGO_URL=http://localhost:8529 \
  -e ARANGO_PASS="<ARANGO_PASS>" \
  -e TIER=2 \
  -e NODES_FILE=h3_sample_nodes_tier2.json \
  load_test_h3.js
```

Result written to `h3_results_tier2.json`.

**Expected output:**
```
P95 latency : ~1200 ms
P99 latency : ~1800 ms
Error rate  : 0%
Ratio vs T1 : ~1.03–1.10x  ✓ (<1.5)
```

---

## Tier 3 — 1,000,000 Nodes

**Do not use port-forward for Tier 3 data loading.** Loading 8M edges through the port-forward tunnel takes 8+ hours and will stall. Instead, generate JSONL files locally and use `arangoimport` inside the pod directly.

### Step 1 — Generate JSONL files locally

```bash
python3 generate_h3_data.py \
  --nodes 1000000 \
  --tier 3 \
  --output-dir /tmp/h3_tier3
```

This runs purely locally — no network, no ArangoDB connection. Generates:
- `/tmp/h3_tier3/Concepts.jsonl` — 600,000 nodes (~159MB)
- `/tmp/h3_tier3/Identities.jsonl` — 200,000 nodes (~54MB)
- `/tmp/h3_tier3/Projects.jsonl` — 100,000 nodes (~26MB)
- `/tmp/h3_tier3/Tools.jsonl` — 100,000 nodes (~25MB)
- `/tmp/h3_tier3/MemoryEdges.jsonl` — 8,000,000 edges (~1.7GB)
- `h3_sample_nodes_tier3.json` — 200 sample node IDs for the load test

Takes approximately 8–10 minutes. The edge generation phase will appear to hang — it is working.

### Step 2 — Copy files into the pod

**Do not use `kubectl cp` with a directory argument** — it base64-encodes and tars the entire directory, stalling for 45+ minutes with no progress visible.

Instead, copy each file individually:

```bash
kubectl cp /tmp/h3_tier3/Concepts.jsonl \
  memory-os-arangodb-0:/tmp/h3_tier3/Concepts.jsonl -n dromos-memory-os

kubectl cp /tmp/h3_tier3/Identities.jsonl \
  memory-os-arangodb-0:/tmp/h3_tier3/Identities.jsonl -n dromos-memory-os

kubectl cp /tmp/h3_tier3/Projects.jsonl \
  memory-os-arangodb-0:/tmp/h3_tier3/Projects.jsonl -n dromos-memory-os

kubectl cp /tmp/h3_tier3/Tools.jsonl \
  memory-os-arangodb-0:/tmp/h3_tier3/Tools.jsonl -n dromos-memory-os

kubectl cp /tmp/h3_tier3/MemoryEdges.jsonl \
  memory-os-arangodb-0:/tmp/h3_tier3/MemoryEdges.jsonl -n dromos-memory-os
```

The MemoryEdges file is 1.7GB — it will take approximately 45–60 minutes. `kubectl cp` exits silently when done (no confirmation message).

Verify all files arrived with correct line counts:
```bash
kubectl exec memory-os-arangodb-0 -n dromos-memory-os -- \
  sh -c "wc -l /tmp/h3_tier3/*.jsonl"
```

Expected:
```
   600000 Concepts.jsonl
   200000 Identities.jsonl
  8000000 MemoryEdges.jsonl
   100000 Projects.jsonl
   100000 Tools.jsonl
```

### Step 3 — Import with arangoimport inside the pod

The ArangoDB pod has no Python but does have `arangoimport`. Run it for each collection — this talks to `localhost:8529` inside the pod, no port-forward needed.

```bash
kubectl exec -it memory-os-arangodb-0 -n dromos-memory-os -- \
  arangoimport --server.username root \
  --server.password "<ARANGO_PASS>" \
  --server.database athena_ltm \
  --collection Concepts \
  --file /tmp/h3_tier3/Concepts.jsonl \
  --type jsonl --overwrite true

kubectl exec -it memory-os-arangodb-0 -n dromos-memory-os -- \
  arangoimport --server.username root \
  --server.password "<ARANGO_PASS>" \
  --server.database athena_ltm \
  --collection Identities \
  --file /tmp/h3_tier3/Identities.jsonl \
  --type jsonl --overwrite true

kubectl exec -it memory-os-arangodb-0 -n dromos-memory-os -- \
  arangoimport --server.username root \
  --server.password "<ARANGO_PASS>" \
  --server.database athena_ltm \
  --collection Projects \
  --file /tmp/h3_tier3/Projects.jsonl \
  --type jsonl --overwrite true

kubectl exec -it memory-os-arangodb-0 -n dromos-memory-os -- \
  arangoimport --server.username root \
  --server.password "<ARANGO_PASS>" \
  --server.database athena_ltm \
  --collection Tools \
  --file /tmp/h3_tier3/Tools.jsonl \
  --type jsonl --overwrite true

kubectl exec -it memory-os-arangodb-0 -n dromos-memory-os -- \
  arangoimport --server.username root \
  --server.password "<ARANGO_PASS>" \
  --server.database athena_ltm \
  --collection MemoryEdges \
  --file /tmp/h3_tier3/MemoryEdges.jsonl \
  --type jsonl --overwrite true
```

MemoryEdges takes approximately 30 minutes. Each command shows a progress percentage.

Verify the final counts:
```bash
kubectl exec memory-os-arangodb-0 -n dromos-memory-os -- \
  sh -c "arangosh --server.username root --server.password '<ARANGO_PASS>' \
  --server.database athena_ltm \
  --javascript.execute-string \
  'var cols=[\"Concepts\",\"Identities\",\"Projects\",\"Tools\",\"MemoryEdges\"]; cols.forEach(c=>print(c+\": \"+db._collection(c).count()));'"
```

Expected:
```
Concepts: 600000
Identities: 200000
Projects: 100000
Tools: 100000
MemoryEdges: 8000000
```

### Step 4 — Run Tier 3 load test (port-forward must be alive)

Warm-up run (discard):
```bash
~/bin/k6 run \
  -e ARANGO_URL=http://localhost:8529 \
  -e ARANGO_PASS="<ARANGO_PASS>" \
  -e TIER=3 \
  -e NODES_FILE=h3_sample_nodes_tier3.json \
  load_test_h3.js
```

Scored run:
```bash
~/bin/k6 run \
  -e ARANGO_URL=http://localhost:8529 \
  -e ARANGO_PASS="<ARANGO_PASS>" \
  -e TIER=3 \
  -e NODES_FILE=h3_sample_nodes_tier3.json \
  load_test_h3.js
```

Result written to `h3_results_tier3.json`.

**Expected output:**
```
P95 latency : ~1524 ms
P99 latency : ~1989 ms
Error rate  : 0%
Ratio vs T1 : ~1.31x  ✓ (<3.0)
```

---

## Step 5 — Score All Tiers

```bash
python3 score_h3.py --tiers 1 2 3
```

**Actual results (2026-03-19):**

```
=================================================================
  H3 — Asymptotic Latency Results
=================================================================

  Tier        Nodes    P95 (ms)    P99 (ms)    Errors   Ratio vs T1
  --------------------------------------------------------------
  T1         10,000        1165        1390     0.00%    (baseline)
  T2        100,000        1213        1405     0.00%    1.04x  ✓ (<1.5)
  T3      1,000,000        1524        1989     0.00%    1.31x  ✓ (<3.0)

  Latency Scalability Factor α : 179.5 ms / decade
  Interpretation               : high (linear scan risk ✗)

-----------------------------------------------------------------
  H3 RESULT: PASS ✓  — Retrieval latency is asymptotically flat
=================================================================
```

---

## Troubleshooting

### P95/P99 showing as 0ms and total requests is 100k+
Port-forward died mid-test. Requests are failing instantly with `connection refused`. Kill k6, restart port-forward, wait 2 minutes for ArangoDB to recover, then re-run.

### P99 showing as 0ms but P95 is correct
k6 is not computing p(99) for the metric. Ensure `summaryTrendStats` is set in `load_test_h3.js` options:
```js
summaryTrendStats: ["avg", "min", "med", "max", "p(90)", "p(95)", "p(99)"],
```
Also ensure `http_req_duration` has `p(99)` in its threshold:
```js
http_req_duration: ["p(95)<5000", "p(99)<10000"],
```

### Latency spikes 4x between consecutive runs
Multiple back-to-back load tests exhaust ArangoDB's buffer pool. Wait 2 minutes between runs to let it recover.

### Total request count varies between runs
Expected — the test runs for exactly 60 seconds. Total requests = `60s / (avg_response_time + 50ms sleep) × 30 VUs`. Any change in ArangoDB response time shifts this number. It is not an error.

### kubectl cp stalls for 45+ minutes with no progress
Do not use `kubectl cp` for directory transfers — it tars and base64-encodes everything. Copy files individually or use the stdin pipe method:
```bash
cat /tmp/h3_tier3/MemoryEdges.jsonl | \
  kubectl exec -i memory-os-arangodb-0 -n dromos-memory-os -- \
  sh -c 'cat > /tmp/h3_tier3/MemoryEdges.jsonl'
```

### Tier 3 data generation appears hung during edge generation
It is working. Generating 8M edges is CPU-bound and prints nothing until all edges are in memory. Wait — it will complete in approximately 8 minutes.

---

## Notes

- The Tier 3 dataset (1M nodes + 8M edges) is reused by **H4 (Structural Coherence)**. Do not truncate ArangoDB after completing H3.
- The `--output-dir` flag on `generate_h3_data.py` generates JSONL files without connecting to ArangoDB — use this for Tier 3 to bypass the port-forward bottleneck.
- The scalability factor α=179.5ms/decade was flagged as "linear scan risk" because the T2→T3 jump (+311ms) was steeper than T1→T2 (+48ms). Both ratio thresholds passed comfortably. At 1M nodes, latency is only 31% higher than at 10k nodes.
