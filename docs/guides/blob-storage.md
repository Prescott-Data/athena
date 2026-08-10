# Blob Storage

Blob storage is Athena's overflow for binary and oversized event payloads: agents attach artifacts to events, Athena stores the bytes in object storage and keeps only a URI in the memory tiers. Optional; configure it only if your agents send payloads.

## How payloads flow

1. `StoreEvent` arrives with `payload` (bytes) and `mime_type`.
2. Athena uploads the object to the configured bucket.
3. The event is stored with a `blobUri` reference; the bytes never enter Redis or MongoDB.
4. Consumers resolve the URI when they need the artifact.
5. When the owning chain is [archived](../concepts/archival-and-lifecycle.md), the object is garbage-collected.

Payload presence also feeds the [density score](../concepts/heat-and-decay.md) (+0.25 on observations with workflow metadata), nudging artifact-rich topics toward promotion.

!!! warning
    Sending a `payload` with no blob store configured fails the request with `FailedPrecondition` (HTTP 412).

## Providers

```bash title="MinIO (local default, ships in docker-compose.local.yml)"
BLOB_PROVIDER=minio
BLOB_ENDPOINT=localhost:9000
BLOB_BUCKET=athena-blobs
BLOB_ACCESS_KEY=<key>
BLOB_SECRET_KEY=<secret>
BLOB_USE_SSL=false
```

```bash title="Amazon S3"
BLOB_PROVIDER=s3
BLOB_BUCKET=athena-blobs
BLOB_REGION=us-east-1
BLOB_ACCESS_KEY=<key>
BLOB_SECRET_KEY=<secret>
BLOB_USE_SSL=true
```

```bash title="Google Cloud Storage"
BLOB_PROVIDER=gcs
BLOB_BUCKET=athena-blobs
# plus GCS credentials per your environment
```

The local stack's MinIO console is at [http://localhost:9001](http://localhost:9001) for inspecting uploaded objects during development.

## Operational notes

- **Create the bucket ahead of time** and scope the credentials to it; Athena assumes the bucket exists.
- **Lifecycle is Athena's job.** The archiver deletes objects when chains freeze. Avoid aggressive bucket-level lifecycle rules that race it; if you add one as a safety net, set it well beyond your archive horizon (`MTM_ARCHIVE_SCAN_DAYS`, default 7 days).
- **Failures degrade gracefully.** If the blob store is down during archival, the error is logged and archiving proceeds; orphaned objects are cleaned on later passes.
- **Size judgment.** The payload path exists so events stay small. As a rule of thumb, anything beyond a few KB of structured output belongs in a blob, not in `content`.
