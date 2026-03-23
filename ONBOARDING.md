# Onboarding Guide — dromos-core

Welcome to dromos-core. This guide gives you a structured reading trail so you understand the system in the right order — no wandering through files wondering what matters.

Estimated time to complete the core trail: **2–3 hours** (reading + local setup).

---

## Step 1 — Get the Big Picture (10 min)

Read [`OVERVIEW.md`](./OVERVIEW.md) in this directory.

By the end you should be able to answer:
- What does Athena MemOS do, and how does it differ from the Odin KG service?
- What do the three memory tiers (STM / MTM / LTM) stand for?
- Which services are written in Go vs Python?
- What are the two separate ArangoDB databases used in this platform?

---

## Step 2 — Understand the Memory System (25 min)

Read [`README.md`](./README.md), sections **1–5** (Architecture, Memory Tiers, Data Flow, Background Schedulers, API Reference).

By the end you should be able to trace a complete user message from `POST /events` all the way to a graph node being written into ArangoDB.

Key things to understand:
- Why is STM dual-written to both Redis **and** MongoDB?
- What triggers a "chain break" and how does the Worker decide?
- What is a heat score and how does it decay over time?
- Why does Pregel run as a Kubernetes CronJob rather than in-process?

---

## Step 3 — Understand the Design Rationale (10 min)

Read [`docs/ARCHITECTURE_EVOLUTION.md`](./docs/ARCHITECTURE_EVOLUTION.md).

This explains the "orbital mechanics" metaphor behind the architecture decisions — why event coalescing works the way it does, why the 12-hour cooldown exists on recall strength, and why cold chains are archived at 0.1 heat (not 0.0).

---

## Step 4 — Understand Cross-Service Integration (30 min)

Read [`docs/ATHENA_UNIFIED_ARCHITECTURE.md`](./docs/ATHENA_UNIFIED_ARCHITECTURE.md).

This is the most important document for understanding how Athena relates to every other service. Key sections:
- **§3 — Athena-Odin Synergy**: How the Promoter feeds into the Odin KG pipeline, and why the two graphs are kept separate
- **§4 — Hybrid Retrieval Model**: Why LTPM is cached per-session but STM/MTM is fetched per-message
- **§5 — Workflow Storage**: Why large workflow JSON lives in Azure Blob and only a pointer lives in Athena

---

## Step 5 — Run Athena Locally (30–45 min)

Follow [`docs/QUICKSTART.md`](./docs/QUICKSTART.md).

At the end of this step you will have:
- All infrastructure containers running (`Redis`, `MongoDB`, `Milvus`, `ArangoDB`, `MinIO`)
- The Athena server running on `localhost:8080` and `localhost:9090`
- Successfully created a session, stored an interaction, and retrieved context via curl

---

## Step 6 — Read for Your Role

Once you've completed Steps 1–5, continue with the documents most relevant to what you'll be working on.

### If you're working on `athena-memos` (Go)

| Document | What it covers |
|---|---|
| [`README.md`](./README.md) **§6–13** | Auth, all config env vars, database schemas, Prometheus metrics, CI/CD, SDK, deployment |
| [`docs/ARANGODB_INFRA_SPEC.md`](./docs/ARANGODB_INFRA_SPEC.md) | ArangoDB infra sizing, indexes, security model, alerting |
| [`techdebt.md`](./techdebt.md) | All known open issues — read this before starting any new feature |

**Entry points in code:**
- `cmd/memory-server/main.go` — server startup, wiring of all dependencies
- `internal/server/server.go` — gRPC service handler (all API methods)
- `internal/memory/stm_store.go` — core STM logic
- `internal/memory/worker.go` — background chain-break detection
- `internal/memory/promoter.go` — heat-based LTM promotion
- `pkg/memoryos/` — Go client SDK (used by other Dromos services)

---

### If you're working on `arango-db` (Odin KG service — Python)

| Document | What it covers |
|---|---|
| [`../arango-db/README.md`](../arango-db/README.md) | Full Odin service overview: graph schema, embedding storage, KG extraction, performance |
| [`../arango-db/CONTRIBUTING.md`](../arango-db/CONTRIBUTING.md) | Local setup, env vars, how to run and test |
| [`docs/ATHENA_UNIFIED_ARCHITECTURE.md`](./docs/ATHENA_UNIFIED_ARCHITECTURE.md) **§8.1** | What Athena needs from the Odin team: HTTP endpoint, custom entity types, `origin_source` tagging |

**Entry points in code:**
- `main.py` — FastAPI + gRPC server startup
- `storage/kg_extractor.py` — LLM-powered entity/relationship extraction
- `storage_client.py` — ArangoDB connection management and batch operations
- `grpc_service/` — gRPC service handler

---

### If you're working on a DocIntel agent (Python)

| Document | What it covers |
|---|---|
| [`docs/docintel-api-integration-spec.md`](./docs/docintel-api-integration-spec.md) | Definitive integration contract: 3 Athena calls, what to delete, migration phases |
| [`docs/docintel-api-integration-spec.md`](./docs/docintel-api-integration-spec.md) **§10** | Agent-by-agent Athena integration matrix: Scout, Guided Scout, Analyst |
| [`../docintel-chat-agent/README.md`](../docintel-chat-agent/README.md) | Chat agent architecture and P2P communication |

**Key architecture decisions to understand from §10:**
- The Scout agent does **not** interact with Athena at all — it queries the Odin graph only
- The Guided Scout **optionally** calls Athena `GetContext` when a user goal is vague
- The Analyst agent writes findings to DocIntel/Odin, **not** Athena — see the Federated Architecture in §10.3

---

### If you're setting up infrastructure or DevOps

| Document | What it covers |
|---|---|
| [`README.md`](./README.md) **§10 CI/CD** | Pipeline steps, protobuf generation, Docker build |
| [`README.md`](./README.md) **§13 Deployment** | Docker, AKS resource sizing, required K8s CronJob manifest |
| [`docs/ARANGODB_INFRA_SPEC.md`](./docs/ARANGODB_INFRA_SPEC.md) | ArangoDB storage (SSD/NVMe, 3000 IOPS), compute (16–32Gi RAM), security (`athena_svc` account), monitoring alerts |

---

## Common Gotchas

> Read these before writing any code.

1. **Never commit generated protobuf files.** `api/grpc/gen/*.pb.go` and `*.pb.gw.go` are in `.gitignore` and are generated fresh by CI. Run `make generate` locally if you need them, but do not `git add` them.

2. **Pregel must never run in-process.** A previous PR added an in-process goroutine for community detection — it was reverted because Pregel loads the full graph into memory and will OOM large tenants. Always trigger via the K8s CronJob at `POST /api/v1/admin/analytics/trigger`.

3. **`GetContext` does not yet return LTM data.** The `ltpm` field in the response is hardcoded to `{status: "not_implemented"}`. This is a known open issue in `techdebt.md §2`. If you're building an integration that needs LTM data, use `SearchMemory` instead.

4. **Two ArangoDB databases.** `athena_ltm` belongs to Athena. The Odin KG database belongs to `arango-db`. They are separate instances with separate schemas. Do not write Athena data into the Odin graph or vice versa.

5. **STM env var naming inconsistency.** `MEMORY_OS_STM_CACHE_MAX_TURNS` (in `config.go`) and `STM_CACHE_MAX_TURNS` (in `stm_cache.go`) are logically the same setting but read from different env vars. Set **both** until this is resolved (tracked in `techdebt.md §5`).

6. **Edges with EMA-skewed confidence in production.** Edges written between ~March 9–10, 2026 may have `confidence` values drift-skewed below 0.5. An AQL migration is pending — see `techdebt.md §7`.

---

## Open Tech Debt

Before starting any new work, review [`techdebt.md`](./techdebt.md). The most impactful open issues are:

| # | Issue | Severity |
|---|---|---|
| 1 | No unit tests for `server.go` | High |
| 2 | `GetContext` LTM field not wired | Medium |
| 3 | CI tests cover empty packages (vacuous pass) | Medium |
| 4 | Worker retry is LIFO with no backoff | Medium |
| 7 | AQL data pollution from reverted PR (production) | Medium |

---

*Last updated: March 2026*
