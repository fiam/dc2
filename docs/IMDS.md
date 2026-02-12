# IMDS Implementation

This document describes how `dc2` serves instance metadata at
`http://169.254.169.254/latest/...` and what is intentionally out of scope.

## Architecture

`dc2` implements IMDS with two components:

- Shared IMDS proxy container (`dc2-imds-proxy`) on a Docker bridge network
  with subnet `169.254.169.0/24`. The proxy is assigned `169.254.169.254`.
- Per-process IMDS backend HTTP server inside each `dc2` process, listening on
  a dynamic host TCP port.

The `dc2` image exposes two entrypoints from the same `dc2` binary:

- `/dc2`: EC2/Auto Scaling API server mode.
- `/imds`: IMDS proxy mode.

The shared proxy container is started from the same image as `dc2`, with
entrypoint `/imds`.

Ownership and routing metadata is stored in labels:

- Instance container label `dc2:imds-owner`: owning `dc2` main container ID.
- `dc2` main container label `dc2:imds-backend-port`: backend port for that
  `dc2` process.

## Request Flow

1. Workload container calls `http://169.254.169.254/latest/...`.
2. Shared IMDS proxy resolves caller container by source IP.
3. Proxy reads `dc2:imds-owner` from that instance.
4. Proxy inspects the owner `dc2` main container and reads
   `dc2:imds-backend-port`.
5. Proxy forwards to `http://host.docker.internal:<owner-port>/latest/...`.
6. Owner backend handles IMDSv2 token issuance/validation and serves metadata.

If instance owner metadata is missing or invalid (`dc2:imds-owner` missing,
owner container missing, or owner backend port missing/invalid), proxy returns
`500`.

If no instance matches the caller IP, proxy returns `404`.

Supported metadata paths:

- `PUT /latest/api/token`
- `GET /latest/meta-data/instance-id`
- `GET /latest/user-data`
- `GET /latest/meta-data/tags/instance`
- `GET /latest/meta-data/tags/instance/{tag-key}`

`RunInstances(UserData=...)` is normalized (base64-decoded when possible) and
stored in container labels so IMDS can return plain user-data text.

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
  `/latest/meta-data/instance-id`, `/latest/user-data`, and tag paths.
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
