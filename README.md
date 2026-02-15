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

By default, `RunInstances` containers are attached to Docker's `bridge`
network. If your test stack runs on another Docker network, those containers
are usually not directly reachable.

To make workload instances reachable from other containers, set
`INSTANCE_NETWORK=<network-name>` (or `--instance-network <network-name>`) and
attach your test stack to the same Docker network.

If the named network already exists, `dc2` uses it as-is. Existing networks do
not need the `dc2:owned-network=true` label.

If the named network does not exist, `dc2` creates it and labels it
`dc2:owned-network=true`, then removes it when unused during shutdown.

When `INSTANCE_NETWORK` is unset, `dc2` uses `bridge` and does not own/remove
that network.

Use `DescribeInstances` to discover the instance address to call from your
test containers on the workload network.
In `dc2`, `PublicIpAddress` currently mirrors `PrivateIpAddress`, so either
field points to the same reachable container IP on that network.

For runnable walkthroughs and scripts, see `examples/README.md`.

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
| Auto Scaling Groups | Supported | Create/describe/update/set desired/delete, including event-driven replacement after out-of-band instance container delete/stop and Docker healthcheck failures. |

See `docs/API_SURFACE.md` for the detailed per-action compatibility matrix.
See `docs/IMDS.md` for IMDS architecture and behavior details.
