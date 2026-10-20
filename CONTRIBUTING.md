# Contributing to Athena

Thank you for helping improve Athena. This page covers what you need to open a pull request: the toolchain, how to run the tests, what CI checks, and how releases are cut.

To report a security vulnerability, do not open an issue or a pull request; follow [SECURITY.md](SECURITY.md) instead.

## Toolchain

- **Go:** the version in `go.mod` (currently 1.26.2). With an older Go installed, `GOTOOLCHAIN=auto` fetches the right one.
- **Module path:** `github.com/Prescott-Data/athena`
- **Formatting and linting:** `gofmt`, `go vet` and `golangci-lint` (config in `.golangci.yml`); `make lint` runs them all.
- **Generated code:** the protobuf stubs in `api/grpc/gen/` and `docs/api/openapi.json` are generated from `api/grpc/memory.proto` and **are committed**. After changing the proto, run `make generate` (it needs `protoc`; `make install-tools` installs the Go plugins) and commit the regenerated files with your change.
- **Configuration:** every environment variable is read in `internal/config/config.go`. A new variable must also be documented in [docs/reference/configuration.md](docs/reference/configuration.md) and, if users set it routinely, in `.env.example`.

## Running the tests

```bash
make build        # compile the server
make test         # all tests; those needing real services skip themselves
```

The storage tests run against real services only when you point them at some. Start disposable ones and export their addresses:

```bash
docker run -d --rm --name athena-test-mongo  -p 27017:27017 \
  -e MONGO_INITDB_ROOT_USERNAME=admin -e MONGO_INITDB_ROOT_PASSWORD=admin mongo:7
docker run -d --rm --name athena-test-arango -p 8529:8529 -e ARANGO_ROOT_PASSWORD=test arangodb:latest
docker run -d --rm --name athena-test-redis  -p 6379:6379 redis:7

export ATHENA_TEST_MONGO_URI='mongodb://admin:admin@127.0.0.1:27017/?authSource=admin'
export ATHENA_TEST_ARANGO_URL=http://127.0.0.1:8529 ATHENA_TEST_ARANGO_PASSWORD=test
export REDIS_HOST=127.0.0.1 REDIS_PORT=6379
go test -count=1 ./...
```

Any change to how memory is stored, scoped or erased should be tested this way before review.

## Pull requests

Branch from `main` and open the pull request against `main`. Before you push:

```bash
make lint
make build
make test
```

A good pull request:

- explains **why** the change is needed, not only what it does, and links the issue it fixes;
- covers new behaviour with a test, and a bug fix with a test that fails without it;
- keeps tenant isolation intact: every read and write stays scoped to the caller's tenant (see [Security Model](docs/concepts/security-model.md));
- updates the documentation the change affects (`docs/` and, for user-facing setup, `README.md`);
- calls out anything breaking (API, configuration, stored data) in its description.

Title pull requests and commits `<area>: <imperative summary>`, for example `memory: keep STM events until they are summarised` or `docs: document EraseMemory`.

## Continuous integration

GitHub Actions runs on every pull request and every push to `main`:

| Workflow | What it does |
|---|---|
| **CI** (`ci.yml`) | `make generate`, `make build`, `make test`, and a Docker image build (not pushed) |
| **CodeQL** | Code scanning for Go, Python and JavaScript |
| **Deploy Documentation** (`docs.yml`) | Builds the MkDocs site and publishes it to GitHub Pages when `docs/` or `mkdocs.yml` change on `main` |
| **Release** (`release.yml`) | Cuts a release when `VERSION` changes on `main` (below) |
| **Mirror to Bitbucket** (`mirror-to-bitbucket.yml`) | Copies `main` to Prescott Data's internal deployment repository |

## Releases

`VERSION` is the single source of truth. To release, bump it in a pull request together with a new entry in [docs/reference/changelog.md](docs/reference/changelog.md). When that pull request merges, the Release workflow tags `v<VERSION>`, creates the GitHub release and publishes a multi-architecture image to `ghcr.io/prescott-data/athena` (also published as `ghcr.io/prescott-data/memory-os` for existing deployments), tagged with the version and `latest`.

Athena follows semantic versioning; while it is pre-1.0, a breaking change bumps the minor version.

## Technical debt

If you find a bug or design flaw you cannot fix in your change, open an issue. Longer-lived design debt is tracked in `techdebt.md`.
