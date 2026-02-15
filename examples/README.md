# Examples

This directory contains runnable scripts for common `dc2` workflows.

## Prerequisites

- `dc2` is already running.
- `aws` CLI is installed.
- Docker daemon is reachable.

For local development, you can run `dc2` from source as a host process:

```sh
INSTANCE_NETWORK=dc2-workload go run ./cmd/dc2 --addr 0.0.0.0:8080
```

Or run the locally built container image:

```sh
make image
docker run --rm --name dc2 \
  -p 8080:8080 \
  -e ADDR=0.0.0.0:8080 \
  -e INSTANCE_NETWORK=dc2-workload \
  -v /var/run/docker.sock:/var/run/docker.sock \
  dc2
```

Create a workload network that `dc2` can own:

```sh
docker network create \
  --driver bridge \
  --label dc2:owned-network=true \
  dc2-workload
```

## Scripts

### `asg.sh`

General ASG helper with subcommands:

- `create`: create launch template + ASG.
- `scale`: change desired capacity.
- `describe`: show ASG state and instance addresses.
- `delete`: delete ASG, optional launch template cleanup.

Examples:

```sh
./examples/asg.sh create --desired 3
./examples/asg.sh scale --asg-name <asg-name> --desired 5
./examples/asg.sh describe --asg-name <asg-name>
./examples/asg.sh delete --asg-name <asg-name> --delete-launch-template 1
```

Command-specific help:

```sh
./examples/asg.sh help
./examples/asg.sh create --help
./examples/asg.sh scale --help
./examples/asg.sh describe --help
./examples/asg.sh delete --help
```

### `asg-curl.sh`

End-to-end ASG demo:

- builds a local nginx image that self-configures from IMDS at startup
- configures a Docker healthcheck for each instance container
- creates launch template + ASG
- picks a random instance
- curls it from a `curlimages/curl` container on the workload network
- optionally forces a healthcheck failure to show ASG replacement

Examples:

```sh
# Default setup (dc2 using bridge):
./examples/asg-curl.sh

# Shared custom workload network:
WORKLOAD_NETWORK=dc2-workload ./examples/asg-curl.sh

# Verify replacement on healthcheck failure:
TEST_HEALTHCHECK_REPLACEMENT=1 ./examples/asg-curl.sh
```

## Environment Variables

Both scripts support the same endpoint/credential variables:

- `DC2_ENDPOINT` (default: `http://localhost:8080`)
- `AWS_REGION` (default: `us-east-1`)
- `AWS_ACCESS_KEY_ID` (default: `test`)
- `AWS_SECRET_ACCESS_KEY` (default: `test`)
- `AWS_SESSION_TOKEN` (optional)
