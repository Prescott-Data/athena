# Changelog

Releases are cut automatically when the `VERSION` file changes on the default branch; each release tags the repo and publishes a container image to GitHub Container Registry.

<div class="changelog-release" markdown>

## v0.3.0 <span class="changelog-date">October 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.3.0" target="_blank" rel="noopener">View release</a>
</div>

`/metrics` now needs a service key, so Prometheus scrapers need a configuration change before they upgrade. See **Upgrading** below.

**Security**

- `/metrics` needs a service key. The metrics are aggregated across tenants and were readable by anyone who could reach the server; a tenant key now gets `403`. `MEMORY_OS_METRICS_PUBLIC=true` serves them without credentials, and a server running without authentication is unaffected. `/health` stays open.
- Dependencies with published advisories are upgraded: `google.golang.org/grpc` 1.83.1, `github.com/klauspost/compress` 1.18.7 and `go.opentelemetry.io/otel/sdk` 1.45.0 in the server; `golang.org/x/crypto`, `golang.org/x/net` and `compress` in the benchmark and test-script modules, which now need Go 1.25.

**Breaking changes**

- Scraping `/metrics` without a service key returns `401` (no key) or `403` (tenant key).

**Fixed**

- Milvus was used only when `MILVUS_HOST` and `MILVUS_PORT` were both set, although the documented `MEMORY_OS_MILVUS_HOST` and `MEMORY_OS_MILVUS_PORT` were read elsewhere. With the two split across the names, Athena ran without vectors: chains were stored but semantic search returned nothing, and every health check stayed green. Both names are now read, and the port defaults to `19530`.

**Added**

- `/api/v1/health` reports `milvus`, and `status` is `degraded` when it is not configured or does not answer. Starting without Milvus is logged as an error.

**Upgrading**

1. Give your Prometheus scraper a service key (`MEMORY_OS_API_KEYS="<key>=*"`) and send it as `X-API-Key`; the [Metrics reference](metrics.md) has a scrape config. Or set `MEMORY_OS_METRICS_PUBLIC=true` to keep `/metrics` open.
2. Check `/api/v1/health` after the upgrade. If `milvus` is not `healthy`, chains stored before it was fixed have no vectors and are not found by semantic search; new chains are.

</div>

<div class="changelog-release" markdown>

## v0.2.1 <span class="changelog-date">October 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.2.1" target="_blank" rel="noopener">View release</a>
</div>

**Fixed**

- Completions with Gemini on Vertex AI (`GEMINI_USE_ADC=true`) failed with `400 Please use a valid role: user, model.`, because the request carried no role. Every completion now sends the `user` role, so chain formation, topic analysis and the startup LLM check work on Vertex. The Generative Language API (API-key mode) is unaffected.

</div>

<div class="changelog-release" markdown>

## v0.2.0 <span class="changelog-date">October 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.2.0" target="_blank" rel="noopener">View release</a>
</div>

This release closes a tenant-isolation vulnerability ([GHSA-3w3p-cfpg-qr9c](https://github.com/Prescott-Data/athena/security/advisories/GHSA-3w3p-cfpg-qr9c)) and makes authentication mandatory, so existing deployments need a configuration change before they upgrade. See **Upgrading** below.

**Security**

- Every call is authenticated and authorised by one gRPC interceptor, for REST and gRPC alike. The tenant comes from the caller's credentials: a tenant API key or JWT can only reach its own tenant's sessions (others return `NotFound`), and only a service key may name the tenant in a request. Administrative calls require a service key.
- Merging and archiving chains, vector search, and the knowledge graph are scoped to the tenant (and, in the graph, to the user). Graph nodes and edges record their tenant, user and the chains they came from.
- Scope IDs containing quotes, backslashes or control characters are refused, closing a vector-store filter injection.
- There is no default API key or JWT secret, and the old placeholder secret is refused. With mTLS required, gRPC listens on loopback only.

**Breaking changes**

- Athena refuses to start without authentication. Enable API keys (`MEMORY_OS_REQUIRE_API_KEY=true` with `MEMORY_OS_API_KEYS`) or JWT, or set `MEMORY_OS_ALLOW_UNAUTHENTICATED=true` for local development only.
- API keys are now bound to a tenant: `MEMORY_OS_API_KEYS="<key>=<tenant>"` for a tenant key, `"<key>=*"` for a service key. The single `MEMORY_OS_API_KEY` from earlier releases still works and is treated as a service key.

**Added**

- `GetSession`, `DeleteSession` and `EraseMemory`. `EraseMemory` erases everything stored for a user, an agent or a whole tenant: short-term events, chains and their vectors, events, and what the knowledge graph learned from them. While an erasure runs, writes to that scope return `FailedPrecondition`.
- `AnalyzeTopics` and `GetHeatMetrics` are implemented, and every RPC in the API contract now works.
- Reasoning deployments (o-series, gpt-5 and later) on Azure OpenAI and OpenAI, detected from the first rejected request or declared with `LLM_REASONING_MODEL`. Azure resource endpoints are accepted as well as full deployment URLs.
- The LLM and embedding model are checked at startup and every `LLM_CHECK_INTERVAL_MIN` minutes; failures are logged and reported in `/api/v1/health`.
- The long-term memory graph is created at startup, retrying for up to `LTM_INIT_TIMEOUT_SEC` while ArangoDB starts. If it is unavailable, Athena still serves and `/api/v1/health` reports `ltm` as unavailable.
- `ltm-purge-unscoped` (in the image and under `cmd/`) removes knowledge-graph entries written before tenant scoping.

**Changed**

- `SearchMemory` returns the most similar chains first, and `similarity_score` is the cosine similarity (−1 to 1, higher is closer). It was a squared L2 distance, and `similarity_threshold` is now compared against the cosine similarity, so thresholds set against the old value need revisiting.
- A chain that is promoted again with an unchanged summary is skipped instead of being re-extracted by the LLM, and no longer inflates graph weights.

**Fixed**

- Short-term events are kept until they have been summarised; previously events evicted from the window before chain formation were lost ([#40](https://github.com/Prescott-Data/athena/issues/40)).
- Concurrent workers no longer form duplicate chains from the same events ([#42](https://github.com/Prescott-Data/athena/issues/42)).
- Chain formation no longer stores every event twice or drops its tenant, session and metadata ([#47](https://github.com/Prescott-Data/athena/issues/47)).
- Session endpoints that returned `Unimplemented` work, and memory can be deleted ([#41](https://github.com/Prescott-Data/athena/issues/41)).
- Search returned no chains at default limits ([#43](https://github.com/Prescott-Data/athena/issues/43)).
- The Azure provider failed silently with reasoning deployments ([#44](https://github.com/Prescott-Data/athena/issues/44)).
- Long-term memory was silently disabled on fresh deployments until the graph was created by hand ([#51](https://github.com/Prescott-Data/athena/issues/51)).

**Upgrading**

1. Choose credentials. A backend that serves many tenants (JarvisCore, for example) uses a service key: `MEMORY_OS_API_KEYS="<random key>=*"`. Callers that each belong to one tenant get tenant keys: `"<key>=acme,<key>=globex"`. Set `MEMORY_OS_REQUIRE_API_KEY=true`. An existing `MEMORY_OS_API_KEY` keeps working as a service key.
2. Give each client its key. JarvisCore reads it from `ATHENA_API_KEY`.
3. Remove graph entries written before tenant scoping. They are no longer returned, but they still hold extracted knowledge. The `ltm-purge-unscoped` tool ships in the image and reads the server's `ARANGODB_*` settings:

    ```bash
    docker exec <athena-container> ./ltm-purge-unscoped -dry-run   # count them
    docker exec <athena-container> ./ltm-purge-unscoped
    ```

    From a source checkout, run `go run ./cmd/ltm-purge-unscoped` with the same environment. Hot chains are promoted again into the scoped graph.

4. If you compare `similarity_score` against a threshold, set it again as a cosine similarity.

</div>

<div class="changelog-release" markdown>

## v0.1.6 <span class="changelog-date">September 2026</span>

**Fixed**

- `StoreEvent` now returns the durable MongoDB event ID and reports an internal
	error when permanent persistence fails instead of acknowledging a Redis-only
	event as successful.

</div>

<div class="changelog-release" markdown>

## v0.1.4 <span class="changelog-date">June 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.4" target="_blank" rel="noopener">View release</a>
</div>

**Fixed**

- Docker container now runs as numeric UID `1000`, satisfying Kubernetes `runAsNonRoot` admission policies

**Dependencies**

- `go.mongodb.org/mongo-driver` 1.13.1 → 1.17.7 (main module and benchmark harnesses)
- `golang.org/x/crypto` bumps across benchmark and test modules

</div>

<div class="changelog-release" markdown>

## v0.1.3 <span class="changelog-date">June 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.3" target="_blank" rel="noopener">View release</a>
</div>

**Fixed**

- Docker build corrected around protobuf generation (generated files are produced at build time, never committed)

</div>

<div class="changelog-release" markdown>

## v0.1.1 / v0.1.2 <span class="changelog-date">June 2026</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.2" target="_blank" rel="noopener">View release</a>
</div>

**Changed**

- First releases under the automated VERSION-driven pipeline: tag, GitHub Release, and GHCR image per version bump

</div>

<div class="changelog-release" markdown>

## v0.1.0 <span class="changelog-date">October 2025</span>

<div class="changelog-meta" markdown>
<a class="changelog-release-link" href="https://github.com/Prescott-Data/athena/releases/tag/v0.1.0" target="_blank" rel="noopener">View release</a>
</div>

**Added**

- Initial release of the Memory OS: three-tier memory (STM/MTM/LTM), cognitive pipeline with chain-break detection, Ebbinghaus heat scoring, knowledge-graph promotion, gRPC + REST API, multi-tenant scoping, and optional auth (disabled by default in this release)

</div>
