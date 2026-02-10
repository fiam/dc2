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

## API Status

| Area | Status | Notes |
| --- | --- | --- |
| EC2 Instances | Partial | `TerminateInstances` cleanup is still limited. |
| EC2 Volumes | Supported | Create/attach/detach/delete + describe pagination. |
| EC2 Launch Templates | Partial | `CreateLaunchTemplate` is implemented. |
| Auto Scaling Groups | Supported | Create/describe/update/set desired/delete. |

See `docs/API_SURFACE.md` for the detailed per-action compatibility matrix.

## Development

```sh
make image   # build runtime image
make test    # run unit + integration tests
make lint    # run golangci-lint
```
