# API Surface

This document tracks the currently implemented EC2/Auto Scaling API surface in
`dc2`. Keep it aligned with `pkg/dc2/dispatcher*.go` and `integration-test/`.

## Compatibility Matrix

| Entity | API Action | Status | Notes |
| --- | --- | --- | --- |
| Instance | `RunInstances` | Partial | Launches container-backed instances, including `UserData` storage for IMDS and container-derived private/public IP plus DNS metadata. |
| Instance | `DescribeInstances` | Partial | Supports IDs/tag filters (`tag:*`, `tag-key`) and returns private/public IP plus DNS metadata from container networking. |
| Instance | `StartInstances` | Supported | `DryRun` supported. |
| Instance | `StopInstances` | Supported | `DryRun` and force-stop path supported. |
| Instance | `TerminateInstances` | Partial | Works, but storage cleanup is still limited. |
| Instance | `ModifyInstanceMetadataOptions` | Partial | Supports runtime `HttpEndpoint` toggle (`enabled`/`disabled`). |
| Instance Metadata | `PUT /latest/api/token` | Supported | IMDSv2 token issuance with `X-aws-ec2-metadata-token-ttl-seconds` (1-21600). |
| Instance Metadata | `GET /latest/meta-data/instance-id` | Supported | Resolved from caller container IP; requires `X-aws-ec2-metadata-token`. |
| Instance Metadata | `GET /latest/user-data` | Supported | Available at `http://169.254.169.254/latest/user-data`; requires token header. |
| Instance Metadata | `GET /latest/meta-data/tags/instance` | Supported | Returns instance tag keys (newline-separated); requires token header. |
| Instance Metadata | `GET /latest/meta-data/tags/instance/{tag-key}` | Supported | Returns tag value for key; requires token header. |
| Tagging | `CreateTags` | Supported | Applies to tracked resources; request-size limit enforced. |
| Tagging | `DeleteTags` | Supported | Removes tags from tracked resources. |
| Volume | `CreateVolume` | Supported | Docker volume-backed implementation. |
| Volume | `DeleteVolume` | Supported | Removes backing Docker volume and state. |
| Volume | `AttachVolume` | Supported | Validates instance/volume availability zone. |
| Volume | `DetachVolume` | Supported | Detaches from instance-backed container. |
| Volume | `DescribeVolumes` | Supported | Supports filtering and pagination. |
| Launch Template | `CreateLaunchTemplate` | Partial | Persists metadata plus version `1` with `ImageId`/`InstanceType`. |
| Launch Template | `DescribeLaunchTemplates` | Supported | Supports ID/name filters and pagination. |
| Launch Template | `DeleteLaunchTemplate` | Supported | Deletes by ID or name. |
| Launch Template | `CreateLaunchTemplateVersion` | Partial | Supports `SourceVersion`, `VersionDescription`, `ImageId`, and `InstanceType`. |
| Launch Template | `DescribeLaunchTemplateVersions` | Partial | Supports `$Default`/`$Latest`/numeric selectors, min/max filters, pagination. |
| Launch Template | `ModifyLaunchTemplate` | Partial | Supports setting the default version (`SetDefaultVersion`). |
| Auto Scaling Group | `CreateAutoScalingGroup` | Supported | Requires launch template image and instance type. |
| Auto Scaling Group | `DescribeAutoScalingGroups` | Supported | Supports pagination and `IncludeInstances`. |
| Auto Scaling Group | `UpdateAutoScalingGroup` | Supported | Supports size, launch template, and VPC updates. |
| Auto Scaling Group | `SetDesiredCapacity` | Supported | Enforces min/max bounds and scales accordingly. |
| Auto Scaling Group | `DetachInstances` | Supported | Supports `ShouldDecrementDesiredCapacity`; detached instances are retained and replacements launch when needed. |
| Auto Scaling Group | `DeleteAutoScalingGroup` | Supported | Supports `ForceDelete` instance teardown. |

## Test Coverage

- Core lifecycle coverage lives in:
  - `integration-test/instances_test.go`
  - `integration-test/volumes_test.go`
  - `integration-test/launch_templates_test.go`
  - `integration-test/autoscaling_test.go`
- When adding/changing actions, update this matrix and add or adjust integration
  tests in the same change.
