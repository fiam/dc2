# dc2

`dc2` is a lightweight EC2 emulator that runs Docker containers instead of
virtual machines. It is designed for local testing and CI workflows that need
an EC2-like API surface.

## Quick Start

### Run from GHCR

`dc2` needs access to the Docker socket because instances/volumes are backed by
local Docker resources.

```sh
docker run --rm \
  --name dc2 \
  -p 8080:8080 \
  -e ADDR=0.0.0.0:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/fiam/dc2:latest
```

Then point your AWS SDK/CLI to `http://localhost:8080`:

```sh
AWS_ACCESS_KEY_ID=test \
AWS_SECRET_ACCESS_KEY=test \
AWS_REGION=us-east-1 \
aws ec2 describe-instances --endpoint-url http://localhost:8080
```

### Compose Setup for Integration Tests

```yaml
services:
  dc2:
    image: ghcr.io/fiam/dc2:latest
    ports: ["8080:8080"]
    environment:
      ADDR: 0.0.0.0:8080
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
```

In your test container/app, configure the EC2/Auto Scaling clients to use
`http://dc2:8080` as the service endpoint.
Instance containers can access IMDS at
`http://169.254.169.254/latest/user-data` and
`http://169.254.169.254/latest/meta-data/instance-id` by default.
Metadata reads require an IMDSv2 token from `PUT /latest/api/token` first.
The shared IMDS proxy runs as a dedicated OpenResty container and routes
requests to the owning `dc2` process.
All dc2-managed containers include `DC2_RUNTIME`:
- `host` when `dc2` is running directly on the host.
- `container` when `dc2` is running in a container.

## Workload Network Reachability

By default, when `dc2` runs in a container, `RunInstances` containers are
attached to the same workload network as that `dc2` container (for example, a
Compose project network). This makes Compose setups work without extra flags.

Outside containers, or when network auto-detection is unavailable, `dc2` falls
back to Docker's `bridge` network.

To make workload instances reachable from other containers, set
`INSTANCE_NETWORK=<network-name>` (or `--instance-network <network-name>`) and
attach your test stack to the same Docker network.

If the named network already exists, `dc2` uses it as-is. Existing networks do
not need the `dc2:owned-network=true` label.

If the named network does not exist, `dc2` creates it and labels it
`dc2:owned-network=true`, then removes it when unused during shutdown.

When `INSTANCE_NETWORK` is unset, `dc2` prefers container-network
auto-detection and otherwise uses `bridge`. In both cases, default networks are
not owned/removed by `dc2`.

Use `DescribeInstances` to discover the instance address to call from your
test containers on the workload network.
In `dc2`, `PublicIpAddress` currently mirrors `PrivateIpAddress`, so either
field points to the same reachable container IP on that network.

For runnable walkthroughs and scripts, see [examples/README.md](examples/README.md).

## Testing

- `make test`: unit tests + host-mode integration tests.
- `make test-integration-in-container`: integration tests with `dc2` running in a container.
- `make test-e2e`: all long-running compose-backed end-to-end tests.
- `make test-e2e E2E_TEST_FILTER=TestComposeAutoDetectsWorkloadNetworkByDefault`: run a subset of E2E tests.

## Test Profile (Delay + Spot Reclaim)

`dc2` supports an optional YAML test profile for delay injection and spot
reclaim overrides in `RunInstances`-based flows (including Auto Scaling warm
pool launches).

- `--test-profile /path/to/profile.yaml`
- `DC2_TEST_PROFILE=/path/to/profile.yaml`

See [docs/TEST_PROFILE.md](docs/TEST_PROFILE.md) for the format, matching
rules, delay hooks, and `reclaim` semantics.

## Spot Reclaim Simulation

`dc2` can simulate AWS spot instance reclamation for instances launched with
`InstanceMarketOptions.MarketType=spot`.

- `--spot-reclaim-after 2m` or `DC2_SPOT_RECLAIM_AFTER=2m`
- `--spot-reclaim-notice 30s` or `DC2_SPOT_RECLAIM_NOTICE=30s`

When enabled:
- spot instances expose lifecycle as `spot` in `DescribeInstances`
- `RunInstances` spot options support `SpotOptions.MaxPrice` and
  `SpotOptions.InstanceInterruptionBehavior`
- `DescribeSpotInstanceRequests` reports tracked spot request state/status
- IMDS exposes interruption metadata at `/latest/meta-data/spot/instance-action`
- IMDS exposes interruption metadata at `/latest/meta-data/spot/termination-time`
- instances are automatically interrupted at reclaim time according to
  `SpotOptions.InstanceInterruptionBehavior` (default `terminate`)

Set `spot-reclaim-after` to empty/zero to disable reclaim simulation.

## Instance Type Catalog Refresh

`dc2` keeps EC2 instance type metadata in
`pkg/dc2/instancetype/data/instance_types.json`.

Refresh it from live AWS APIs through the Dockerfile generator target:

```sh
make refresh-instance-type-catalog
```

This runs `uv --script` inside a container, so the host does not need `uv`.
AWS credentials are still required (profile, environment variables, or
`~/.aws` config/credentials).

The generator currently pulls `DescribeInstanceTypes` from `us-east-1` only.
At runtime, `DescribeInstanceTypeOfferings` treats those known types as
available in any requested region/location filter.

## Exit Resource Mode

`dc2` controls shutdown cleanup/verification with `--exit-resource-mode` (or
`DC2_EXIT_RESOURCE_MODE`):

- `cleanup` (default): delete owned resources on exit, then fail shutdown if
  any owned resources remain.
- `keep`: do not cleanup or verify owned resources.
- `assert`: do not cleanup, but fail shutdown if owned resources remain.

## Build Metadata

`dc2 --help` and `dc2 -version` include build metadata (version, commit,
dirty state, and Go version).

For machine-readable diagnostics, `dc2` also serves an internal endpoint:

```sh
curl -s http://localhost:8080/_dc2/metadata
```

The endpoint is intentionally internal and not part of the EC2-compatible API
surface.

## API Status

| Area | Status | Notes |
| --- | --- | --- |
| EC2 Instances | Partial | Lifecycle APIs plus IMDSv2 instance-id/user-data/tag metadata support. |
| EC2 Volumes | Supported | Create/attach/detach/delete + describe pagination. |
| EC2 Launch Templates | Partial | Create/describe/delete/versioning + default-version updates. |
| Auto Scaling Groups | Partial | Create/describe/update/set desired/detach/delete, including event-driven replacement after out-of-band instance container delete/stop and Docker healthcheck failures. Includes partial warm pool support (`PutWarmPool`/`DescribeWarmPool`/`DeleteWarmPool`) with warm-instance scale-out consumption, `PoolState` reconciliation for existing warm instances, warm-instance recycling on launch template updates, ASG warm-pool metadata (`WarmPoolConfiguration`/`WarmPoolSize`), `ReuseOnScaleIn` scale-in return-to-warm behavior, and asynchronous retried non-force warm-pool deletion. Describe actions are read-only; reconciliation runs in background loops. |

See [docs/API_SURFACE.md](docs/API_SURFACE.md) for the detailed per-action compatibility matrix.
See [docs/IMDS.md](docs/IMDS.md) for IMDS architecture and behavior details.
See [docs/TEST_PROFILE.md](docs/TEST_PROFILE.md) for test profile delay injection behavior.
