# gRPC & Protobuf

The API's single source of truth is `api/grpc/memory.proto` (package `memory.v1`, service `MemoryService`). This page covers consuming it over gRPC and regenerating the code.

## Connecting

gRPC listens on port **9090** (configurable via `MEMORY_OS_GRPC_PORT`). Plaintext by default; TLS when `MEMORY_OS_ENABLE_TLS=true` (see [Configuration](configuration.md)).

```go
conn, err := grpc.NewClient("athena:9090",
    grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil { ... }
client := gen.NewMemoryServiceClient(conn)

resp, err := client.CreateSession(ctx, &gen.CreateSessionRequest{
    TenantId: "acme",
    UserId:   "user_123",
    AgentId:  "support-bot",
})
```

With auth enabled, send credentials as metadata:

```go
ctx = metadata.AppendToOutgoingContext(ctx,
    "x-api-key", apiKey,
    "x-jwt-token", token,
)
```

The same header names apply as in REST; see [Enabling Authentication](../guides/authentication.md).

## Exploring with grpcurl

```bash
grpcurl -plaintext athena:9090 list                        # services
grpcurl -plaintext athena:9090 list memory.v1.MemoryService
grpcurl -plaintext -d '{"tenant_id":"acme","user_id":"u1"}' \
  athena:9090 memory.v1.MemoryService/CreateSession
```

If reflection is not enabled on your build, pass the proto instead: `-proto api/grpc/memory.proto -import-path api/grpc`.

## Generating code

Generated stubs are **never committed**; `api/grpc/gen/` is produced locally and in CI:

```bash
make generate          # runs scripts/generate.sh
```

This requires `protoc` plus the Go plugins (installed by `make install-tools`) and produces three artifacts from the one proto:

| Artifact | Purpose |
|---|---|
| `*.pb.go` | Message types + gRPC client/server stubs |
| `*.pb.gw.go` | The grpc-gateway REST reverse proxy |
| `docs/api/openapi.json` | OpenAPI document for the REST surface |

## Clients in other languages

Two paths, both first-class:

- **Any gRPC language**: run `protoc` with your language's plugin against `api/grpc/memory.proto` (you also need the `google/api` annotation protos, vendored under `third_party/googleapis`).
- **Any HTTP language**: generate from `docs/api/openapi.json` with an OpenAPI generator, or just call the [REST API](../guides/rest-api.md) directly.

## Versioning and compatibility

The package is `memory.v1`; additive changes (new fields, new RPCs) are backward-compatible per proto3 semantics, and field numbers are never reused. The five [Planned](api.md) RPCs are already present in the contract, so regenerating against a newer server proto will not break existing clients.

## gRPC or REST?

Same operations, same semantics, one implementation behind both. Choose gRPC for high-frequency service-to-service calls (typed stubs, HTTP/2 multiplexing, deadline propagation); choose REST for scripts, schedulers, and languages where you don't want a proto toolchain. The rationale for shipping both is on the [Architecture page](../concepts/architecture.md#one-api-two-protocols).
