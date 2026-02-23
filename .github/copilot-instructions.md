# Dapr Runtime – Copilot Coding Agent Instructions

## Repository Overview

This is **dapr/dapr**, the Go implementation of the [Dapr](https://dapr.io) distributed application runtime. Dapr is a graduated CNCF project that provides APIs (state management, pub/sub, service invocation, actors, workflows, bindings, secrets, configuration, distributed lock, cryptography) as a sidecar process alongside applications.

**Go module**: `github.com/dapr/dapr`  
**Go version**: See `go.mod` (currently 1.24.x)  
**License**: Apache 2.0

---

## Repository Layout

```
cmd/                  # Main entry points for each binary
  daprd/              # Dapr sidecar runtime (primary binary)
  injector/           # Kubernetes sidecar injector
  operator/           # Kubernetes operator
  placement/          # Actor placement service
  scheduler/          # Scheduler service
  sentry/             # Certificate authority / mTLS
pkg/                  # Core library packages
  actors/             # Virtual actor framework
  api/                # HTTP and gRPC API implementations
    grpc/             # gRPC server
    http/             # HTTP server
    universal/        # Shared handler logic
  components/         # Component registry and loaders
  config/             # Configuration parsing
  messaging/          # Service-to-service invocation
  middleware/         # HTTP and gRPC middleware
  operator/           # Kubernetes operator logic
  placement/          # Placement client and service
  resiliency/         # Retry/circuit-breaker/timeout policies
  runtime/            # Main runtime initialization
  scheduler/          # Scheduler logic
  security/           # mTLS and SPIFFE
  sentry/             # Sentry CA service
dapr/proto/           # Protobuf definitions
  runtime/            # Dapr runtime proto
  common/             # Common proto types
  operator/           # Operator proto
  placement/          # Placement service proto
  scheduler/          # Scheduler service proto
  sentry/             # Sentry proto
tests/
  integration/        # Integration tests (run against real daprd binary)
  e2e/                # End-to-end tests (require Kubernetes)
  perf/               # Performance tests
.build-tools/         # Internal build tools
charts/               # Helm charts
```

---

## Building

**Build all binaries** (output to `dist/{os}_{arch}/release/`):
```sh
make build
```

**Build a specific binary directly** (faster, good for iteration):
```sh
cd cmd/daprd
go build -tags=allcomponents -v
```

**Build tags** for the daprd sidecar:
- `allcomponents` (default) — includes all components
- `stablecomponents` — includes only stable components

> The `DAPR_SIDECAR_FLAVOR` variable in the Makefile controls which build tag is applied.

**Cross-compile**:
```sh
make build GOOS=linux GOARCH=amd64
```

---

## Testing

### Unit Tests

Unit tests live alongside source code in `pkg/`, `utils/`, and `cmd/`. They use the build tag `//go:build unit`.

**Run all unit tests** (requires `gotestsum`; see below):
```sh
make test
```

**Run a single package** (no `gotestsum` needed):
```sh
go test -tags=unit,allcomponents ./pkg/actors/...
```

**Run a specific test**:
```sh
go test -tags=unit,allcomponents -run TestFoo ./pkg/actors/...
```

**Install `gotestsum`** (required by `make test`):
```sh
go install gotest.tools/gotestsum@latest
```

### Integration Tests

Integration tests in `tests/integration/` run against the compiled daprd binary. They use the build tag `integration`.

**Run integration tests**:
```sh
make test-integration
```

**Run a specific integration test**:
```sh
make test-integration ARGS="-run TestSuiteName/TestName"
```

**Run in parallel mode**:
```sh
make test-integration-parallel
```

> Integration tests require `CGO_ENABLED=1`.

### E2E Tests

E2E tests require a running Kubernetes cluster with Dapr installed. See `tests/docs/running-e2e-test.md` for details. These are not normally run locally; CI handles them.

---

## Linting

```sh
make lint
```

Uses `golangci-lint` **v1.64.6** with build tags `allcomponents,subtlecrypto`. Always use this exact version to avoid false errors. Download from: https://github.com/golangci/golangci-lint/releases/tag/v1.64.6

**Auto-fix issues**:
```sh
make lint-fix
```

---

## Formatting and Tidy

**Format all code and tidy go.mod**:
```sh
make format
```

This runs `gofumpt`, `goimports` (with local prefix `github.com/dapr/`), and `go mod tidy`.

**Tidy go.mod only**:
```sh
make modtidy
```

**One-line local check** (format + test + lint + verify no uncommitted changes):
```sh
make check
```

---

## Code Conventions

### License Header

Every new Go file **must** start with this Apache 2.0 header:

```go
/*
Copyright <YEAR> The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
```

### Build Tags

Unit test files use:
```go
//go:build unit
```

Integration test files use:
```go
//go:build integration
```

### Import Grouping

Imports are organized in three groups with `goimports`:
1. Standard library
2. External packages
3. Internal packages (`github.com/dapr/...`)

### Testing Patterns

- Use `github.com/stretchr/testify/require` (not `assert`) for test assertions
- Use `github.com/stretchr/testify/assert` only for non-fatal checks
- Mocks are generated with `github.com/golang/mock/mockgen` and live in `mock/` subdirectories
- Table-driven tests are preferred

### Error Handling

- Use the `errors` package from the standard library
- Dapr-specific error types are in `pkg/api/errors/`
- Do not use `fmt.Errorf` with `%w` when Dapr error types are more appropriate

### Logging

Use `github.com/dapr/kit/logger`:
```go
var log = logger.NewLogger("dapr.mypackage")
```

---

## Protobuf / gRPC

Proto files are in `dapr/proto/`. Generated Go code is committed to `pkg/proto/`.

**Regenerate proto files**:
```sh
make gen-proto
```

**Required tools** (installed via `make init-proto`):
- `protoc` v25.4
- `protoc-gen-go` v1.32.0
- `protoc-gen-go-grpc` v1.3.0
- `protoc-gen-connect-go` v1.9.1

> Do **not** manually edit files under `pkg/proto/` — they are auto-generated.

---

## DCO Sign-Off

Every commit **must** include a DCO sign-off:

```sh
git commit -s -m "your message"
```

This appends `Signed-off-by: Your Name <email>` to the commit message. PRs cannot be merged without it.

---

## CI Overview

The main CI workflow (`.github/workflows/dapr.yml`) runs on every PR to `main`/`master`/`release-*` branches:

1. **lint** — `make lint` + `make modtidy check-diff` + vulnerability scan (`govulncheck`)
2. **unit-tests** — `make test` on Linux, Windows, macOS
3. **integration-tests** — `make test-integration-parallel`
4. **build** — `make build` for various OS/arch combinations and sidecar flavors

**Common CI failure causes and fixes**:
- `make modtidy check-diff` failure: run `make modtidy` locally and commit the updated `go.mod`/`go.sum`
- Lint failure: run `make lint-fix` and fix remaining issues; ensure you are using `golangci-lint` v1.64.6
- Missing license header: add the Apache 2.0 header to new files
- Build tag missing from test file: add `//go:build unit` to unit test files

---

## Development Environment

A pre-configured dev container is available in `.devcontainer/` using the `ghcr.io/dapr/dapr-dev:latest` image. It includes Go, Docker-in-Docker, Kubernetes tools, and all necessary build dependencies.

**VS Code build tags** configured in devcontainer: `e2e,perf,conftests,unit,integration_test,certtests,allcomponents`

For manual setup, see `docs/development/setup-dapr-development-env.md`.

---

## PR Checklist

When submitting a PR, ensure:

- [ ] Code compiles: `make build`
- [ ] Unit tests pass: `make test`
- [ ] Linter passes: `make lint`
- [ ] New code has test coverage
- [ ] All new files have Apache 2.0 license header
- [ ] Commits are signed off with `git commit -s`
- [ ] `go.mod`/`go.sum` are up to date: `make modtidy`
- [ ] Issue reference included in PR description

---

## Key Dependencies

- **github.com/dapr/components-contrib** — component implementations (state stores, pub/sub brokers, etc.)
- **github.com/dapr/kit** — shared utilities (logger, concurrency, retry, etc.)
- **github.com/dapr/durabletask-go** — workflow engine
- **google.golang.org/grpc** + **connectrpc.com/connect** — gRPC and Connect-RPC
- **k8s.io/...** — Kubernetes client libraries
- **github.com/stretchr/testify** — test assertions
- **github.com/golang/mock** — mock generation
- **go.opentelemetry.io/otel** — distributed tracing
