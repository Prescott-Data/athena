# Debug Tools

The `cmd/` folder ships six binaries. One is the server; the other five exist to answer "what is the pipeline actually doing?" without attaching a debugger.

| Binary | Question it answers | Needs |
|---|---|---|
| `memory-server` | (the server itself) | Full environment |
| `init-ltm` | Schema setup, not debugging | `ARANGODB_*` vars |
| `verifydb` | What chains exist in MongoDB right now? | Local MongoDB |
| `verify` | Did the whole pipeline run end to end? | Local MongoDB + ArangoDB |
| `verify_analytics` | What state is the LTM graph in? | `ARANGODB_*` vars |
| `simulate` | Does the pipeline behave correctly under scripted conversations? | Running server + MongoDB |
| `test-gemini` | Is the LLM provider reachable and working? | `LLM_*` vars |

## `verifydb`: inspect cognitive chains

```bash
go run cmd/verifydb/main.go
```

Dumps `cognitive_chains` documents: topics, summaries, heat scores, statuses, event counts. The fastest way to see whether your conversations are forming the chains you expect.

!!! note
    Connects to `localhost` MongoDB with the local-stack credentials (hardcoded). A local-dev tool, not a production client.

## `verify`: end-to-end pipeline check

```bash
go run cmd/verify/main.go
```

Cross-checks the pipeline's downstream effects: archived-chain counts in MongoDB and knowledge present in the ArangoDB graph. Use it after an end-to-end test run to confirm every stage fired. Same localhost assumption as `verifydb`.

## `verify_analytics`: graph state

```bash
go run cmd/verify_analytics/main.go
```

Reports on the LTM graph: node counts per collection, `community_id` assignments, bridge entities. Run it after [analytics](running-graph-analytics.md) to confirm the job produced results.

## `simulate`: scripted end-to-end scenarios

```bash
go run cmd/simulate/main.go
```

Drives a running server through ~15 scripted conversation scenarios (topic shifts, returns to earlier topics, automation floods) and checks the resulting state. The closest thing to an integration test you can point at any environment; useful after tuning [pipeline thresholds](workers-and-schedulers.md) to see the behavioral effect.

## `test-gemini`: provider connectivity

```bash
go run cmd/test-gemini/main.go
```

Exercises the configured LLM provider (completions and embeddings) and prints results. First stop when chains stop forming and you suspect the [provider](llm-providers.md), and the quickest check that a new API key works.

## `init-ltm`: schema initialization

```bash
go run cmd/init-ltm/main.go
```

Creates the `athena_ltm` database, its vertex collections, `MemoryEdges`, and indexes. Idempotent; run once per environment and again after ArangoDB rebuilds. Listed here for completeness; it belongs to [deployment](deploying-athena.md), not debugging.

## A debugging session, end to end

Chains are not appearing? Work down the pipeline:

```bash
go run cmd/test-gemini/main.go                       # 1. provider works?
curl -s localhost:8080/metrics | grep memos_          # 2. tasks flowing? fallbacks spiking?
go run cmd/verifydb/main.go                           # 3. chains in MongoDB?
go run cmd/verify_analytics/main.go                   # 4. knowledge reaching the graph?
```

Pair with the symptom table in [Troubleshooting](troubleshooting.md).
