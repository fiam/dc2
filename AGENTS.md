# Repository Guidelines

## Project Structure & Module Organization
- `cmd/dc2/main.go`: binary entrypoint and HTTP server bootstrap.
- `pkg/dc2/`: core EC2 emulator logic.
- `pkg/dc2/api/`: request/response models and API errors.
- `pkg/dc2/dispatcher*.go`: action routing for EC2-like operations.
- `pkg/dc2/docker/`: Docker-backed executor implementation.
- `pkg/dc2/storage/`: in-memory resource state.
- `pkg/dc2/format/`: URL/XML encoding/decoding plus unit tests.
- `integration-test/`: end-to-end API behavior tests.
- `docs/API_SURFACE.md`: detailed API compatibility matrix (source of truth).
- `.github/workflows/`: CI (`lint`, `build`, `test`) and release/tag pipelines.

## Build, Test, and Development Commands
- `make image`: build the `dc2` Docker image (`Dockerfile` target `dc2`).
- `make run`: run local stack with `docker compose up --build`.
- `make test`: run `go test -v -race -covermode=atomic ./...` in the Docker test image.
- `make lint`: run `golangci-lint` using `.golangci.yaml`.
- Optional fast loop: `go test ./...` for local checks when Docker-backed integration flow is not required.

## Coding Style & Naming Conventions
- Use Go `1.26` (see `go.mod`).
- Prefer newer Go constructs when they improve clarity and correctness over
  older patterns.
- Check https://go.dev/doc/devel/release for releases after the model training
  cutoff and incorporate relevant newer language/library features when working
  on this codebase.
- Keep code `gofmt` clean; linting also enforces `goimports` and `gci` (project prefix `github.com/fiam/dc2`).
- Use lowercase package names and feature-oriented filenames (for example, `dispatcher_instance.go`, `dispatcher_volume.go`).
- Match existing action-oriented naming for handlers/types (for example, `CreateVolume`, `DescribeInstances`).

## Testing Guidelines
- Write tests with Go `testing` and `testify` (`assert`/`require`).
- Use `*_test.go` filenames and `TestXxx` function names.
- Parallelism is expected: add `t.Parallel()` in top-level tests and subtests when safe (`paralleltest` is enabled).
- Integration tests in `integration-test/` require a reachable Docker daemon.

## Commit & Pull Request Guidelines
- Follow Conventional Commits for the subject line: `feat(scope): ...`, `fix(scope): ...`, `chore: ...`.
- Keep the subject concise and imperative (target <= 50 characters).
- Include a commit body for non-trivial changes explaining what changed and why.
- Use exactly one blank line between subject and body; do not add extra blank lines in the body.
- Wrap commit body lines at about 72 characters (idiomatic Git formatting).
- Ensure linters pass before committing (`make lint` must be green).
- Example subject: `fix(xml): omit nil fields in responses`.
- PRs should include a concise behavior summary, related issue (if any), and verification evidence (`make lint`, `make test`).
- Keep PRs focused and ensure CI passes on `main` before merge.

## Documentation Maintenance
- Keep `README.md` Quick Start examples current with the published GHCR image and compose usage.
- Keep `docs/API_SURFACE.md` synchronized with `pkg/dc2/dispatcher*.go` and integration tests whenever API behavior changes.
- If release workflows or supported API actions change, update docs in the same commit.
