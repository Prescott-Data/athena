# Onboarding Guide — Athena MemOS

Welcome to **Athena MemOS**. This guide gives you a structured reading trail so you understand the service architecture without getting lost in the codebase.

Estimated time to complete the core trail: **2–3 hours** (reading + local setup).

---

## Step 1 — Get the Big Picture (10 min)

Read [`OVERVIEW.md`](./OVERVIEW.md) in this directory.

By the end you should be able to answer:
- What does Athena MemOS do?
- What do the three memory tiers (STM / MTM / LTM) stand for?
- How does data flow between Redis, MongoDB, and ArangoDB?

---

## Step 2 — Understand the Memory System (25 min)

Read [`README.md`](./README.md), sections **1–5** (Architecture, Memory Tiers, Data Flow, Background Schedulers, API Reference).

By the end you should be able to trace a complete user message from `POST /events` all the way to a graph node being written into ArangoDB.

Key things to understand:
- Why is STM dual-written to both Redis **and** MongoDB?
- What triggers a "chain break" and how does the Worker decide?
- What is a heat score and how does it decay over time?
- Why does Pregel (community detection) run as an intermittent CronJob rather than an active go routine?

---

## Step 3 — Understand the Design Rationale (10 min)

Read [`docs/ARCHITECTURE_EVOLUTION.md`](./docs/ARCHITECTURE_EVOLUTION.md).

This explains the "orbital mechanics" metaphor behind the architecture decisions — why event coalescing works the way it does, why the 12-hour cooldown exists on recall strength, and why cold chains are archived at 0.1 heat (not 0.0).

---

## Step 4 — Understand Cross-Service Integration (30 min)

Read [`docs/ATHENA_UNIFIED_ARCHITECTURE.md`](./docs/ATHENA_UNIFIED_ARCHITECTURE.md).

Since Athena acts as the central hub for other Dromos services (like `arango-db` and Python agents), this doc explains those integration boundaries.

Key sections:
- **§3 — Athena-Odin Synergy**: How the Promoter feeds into the external `arango-db` (Odin KG) pipeline.
- **§4 — Hybrid Retrieval Model**: Why LTPM is cached per-session but STM/MTM is fetched per-message.
- **§5 — Workflow Storage**: Why large workflow JSON lives in Azure Blob and only a pointer lives in Athena.

---

## Step 5 — Run Athena Locally (30–45 min)

Follow [`docs/QUICKSTART.md`](./docs/QUICKSTART.md).

At the end of this step you will have:
- All infrastructure containers running (`Redis`, `MongoDB`, `Milvus`, `ArangoDB`, `MinIO`)
- The Athena server running on `localhost:8080` and `localhost:9090`
- Successfully created a session, stored an interaction, and retrieved context via `curl`.

---

## Step 6 — Digging into the Code

Once you've completed Steps 1–5, here are the best entry points in the source code:

- `cmd/memory-server/main.go` — Server startup, dependency injection wiring.
- `internal/server/server.go` — The gRPC service handler containing all API methods.
- `internal/memory/stm_store.go` — The core logic for Short-Term Memory operations.
- `internal/memory/worker.go` — Background chain-break detection logic.
- `internal/memory/promoter.go` — The background goroutine responsible for heat-based LTM promotion.
- `pkg/memoryos/` — The Go client SDK used by external sibling services.

Read [`README.md`](./README.md) **§6–13** for deep-dive references on Auth, Environment Variables, Database Schemas, Prometheus Metrics, and Deployment.

Read [`docs/ARANGODB_INFRA_SPEC.md`](./docs/ARANGODB_INFRA_SPEC.md) for ArangoDB infrastructure sizing and security model expectations.

---

## Common Gotchas

> Read these before submitting a PR.

1. **Never commit generated protobuf files.** `api/grpc/gen/*.pb.go` and `*.pb.gw.go` are in `.gitignore` and are generated fresh by CI. Run `make generate` locally if you need them, but do not `git add` them.

2. **Pregel must never run in-process.** A previous PR added an in-process goroutine for community detection — it was reverted because Pregel loads the full graph into memory and will OOM large tenants. Always trigger via the remote K8s CronJob at `POST /api/v1/admin/analytics/trigger`.

3. **`GetContext` does not yet return LTM data.** The `ltpm` field in the response is hardcoded to `{status: "not_implemented"}`. This is a known open issue in `techdebt.md §2`. If you're building an integration that needs LTM data, use `SearchMemory` instead.

4. **STM env var naming inconsistency.** `MEMORY_OS_STM_CACHE_MAX_TURNS` (in `config.go`) and `STM_CACHE_MAX_TURNS` (in `stm_cache.go`) are logically the same setting but read from different env vars. Set **both** until this is resolved (tracked in `techdebt.md §5`).

5. **Edges with EMA-skewed confidence in production.** Edges written between ~March 9–10, 2026 may have `confidence` values drift-skewed below 0.5. An AQL migration is pending — see `techdebt.md §7`.

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
