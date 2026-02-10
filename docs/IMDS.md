# IMDS Implementation

This document describes how `dc2` serves instance metadata at
`http://169.254.169.254/latest/...` and what is intentionally out of scope.

## Architecture

`dc2` implements IMDS with two components:

- IMDS proxy container (`dc2-imds-proxy`) on a Docker bridge network with
  subnet `169.254.169.0/24`. The proxy is assigned `169.254.169.254`.
- IMDS backend HTTP server in `dc2`, reachable via:
  - internal Unix socket (`/tmp/dc2-imds/backend.sock`) for control/state sync
    across `dc2` processes
  - dynamically allocated host TCP port for proxy-to-backend data traffic

On startup, the executor ensures the network/proxy exist. New instance
containers are connected to the IMDS network and labeled with metadata
(`dc2:user-data`, instance image/type labels).
When a `dc2` process shuts down, it removes its executor resources; the IMDS
proxy container is removed once no `dc2` main containers remain on the host.

## Request Flow

1. Workload container calls `http://169.254.169.254/latest/...`.
2. Nginx proxy forwards `/latest/*` to `host.docker.internal:<dynamic-port>` and sets
   `X-Forwarded-For` to the caller container IP.
3. Backend resolves caller IP from `X-Forwarded-For` (or `RemoteAddr`).
4. Backend scans dc2-managed containers and matches the container network IP.
5. If matched and IMDS is enabled for that instance:
   - `PUT /latest/api/token` issues a token with requested TTL.
   - `GET` metadata endpoints validate `X-aws-ec2-metadata-token`.
   - Supported metadata paths:
     - `/latest/meta-data/instance-id`
     - `/latest/user-data`
     - `/latest/meta-data/tags/instance`
     - `/latest/meta-data/tags/instance/{tag-key}`

`RunInstances(UserData=...)` is normalized (base64-decoded when possible) and
stored in container labels so IMDS can return plain user-data text.

## Metadata Options Behavior

`ModifyInstanceMetadataOptions` currently supports `HttpEndpoint` only:

- `enabled`: IMDS endpoints are served.
- `disabled`: IMDS endpoints return `404`.

This state is tracked in-memory in the IMDS backend, keyed by instance
container ID.

Token behavior:

- Clients must call `PUT /latest/api/token` with header
  `X-aws-ec2-metadata-token-ttl-seconds`.
- TTL must be an integer in the range `1..21600`.
- Returned token is scoped to the calling instance and has in-memory expiry.
- Metadata reads without a valid token return `401`.

## Multiple dc2 Instances

`dc2` uses a shared IMDS backend and shared Docker IMDS proxy resources.
When multiple `dc2` processes run on the same host:

- One process owns the backend listener on `/tmp/dc2-imds/backend.sock`.
- Other processes detect the healthy backend and reuse it.
- The owning process also publishes the active backend TCP port
  (used by the proxy) in `/tmp/dc2-imds/backend.port`.
- The IMDS proxy stays up while at least one `dc2` main container exists.
- IMDS control updates (enable/disable, token revocation, tag sync) are sent to
  the shared backend over internal Unix-socket endpoints.

## Limitations

- IMDSv2 is always enforced. IMDSv1-style unauthenticated metadata reads are
  not supported.
- Only a subset of metadata paths is implemented:
  `/latest/meta-data/instance-id`, `/latest/user-data`, and tag paths.
- Other metadata options are not implemented (`HttpTokens`,
  `HttpProtocolIpv6`, `HttpPutResponseHopLimit`, `InstanceMetadataTags`).
- IMDS disable/enable state and issued tokens are not persisted across `dc2`
  process restarts.
- If the process owning the shared IMDS backend exits while other `dc2`
  processes stay up, those processes must be restarted to re-elect an IMDS
  backend owner.
- IPv6 IMDS endpoint is not supported.
- Networking is opinionated and mostly fixed:
  - proxy IP `169.254.169.254`
  - subnet `169.254.169.0/24`
  - proxy image `nginx:1.29-alpine`
  - proxy reaches backend via `host.docker.internal` and a dynamic port
- Instance lookup is a container scan by IP and may become expensive at very
  high container counts.

## Tests

Coverage lives in `integration-test/instances_test.go`, including:

- `TestInstanceUserDataViaIMDS`
- `TestInstanceMetadataRequiresToken`
- `TestInstanceTagsViaIMDS`
- `TestInstanceMetadataOptionsCanDisableIMDSAtRuntime`
