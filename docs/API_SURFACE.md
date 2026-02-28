# API Surface

This document tracks the currently implemented EC2/Auto Scaling API surface in
`dc2`. Keep it aligned with `pkg/dc2/dispatcher*.go` and `integration-test/`.

## Compatibility Matrix

| Entity | API Action | Status | Notes |
| --- | --- | --- | --- |
| Instance | `RunInstances` | Partial | Launches container-backed instances, including `UserData` storage for IMDS, IP/DNS metadata, synthetic primary network interface data, and `BlockDeviceMapping[].Ebs` volume creation/attachment at launch with `DeleteOnTermination` cleanup on terminate. Instance IDs use AWS-like hex format (`i-` + 17 hex chars). Supports `InstanceMarketOptions.MarketType=spot` plus optional simulated reclaim timing. Optional test-profile rules can inject allocate/start delays and per-request spot reclaim overrides; see `docs/TEST_PROFILE.md`. |
| Instance | `DescribeInstances` | Partial | Supports IDs, tag filters (`tag:*`, `tag-key`), and instance filters (`instance-state-name`, `instance-lifecycle`, `private-ip-address`, `ip-address`, `instance-type`, `availability-zone`, DNS names). Returns IP/DNS metadata, primary network interface data, `MetadataOptions.HttpEndpoint`, spot lifecycle (`instanceLifecycle`) for spot instances, and stop/terminate transition reason fields. `PublicIpAddress` currently mirrors `PrivateIpAddress` (no separate NAT/EIP model). |
| Instance | `DescribeSpotInstanceRequests` | Partial | Supports IDs, pagination, tag filters (`tag:*`, `tag-key`), and request filters (`spot-instance-request-id`, `state`, `status-code`, `status-message`, `instance-id`, `instance-type`, `spot-price`, `type`). Spot requests are tracked for spot `RunInstances` launches, including lifecycle/status transitions for reclaim and user/service terminations. |
| Instance | `DescribeInstanceStatus` | Partial | Supports IDs/tag filters, `IncludeAllInstances`, and pagination with synthesized health summaries. |
| Instance | `StartInstances` | Supported | `DryRun` supported. |
| Instance | `StopInstances` | Supported | `DryRun` and force-stop path supported. |
| Instance | `TerminateInstances` | Partial | Works, but storage cleanup is still limited. |
| Instance | `ModifyInstanceMetadataOptions` | Partial | Supports runtime `HttpEndpoint` toggle (`enabled`/`disabled`). |
| Instance Type | `DescribeInstanceTypes` | Partial | Returns data from a generated catalog sourced from AWS `DescribeInstanceTypes` in `us-east-1`; supports `InstanceType` and `instance-type` filtering plus pagination. |
| Instance Type | `DescribeInstanceTypeOfferings` | Partial | Supports `instance-type`, `location`, and `location-type` filters plus pagination. Offerings are synthesized so all known instance types are treated as available in all requested locations. |
| Instance Type | `GetInstanceTypesFromInstanceRequirements` | Partial | Supports architecture/virtualization requirements and core `InstanceRequirements` matching (vCPU, memory, generation, storage/network, accelerators, inclusion/exclusion patterns, baseline factors) with pagination. |
| Instance Metadata | `PUT /latest/api/token` | Supported | IMDSv2 token issuance with `X-aws-ec2-metadata-token-ttl-seconds` (1-21600). |
| Instance Metadata | `GET /latest/meta-data/instance-id` | Supported | Resolved from caller container IP; requires `X-aws-ec2-metadata-token`. Routed to owner `dc2` process through shared IMDS proxy labels. |
| Instance Metadata | `GET /latest/user-data` | Supported | Available at `http://169.254.169.254/latest/user-data`; requires token header. |
| Instance Metadata | `GET /latest/meta-data/tags/instance` | Supported | Returns instance tag keys (newline-separated); requires token header. |
| Instance Metadata | `GET /latest/meta-data/tags/instance/{tag-key}` | Supported | Returns tag value for key; requires token header. |
| Instance Metadata | `GET /latest/meta-data/spot/instance-action` | Partial | Returns spot interruption action payload (`action`, `time`) when reclaim simulation is configured and a spot reclaim is pending; otherwise `404`. Requires token header. |
| Instance Metadata | `GET /latest/meta-data/spot/termination-time` | Partial | Returns RFC3339 spot termination time when reclaim simulation is configured and a spot reclaim is pending; otherwise `404`. Requires token header. |
| Internal | `GET /_dc2/metadata` | Supported | Returns `dc2` build metadata (`version`, `commit`, `commit_time`, `dirty`, `go_version`) and active emulated region as JSON. |
| Tagging | `CreateTags` | Supported | Applies to tracked resources; request-size limit enforced. |
| Tagging | `DeleteTags` | Supported | Removes tags from tracked resources. |
| Volume | `CreateVolume` | Supported | Docker volume-backed implementation. Volume IDs use AWS-like hex format (`vol-` + 17 hex chars). |
| Volume | `DeleteVolume` | Supported | Removes backing Docker volume and state. |
| Volume | `AttachVolume` | Supported | Validates instance/volume availability zone. |
| Volume | `DetachVolume` | Supported | Detaches from instance-backed container. |
| Volume | `DescribeVolumes` | Supported | Supports filtering and pagination. |
| Launch Template | `CreateLaunchTemplate` | Partial | Persists metadata plus version `1` with `ImageId`/`InstanceType`/`UserData` and `BlockDeviceMapping[].Ebs`. Launch template IDs use AWS-like hex format (`lt-` + 17 hex chars). |
| Launch Template | `DescribeLaunchTemplates` | Supported | Supports ID/name filters and pagination. |
| Launch Template | `DeleteLaunchTemplate` | Supported | Deletes by ID or name. |
| Launch Template | `CreateLaunchTemplateVersion` | Partial | Supports `SourceVersion`, `VersionDescription`, `ImageId`, `InstanceType`, `UserData`, and `BlockDeviceMapping[].Ebs`. |
| Launch Template | `DescribeLaunchTemplateVersions` | Partial | Supports `$Default`/`$Latest`/numeric selectors, min/max filters, pagination. |
| Launch Template | `ModifyLaunchTemplate` | Partial | Supports setting the default version (`SetDefaultVersion`). |
| Auto Scaling Group | `CreateAutoScalingGroup` | Supported | Requires launch template image and instance type; applies launch template `UserData` and `BlockDeviceMapping[].Ebs` to launched instances; accepts `Tags.member.N` entries with ASG resource tags. |
| Auto Scaling Group | `CreateOrUpdateTags` | Supported | Supports setting ASG tags via `Tags.member.N` payloads with `ResourceId`, `ResourceType`, `Key`, and `Value`. |
| Auto Scaling Group | `DescribeAutoScalingGroups` | Supported | Supports `AutoScalingGroupNames`, pagination, `IncludeInstances`, returned ASG `Tags`, and tag filters (`Filters.member.N.Name=tag:<key>`, `Filters.member.N.Values.member.M`). Includes warm pool metadata (`WarmPoolConfiguration`, `WarmPoolSize`) when configured. This action is read-only; reconciliation runs in background loops. |
| Auto Scaling Group | `UpdateAutoScalingGroup` | Supported | Supports size, launch template, and VPC updates. When the launch template changes, existing warm-pool instances are recycled so warm capacity is refilled from the updated template. |
| Auto Scaling Group | `SetDesiredCapacity` | Supported | Enforces min/max bounds and scales accordingly. |
| Auto Scaling Group | `DetachInstances` | Supported | Supports `ShouldDecrementDesiredCapacity`; detached instances are retained and replacements launch when needed. |
| Auto Scaling Group | `DeleteAutoScalingGroup` | Supported | Supports `ForceDelete` instance teardown. |
| Auto Scaling Group | `PutWarmPool` | Partial | Supports configuring warm pools (`MinSize`, `MaxGroupPreparedCapacity`, `PoolState`, `InstanceReusePolicy.ReuseOnScaleIn`), with warm instance launch and stopped/running pool states. Updating `PoolState` reconciles existing warm instances to the requested state. ASG scale-out consumes available warm instances before launching new ones, and scale-in can return instances to warm pool when `ReuseOnScaleIn=true`. Warm-pool instance launch timing honors test-profile `RunInstances` delay hooks (`before/after allocate/start`). |
| Auto Scaling Group | `DescribeWarmPool` | Partial | Supports warm pool pagination plus `WarmPoolConfiguration` and warm instances with `Warmed:*` lifecycle states. `WarmPoolConfiguration.Status` is populated (`Active`, `PendingDelete`). This action is read-only; reconciliation runs in background loops. |
| Auto Scaling Group | `DeleteWarmPool` | Partial | Supports warm-pool removal and terminating warm instances. Non-force delete marks `PendingDelete` and completes asynchronously in the background with retry until cleanup succeeds or configuration changes. |

## Test Coverage

- Core lifecycle coverage lives in:
  - `integration-test/instances_test.go`
  - `integration-test/spot_test.go`
  - `integration-test/instance_types_test.go`
  - `integration-test/volumes_test.go`
  - `integration-test/launch_templates_test.go`
  - `integration-test/autoscaling_test.go`
- When adding/changing actions, update this matrix and add or adjust integration
  tests in the same change.
