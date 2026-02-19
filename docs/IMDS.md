# IMDS Implementation

This document describes how `dc2` serves instance metadata at
`http://169.254.169.254/latest/...` and what is intentionally out of scope.

## Architecture

`dc2` implements IMDS with three components:

- Shared IMDS proxy container (`dc2-imds-proxy`) on a Docker bridge network
  with subnet `169.254.169.0/24`. The proxy is assigned `169.254.169.254`.
- Per-process IMDS backend HTTP server inside each `dc2` process, listening on
  a dynamic host TCP port.
- Per-process `dc2` main container (helper container managed by the executor)
  that stores IMDS routing labels and process ownership metadata.

The shared proxy container runs OpenResty + Lua (default image:
`openresty/openresty:1.27.1.2-alpine`). The image can be overridden with
`DC2_IMDS_PROXY_IMAGE`.

Ownership and routing metadata is stored in labels:

- Instance container label `dc2:imds-owner`: owning `dc2` main container ID.
  This is always a Docker container ID, even when `dc2` itself runs on the
  host.
- Instance container label `dc2:instance-id`: runtime instance ID suffix used
  by EC2 IDs (`i-<suffix>`).
- `dc2` main container label `dc2:imds-backend-host`: backend host for that
  `dc2` process.
- `dc2` main container label `dc2:imds-backend-port`: backend port for that
  `dc2` process.

## Request Flow

1. Workload container calls `http://169.254.169.254/latest/...`.
2. Shared IMDS proxy resolves caller container by source IP.
3. Proxy reads `dc2:imds-owner` from that instance.
4. Proxy inspects the owner `dc2` main container and reads
   `dc2:imds-backend-host` and `dc2:imds-backend-port`.
5. Proxy forwards to `http://<owner-backend-host>:<owner-port>/latest/...`.
6. Owner backend handles IMDSv2 token issuance/validation and serves metadata.

If instance owner metadata is missing or invalid (`dc2:imds-owner` missing,
owner container missing, owner backend host missing, or owner backend port
missing/invalid), proxy returns `500`.

If no instance matches the caller IP, proxy returns `404`.

## Runtime Mode Routing

`dc2:imds-owner` remains a container ID in both runtime modes:

- `DC2_RUNTIME=container`:
  - Owner is the `dc2` container running the process.
  - Backend host is that container's IP on the IMDS network.
- `DC2_RUNTIME=host`:
  - Owner is the per-process helper `dc2` main container.
  - Backend host is resolved to the host path from the IMDS network:
    - Linux: IMDS network gateway IP.
    - Non-Linux: `host.docker.internal`.

This keeps proxy routing logic uniform: resolve caller instance -> read owner
container ID -> read backend host/port labels from owner -> forward request.

Supported metadata paths:

- `PUT /latest/api/token`
- `GET /latest/meta-data/instance-id`
- `GET /latest/user-data`
- `GET /latest/meta-data/tags/instance`
- `GET /latest/meta-data/tags/instance/{tag-key}`
- `GET /latest/meta-data/spot/instance-action` (when spot reclaim simulation is pending)
- `GET /latest/meta-data/spot/termination-time` (when spot reclaim simulation is pending)

`RunInstances(UserData=...)` and launch template `UserData` (for Auto Scaling
launches) are normalized (base64-decoded when possible) and stored in container
labels so IMDS can return plain user-data text.

## Metadata Options Behavior

`ModifyInstanceMetadataOptions` currently supports `HttpEndpoint` only:

- `enabled`: IMDS endpoints are served.
- `disabled`: IMDS endpoints return `404`.

State is tracked in-memory by each `dc2` process, keyed by instance container
ID.

Token behavior:

- Clients must call `PUT /latest/api/token` with header
  `X-aws-ec2-metadata-token-ttl-seconds`.
- TTL must be an integer in the range `1..21600`.
- Returned token is scoped to the calling instance and has in-memory expiry.
- Metadata reads without a valid token return `401`.

## Multiple dc2 Instances

`dc2` uses shared Docker IMDS infrastructure (network + proxy), but backend
state is owned by each `dc2` process:

- Every `dc2` process has its own backend port and in-memory IMDS state.
- Routing to the correct backend is done through instance-owner labels.
- First `dc2` process up starts the shared proxy if needed.
- Last `dc2` process down removes the shared proxy.

Proxy startup avoids inspect/create TOCTOU races by using a create-first
strategy with inspect/reconcile retries.

## Limitations

- IMDSv2 is always enforced. IMDSv1-style unauthenticated metadata reads are
  not supported.
- Only a subset of metadata paths is implemented:
  `/latest/meta-data/instance-id`, `/latest/user-data`, tag paths, and spot
  interruption metadata paths.
- Other metadata options are not implemented (`HttpTokens`,
  `HttpProtocolIpv6`, `HttpPutResponseHopLimit`, `InstanceMetadataTags`).
- IMDS disable/enable state and issued tokens are not persisted across `dc2`
  process restarts.
- If an instance outlives its owner `dc2` main container, IMDS routing returns
  `500` for that instance until ownership metadata is corrected.
- IPv6 IMDS endpoint is not supported.
- Networking is opinionated and mostly fixed:
  - proxy IP `169.254.169.254`
  - subnet `169.254.169.0/24`
  - proxy container name `dc2-imds-proxy`

## Tests

Coverage lives in `integration-test/instances_test.go`, including:

- `TestInstanceUserDataViaIMDS`
- `TestInstanceMetadataRequiresToken`
- `TestInstanceTagsViaIMDS`
- `TestInstanceMetadataOptionsCanDisableIMDSAtRuntime`
- `TestSpotInstanceIMDSInterruptionAction` (in `integration-test/spot_test.go`)
