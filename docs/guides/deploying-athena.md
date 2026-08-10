# Deploying Athena

Athena is one stateless container plus five stateful dependencies. This guide covers the container, a Kubernetes deployment shape, and the operational contract.

## The container

The repo's multi-stage `Dockerfile` builds `memory-server` (and `init-ltm`) on `golang:1.26-alpine` and ships them on a minimal Alpine runtime:

- Runs as non-root (numeric UID **1000**, satisfies `runAsNonRoot` policies)
- Exposes **8080** (REST) and **9090** (gRPC)
- Built-in healthcheck against `http://localhost:8080/health`

```bash
docker build -t athena:local .
# or use the published image from GitHub Container Registry (see Releases)
```

Releases are cut automatically when `VERSION` changes on the default branch: a tag, GitHub Release, and GHCR image are produced by the release workflow.

## What Athena needs around it

| Dependency | Purpose | Managed-service guidance |
|---|---|---|
| Redis 7 | STM window, task queues | Any managed Redis; latency matters most |
| MongoDB 7 | Events, chains, sessions | Standard replica set |
| Milvus 2.3+ | Chain embeddings | Self-hosted or managed (needs etcd + object storage) |
| ArangoDB | LTM graph | Pregel support recommended (pre-3.12) for community detection |
| Blob store | Event payloads | Optional; MinIO/S3/GCS ([guide](blob-storage.md)) |

All connections are configured via environment variables ([Configuration Reference](../reference/configuration.md)). The server validates its four required secrets at startup and exits if missing.

## Kubernetes shape

```yaml title="deployment.yaml (skeleton)"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: athena
spec:
  replicas: 2
  selector:
    matchLabels: {app: athena}
  template:
    metadata:
      labels: {app: athena}
    spec:
      securityContext:
        runAsNonRoot: true
      containers:
        - name: memory-server
          image: ghcr.io/prescott-data/athena:<version>
          ports:
            - {containerPort: 8080, name: http}
            - {containerPort: 9090, name: grpc}
          envFrom:
            - secretRef: {name: athena-secrets}     # LLM keys, DB passwords, JWT secret
            - configMapRef: {name: athena-config}   # everything else
          livenessProbe:
            httpGet: {path: /health, port: http}
          readinessProbe:
            httpGet: {path: /health, port: http}
```

Plus:

- A one-time **init job** running `init-ltm` against ArangoDB before first boot (idempotent, safe to re-run on upgrades).
- The **analytics CronJob** from [Running Graph Analytics](running-graph-analytics.md).
- A `Service` exposing 8080/9090; put gRPC behind an HTTP/2-aware balancer.

## Scaling model

Replicas are safe by construction: all state lives in the stores, workers coordinate through the two-level Redis queue, and schedulers' work is idempotent (promotion re-UPSERTs; archival re-checks status). Scale horizontally for API throughput; remember each replica adds `MEMORY_WORKER_COUNT` workers to your [LLM budget](workers-and-schedulers.md#the-rate-limit-interaction).

## Production checklist

- [ ] [Authentication enabled](authentication.md); JWT if multi-tenant
- [ ] `/api/v1/admin/*` restricted at ingress
- [ ] `EMBEDDING_DIMENSIONS` matches the provider ([the trap](llm-providers.md#the-dimension-trap))
- [ ] `/metrics` scraped; alerts on queue depth and `llm_fallback_calls_total`
- [ ] Analytics CronJob scheduled off-peak with `concurrencyPolicy: Forbid`
- [ ] Datastore backups: MongoDB and ArangoDB are the system of record (Redis and Milvus are reconstructible in principle, but archived-chain vectors are not; treat Milvus as valuable)
- [ ] TLS at the edge or `MEMORY_OS_ENABLE_TLS` end-to-end
