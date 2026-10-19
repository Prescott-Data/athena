# LLM Providers

Athena needs one LLM provider for two jobs: **chat completions** (summarization, topic analysis, gray-zone arbitration, graph extraction) and **embeddings** (chain-break detection, Milvus vectors). Providers are selected entirely through environment variables; no code path is provider-specific.

`LLM_PROVIDER` chooses the backend (`gemini` | `azure` | `openai`). `LLM_API_KEY` works for every provider; the provider-native names (`GEMINI_API_KEY`, `AZURE_OPENAI_API_KEY`, `OPENAI_API_KEY`) are accepted as fallbacks. Every pipeline call (topic analysis, summaries, entity extraction, continuity, embeddings, graph extraction) goes through the same provider abstraction, so switching providers is a config change, not a migration.

## Google Gemini

```bash title="environment"
LLM_PROVIDER=gemini
LLM_API_KEY=<your Gemini API key>          # or GEMINI_API_KEY
LLM_MODEL_NAME=gemini-3-flash-preview
EMBEDDING_MODEL_NAME=gemini-embedding-001  # default when unset
MILVUS_VECTOR_DIMENSION=3072               # gemini-embedding-001 native output
```

On thinking-capable Gemini models (2.5-flash and newer), Athena disables hidden reasoning (`thinkingBudget: 0`) for its pipeline calls; they use small token budgets that thinking would otherwise consume entirely.

### Gemini via Vertex AI (no API keys)

On GCP (Cloud Run, GKE, GCE), Application Default Credentials are the platform-native auth path and avoid long-lived keys entirely:

```bash title="environment"
LLM_PROVIDER=gemini
GEMINI_USE_ADC=true
GCP_PROJECT_ID=<your project>
VERTEX_LOCATION=global           # or a region, e.g. us-central1
LLM_MODEL_NAME=gemini-3-flash-preview
EMBEDDING_MODEL_NAME=gemini-embedding-001
MILVUS_VECTOR_DIMENSION=3072
```

Credentials resolve from the workload's service account (workload identity / attached SA) or `GOOGLE_APPLICATION_CREDENTIALS`; no `LLM_API_KEY` is needed. Completions and embeddings route to the Vertex AI endpoints for the configured project and location.

## Azure OpenAI

```bash title="environment"
LLM_PROVIDER=azure
LLM_API_KEY=<key>                          # or AZURE_OPENAI_API_KEY
AZURE_OPENAI_ENDPOINT=https://<resource>.openai.azure.com
AZURE_OPENAI_DEPLOYMENT=<chat deployment>              # or LLM_MODEL_NAME
EMBEDDING_MODEL_NAME=<embedding deployment>            # or AZURE_OPENAI_EMBEDDING_DEPLOYMENT
MILVUS_VECTOR_DIMENSION=1536                           # text-embedding-3-small / ada-002 output
# AZURE_OPENAI_API_VERSION=2024-10-21                  # default
```

Athena builds the deployment URLs from the endpoint and deployment names. Full deployment URLs still work and are used as given: `LLM_BASE_URL=https://<resource>.openai.azure.com/openai/deployments/<dep>/chat/completions?api-version=...` and `EMBEDDING_BASE_URL=.../deployments/<dep>/embeddings?api-version=...` (the v1 form `.../openai/v1/chat/completions` too). The provider fails at startup naming the missing variable.

### Reasoning deployments (o-series, gpt-5 and later)

Reasoning models reject `max_tokens`, any non-default `temperature` and `stop`, and spend completion tokens on hidden reasoning before they answer. Athena handles this for Azure and OpenAI:

- The first rejected parameter switches the request shape for the life of the process (`max_completion_tokens`, no `temperature`, no `stop`). Set `LLM_REASONING_MODEL=true` to start in that shape.
- `LLM_REASONING_TOKEN_HEADROOM` (default 4096) is added to each request's token budget, so reasoning does not consume the whole budget. An answer that still comes back empty is reported as an error naming this setting, not as an empty summary.
- `LLM_REASONING_EFFORT` (for example `minimal` or `low`) is sent as `reasoning_effort` when set, to cut latency and cost; it is dropped if the deployment rejects it.

Reasoning models are slower. If summaries time out, raise `LLM_SUMMARY_TIMEOUT_SEC` (20) and `LLM_TIMEOUT_SECONDS` (10), or lower the effort.

## Checking the configuration

At startup and every `LLM_CHECK_INTERVAL_MIN` minutes (default 10; `0` checks at startup only), Athena makes one small completion and one embedding and checks the embedding's dimension against `MILVUS_VECTOR_DIMENSION`. A failure is logged as an error and reported in `GET /api/v1/health` as `"llm": "unhealthy: <reason>"` with status `degraded`; otherwise `"llm": "healthy"`.

## OpenAI

```bash title="environment"
LLM_PROVIDER=openai
LLM_API_KEY=<key>                          # or OPENAI_API_KEY
LLM_MODEL_NAME=gpt-4
EMBEDDING_MODEL_NAME=text-embedding-ada-002
MILVUS_VECTOR_DIMENSION=1536
```

## The dimension trap

!!! danger "MILVUS_VECTOR_DIMENSION must match the embedding model, and Milvus remembers"
    Azure/OpenAI `text-embedding-ada-002` produces **1536**-dimensional vectors; Gemini `gemini-embedding-001` produces **3072**. The Milvus collection is created with `MILVUS_VECTOR_DIMENSION` (default 1536). On mismatch the collection is dropped and recreated at startup, which **silently discards all existing chain vectors**.

Switching providers on an existing deployment:

1. Stop the server (or disable workers).
2. Update the `LLM_*`/`EMBEDDING_*` variables consistently, **including `MILVUS_VECTOR_DIMENSION`**.
3. Restart; the Milvus collection is recreated at the new dimension and new chains embed with the new model.

Old chains lose semantic searchability (their vectors are gone); their MongoDB summaries and any promoted LTM knowledge are unaffected. There is no re-embedding backfill today, so treat a provider switch as a semi-destructive migration and do it early in a deployment's life if possible.

## Guardrails

All LLM traffic passes through shared guardrails:

| Variable | Default | Effect |
|---|---|---|
| `LLM_RATE_LIMIT_PER_MINUTE` | 50 | Token-bucket cap on pipeline LLM calls |
| `LLM_CIRCUIT_BREAKER_THRESHOLD` | 5 | Consecutive failures before the breaker opens |
| `LLM_CIRCUIT_BREAKER_TIMEOUT_SECONDS` | 60 | How long the breaker stays open |
| `LLM_TIMEOUT_SECONDS` | 10 | Per-call timeout |

Budget note: each chain formation costs roughly 2–3 completion calls (topic analysis, summary, arbitration when the gray zone hits) and each promotion costs one extraction call. The [worker retry behavior](../concepts/cognitive-pipeline.md) is LIFO with no backoff, so an undersized rate limit during an outage recovers noisily; keep the breaker enabled.

## Verifying connectivity

```bash
go run cmd/test-gemini/main.go   # exercises the configured provider end to end
```

Watch `llm_fallback_calls_total` in [metrics](../reference/metrics.md) for silent degradation: topic analysis falls back to heuristics when the LLM is unavailable, which lowers chain quality without failing requests.

## Choosing a provider

All providers run the same pipeline. Decide on: where your data may travel (regional endpoints), embedding cost at your event volume, and completion quality on your language mix. Whichever you choose, **pin `MILVUS_VECTOR_DIMENSION` in the same change** as the model variables; that pairing is the one config mistake this system does not forgive.
