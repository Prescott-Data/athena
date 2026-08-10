# LLM Providers

Athena needs one LLM provider for two jobs: **chat completions** (summarization, topic analysis, gray-zone arbitration, graph extraction) and **embeddings** (chain-break detection, Milvus vectors). Two providers are supported: Google Gemini and Azure OpenAI.

## Google Gemini

```bash title="environment"
LLM_PROVIDER=gemini
LLM_API_KEY=<your Gemini API key>
LLM_MODEL_NAME=gemini-3-flash-preview
EMBEDDING_MODEL_NAME=gemini-embedding-001
EMBEDDING_DIMENSIONS=768
```

## Azure OpenAI

```bash title="environment"
LLM_PROVIDER=azure
AZURE_OPENAI_ENDPOINT=https://<resource>.openai.azure.com
AZURE_OPENAI_API_KEY=<key>
LLM_MODEL_NAME=gpt-4o
EMBEDDING_MODEL_NAME=text-embedding-ada-002
EMBEDDING_DIMENSIONS=1536
```

Legacy deployment-URL variables (`LLM_BASE_URL`, `EMBEDDING_BASE_URL`, `LLM_API_VERSION`, `EMBEDDING_API_VERSION`) exist for older Azure setups; the [Configuration Reference](../reference/configuration.md) covers them.

## The dimension trap

!!! danger "EMBEDDING_DIMENSIONS must match the model, and Milvus remembers"
    Azure `text-embedding-ada-002` produces **1536**-dimensional vectors; Gemini `gemini-embedding-001` produces **768**. The Milvus collection is created with the configured dimension on first startup. If you later switch providers without recreating it, inserts fail with dimension-mismatch errors and search returns garbage.

Switching providers on an existing deployment:

1. Stop the server (or disable workers).
2. Drop the Milvus collection (or point `MILVUS_DATABASE` at a fresh database).
3. Update the `LLM_*`/`EMBEDDING_*` variables consistently, including `EMBEDDING_DIMENSIONS`.
4. Restart. New chains embed with the new model.

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

Both run the same pipeline. Decide on: where your data may travel (regional endpoints), embedding cost at your event volume, and completion quality on your language mix. Whichever you choose, **pin `EMBEDDING_DIMENSIONS` in the same change** as the model variables; that pairing is the one config mistake this system does not forgive.
