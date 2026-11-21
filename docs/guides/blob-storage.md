# Blob Storage

Blob storage is Athena's overflow for binary and oversized event payloads: agents attach artifacts to events, Athena stores the bytes in object storage and keeps only a URI in the memory tiers. Optional; configure it only if your agents send payloads.

## How payloads flow

1. `StoreEvent` arrives with `payload` (bytes) and `mime_type`.
2. Athena uploads the object to the configured bucket.
3. The event is stored with a `blobUri` reference; the bytes never enter Redis or MongoDB.
4. Consumers read the artifact back through Athena with [`GetEventPayload`](#reading-a-payload-back), a range at a time; they never touch the bucket.
5. When the owning chain is [archived](../concepts/archival-and-lifecycle.md), the object is garbage-collected.

Payload presence also feeds the [density score](../concepts/heat-and-decay.md) (+0.25 on observations with workflow metadata), nudging artifact-rich topics toward promotion.

!!! warning
    Sending a `payload` with no blob store configured fails the request with `FailedPrecondition` (HTTP 412).

## Reading a payload back

`GetEventPayload` returns a byte range of the payload stored with an event. The `event_id` is the one `StoreEvent` returned:

```bash
curl -H "X-API-Key: $ATHENA_KEY" \
  "http://localhost:8080/api/v1/sessions/$SESSION/events/$EVENT_ID/payload?offset=0&length=65536"
# {"data":"<base64>","totalBytes":"48211","offset":"0","mimeType":"application/json"}
```

- `offset` defaults to 0; `length` defaults to, and is capped at, 1048576 bytes (1 MiB). Read a larger payload by advancing `offset` until it reaches `totalBytes`.
- A read at or past the end succeeds with empty `data`, so a loop can stop on either condition.
- Athena fetches only the requested range from the bucket (a native range read on every provider), so paging a large artifact never loads it whole.
- The call is scoped like every session route: the event must belong to the session's tenant, user and agent. An unknown event, someone else's event and an event without a payload all answer `NotFound` (HTTP 404), as does a payload whose object was already garbage-collected with its chain. With no blob store configured the call fails with `FailedPrecondition` (HTTP 412).

Because Athena does the read, agents and services need an Athena key and nothing else: no bucket credentials, no signed URLs.

## Payload size

A payload travels inside the `StoreEvent` request, so its size is bounded by the largest gRPC message Athena accepts: `MEMORY_OS_GRPC_MAX_MESSAGE_BYTES`, default 16777216 (16 MiB). The limit applies on the gRPC port and between the REST gateway and the gRPC server. Over REST the payload is base64 inside JSON, a third larger than the raw bytes: an 8 MiB request body carries about 6 MiB of payload and fits the default with room to spare. A request above the limit is refused with `ResourceExhausted` (HTTP 429).

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
