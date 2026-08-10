# Go SDK

Athena ships a lightweight Go client at `pkg/memoryos`: a REST wrapper covering the core session workflow. This guide walks through it and is honest about its current boundaries.

## Install

The SDK lives in the main module; import it directly:

```bash
go get github.com/Prescott-Data/athena
```

```go
import "github.com/Prescott-Data/athena/pkg/memoryos"
```

## Creating a client

```go
client := memoryos.NewClient(memoryos.ClientConfig{
    BaseURL:  "http://localhost:8080",
    APIKey:   os.Getenv("ATHENA_API_KEY"), // sent as X-API-Key when set
    JWTToken: token,                        // sent as X-JWT-Token when set
    Timeout:  30 * time.Second,             // default 30s when zero
})
```

`ClientConfig` is a plain struct (no functional options). Auth fields are optional and independent; set the ones your deployment [requires](authentication.md). The underlying `http.Client` is reused across calls, so create one client and share it.

## The three methods

```go
// 1. Create a session
session, err := client.CreateSession(ctx, &memoryos.CreateSessionRequest{
    UserID:   "user_123",
    Metadata: map[string]string{"app": "support-bot"},
})

// 2. Store a conversation turn
_, err = client.StoreInteraction(ctx, session.SessionID, &memoryos.StoreInteractionRequest{
    UserMessage:   "My name is John and I love writing Go.",
    AgentResponse: "Nice to meet you, John.",
    Timestamp:     time.Now(),
})

// 3. Read the context window
context, err := client.GetContext(ctx, session.SessionID, 10)
for _, ev := range context.RecentEvents {
    fmt.Println(ev.Timestamp, ev.UserMessage)
}
```

All methods take a `context.Context` for cancellation and deadline control, layered under the client-level `Timeout`.

## Current boundaries

<span class="nx-badge nx-badge-beta">Subset</span> The SDK covers sessions, interactions, and context. It does **not** yet wrap:

- `SearchMemory` (semantic search)
- `StoreEvent` (granular events, blob payloads)
- Admin endpoints

For those, call the [REST API](rest-api.md) directly, or generate a full gRPC client from the proto (`make generate` produces the stubs; see [gRPC & Protobuf](../reference/grpc.md)). Mixing the SDK for the session workflow with direct REST calls for search is a perfectly reasonable pattern.

Other characteristics to plan around:

- **No retries or backoff.** Errors return as `(nil, error)`; wrap calls in your own retry policy if your platform requires one.
- **REST transport only.** For high-frequency service-to-service traffic where gRPC matters, use the generated stubs instead.
- **Import by commit or tag.** The module is fetched from Git; pin a tag in `go.mod` for reproducible builds.

## Full turn loop

The canonical integration, combining SDK and direct REST for search:

```go
func handleTurn(ctx context.Context, client *memoryos.Client, sessionID, userMsg string) (string, error) {
    // Recent window via SDK
    window, err := client.GetContext(ctx, sessionID, 10)
    if err != nil {
        return "", fmt.Errorf("get context: %w", err)
    }

    // Older memory via REST (SearchMemory not in SDK yet)
    memories, err := searchMemory(ctx, sessionID, userMsg, 5)
    if err != nil {
        return "", fmt.Errorf("search memory: %w", err)
    }

    reply, err := generate(ctx, buildPrompt(window, memories, userMsg))
    if err != nil {
        return "", err
    }

    _, err = client.StoreInteraction(ctx, sessionID, &memoryos.StoreInteractionRequest{
        UserMessage:   userMsg,
        AgentResponse: reply,
        Timestamp:     time.Now(),
    })
    return reply, err
}
```
