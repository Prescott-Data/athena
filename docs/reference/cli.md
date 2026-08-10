# CLI & Binaries

Everything runnable in the repo: the `cmd/` binaries and the Make targets that build, test, and generate.

## Binaries (`cmd/`)

| Binary | Purpose |
|---|---|
| `memory-server` | The Athena server: API, workers, schedulers in one process |
| `init-ltm` | Creates the `athena_ltm` database, collections, and indexes (idempotent) |
| `simulate` | Drives a running server through scripted conversation scenarios |
| `test-gemini` | Exercises the configured LLM provider end to end |
| `verify` | End-to-end pipeline check: archival counts + graph state |
| `verifydb` | Dumps `cognitive_chains` from MongoDB |
| `verify_analytics` | Reports LTM graph state: communities, bridges, node counts |

All run with `go run cmd/<name>/main.go`; the verifiers assume the local Docker stack. Debugging workflows: [Debug Tools](../guides/debug-tools.md).

## Make targets

### Build

| Target | Does |
|---|---|
| `make build` | Compiles `memory-server` |
| `make docker-build` | Builds the Docker image |
| `make clean` | Removes build artifacts |

### Generate

| Target | Does |
|---|---|
| `make generate` | Runs `scripts/generate.sh`: protoc → `*.pb.go`, `*.pb.gw.go`, `docs/api/openapi.json` |
| `make install-tools` | Installs protoc plugins, golangci-lint, staticcheck, goimports |

Generated files under `api/grpc/gen/` are git-ignored; always regenerate rather than commit.

### Test

| Target | Does |
|---|---|
| `make test` | Full test suite |
| `make test-short` | Unit tests only; no Docker needed (stores are mocked) |
| `make test-e2e` | Integration suite; needs the Docker stack and a real LLM key (loads `.env.dev`) |
| `make test-race` | Race-detector run |
| `make test-cover` | Coverage report |

### Lint

| Target | Does |
|---|---|
| `make lint` | All checks below |
| `make lint-fmt` | gofmt |
| `make lint-vet` | go vet |
| `make lint-lint` | golangci-lint |
| `make lint-tidy` | go.mod tidy check |
| `make pre-commit` | fmt + vet + tidy, the fast pre-push loop |

### CI

| Target | Does |
|---|---|
| `make ci` | lint + test + build, the same pipeline CI runs |

## Scripts

| Script | Purpose |
|---|---|
| `run_local_server.sh` | Starts the server with dev-friendly overrides (1-minute promoter/archiver tickers) |
| `test_e2e_pipeline.sh` | Scripted end-to-end pipeline exercise |
| `scripts/generate.sh` | The protoc runner behind `make generate` |
| `scripts/lint.sh` | The lint suite behind `make lint-*` |
| `scripts/benchmark/` | Performance test harnesses |

## Releases

Releases are driven by the `VERSION` file: changing it on the default branch triggers the release workflow, which tags `v<version>`, creates a GitHub Release, and pushes the Docker image to GitHub Container Registry. There is no manual tag step.
