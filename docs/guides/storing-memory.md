# Storing Memory

Everything Athena knows arrives through two write endpoints. This guide covers when to use each, how to shape events so the pipeline works *for* you, and how binary payloads are handled.

## Interactions vs. events

**`StoreInteraction`** is the high-level call for the standard chat pattern. One request records a full turn (the user's message plus the agent's response) as two STM events:

```bash
curl -X POST http://localhost:8080/api/v1/sessions/$SESSION/interactions \
  -H "Content-Type: application/json" \
  -d '{
    "user_message": "Can you review my Go service for race conditions?",
    "agent_response": "Yes. Share the repository and I will start with the worker pool.",
    "metadata": {"channel": "web"}
  }'
```

**`StoreEvent`** is the granular call for everything else: single messages, agent reasoning, tool calls, and observations:

```bash
curl -X POST http://localhost:8080/api/v1/sessions/$SESSION/events \
  -H "Content-Type: application/json" \
  -d '{
    "role": "agent",
    "type": "thought",
    "content": "The user mentioned race conditions; I should check the worker pool mutex usage first.",
    "metadata": {"origin_service": "code-review-agent"}
  }'
```

Use `StoreInteraction` when you have a user↔agent exchange. Use `StoreEvent` when you are recording one side, an internal step, or a non-conversational artifact.

## Roles and types

| Field | Values | Notes |
|---|---|---|
| `role` | `user` \| `agent` \| `system` | Defaults to `system` when omitted |
| `type` | `message` \| `thought` \| `action` \| `observation` | Required on `StoreEvent`; invalid values return `InvalidArgument` |

How the pipeline treats each type:

- **`message`**: conversational content. User messages (`role: user`, `type: message`) are the *only* events that trigger [chain-break checks](../concepts/cognitive-pipeline.md); they define topic boundaries.
- **`thought`**: agent reasoning. Never triggers checks, but its presence raises the segment's [density score](../concepts/heat-and-decay.md) (+0.20), making the topic more likely to be promoted.
- **`action`**: a tool call or operation the agent performed. Same density benefit as thoughts.
- **`observation`**: results and telemetry (tool output, workflow logs). Subject to [coalescing](#coalescing-taming-automation-floods).

## Metadata that changes behavior

`metadata` is a free string map, but three keys are load-bearing:

| Key | Effect |
|---|---|
| `workflow_id` / `execution_id` | Consecutive user messages sharing either value skip chain-break checks (steps of one execution are never topic breaks). Observations sharing `execution_id` + `step_id` coalesce. Presence on an observation adds +0.25 density. |
| `step_id` | Scopes coalescing within an execution |
| `origin_service` | Each distinct value adds density (+0.15, capped); useful in multi-service traces |

Everything else (locale, channel, app version) is stored and returned untouched.

## Coalescing: taming automation floods

Consecutive `observation` events with the same `execution_id` and `step_id` merge into one STM event: content is replaced with the latest, metadata is merged, and `coalesced_count` increments. A CI pipeline emitting 40 log lines consumes one window slot, not 40. Design your automation events to carry these IDs and the STM window stays useful during heavy tool use.

## Binary payloads

`StoreEvent` accepts an optional `payload` (bytes) with a `mime_type`. Athena uploads it to [blob storage](blob-storage.md) and stores only the URI on the event:

```bash
curl -X POST http://localhost:8080/api/v1/sessions/$SESSION/events \
  -H "Content-Type: application/json" \
  -d '{
    "role": "agent",
    "type": "observation",
    "content": "Test run results attached",
    "payload": "'"$(base64 -w0 results.json)"'",
    "mime_type": "application/json",
    "metadata": {"execution_id": "run-42", "step_id": "test"}
  }'
```

!!! warning
    If a payload is sent and no blob store is configured, the request fails with `FailedPrecondition` (HTTP 412). Configure `BLOB_*` variables first; see [Blob Storage](blob-storage.md).

## What happens after the write

The call returns as soon as the dual-write (Redis + MongoDB) completes. Asynchronously, for user messages, a worker checks for a topic break and may form a [cognitive chain](../concepts/chain-formation.md). Nothing you do at write time blocks on the LLM.

!!! note "Timestamps"
    `timestamp` is optional on both endpoints and defaults to server time. Supply it when backfilling history so chain formation sees the real conversational timeline.
