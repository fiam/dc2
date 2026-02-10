# API Surface

This document tracks the currently implemented EC2/Auto Scaling API surface in
`dc2`. Keep it aligned with `pkg/dc2/dispatcher*.go` and `integration-test/`.

## Compatibility Matrix

| Service | API Action | Status | Notes |
| --- | --- | --- | --- |
| EC2 | `RunInstances` | Supported | Launches container-backed instances. |
| EC2 | `DescribeInstances` | Partial | Supports IDs and tag filters (`tag:*`, `tag-key`). |
| EC2 | `StartInstances` | Supported | `DryRun` supported. |
| EC2 | `StopInstances` | Supported | `DryRun` and force-stop path supported. |
| EC2 | `TerminateInstances` | Partial | Works, but storage cleanup is still limited. |
| EC2 | `CreateTags` | Supported | Applies to tracked resources; request-size limit enforced. |
| EC2 | `DeleteTags` | Supported | Removes tags from tracked resources. |
| EC2 | `CreateVolume` | Supported | Docker volume-backed implementation. |
| EC2 | `DeleteVolume` | Supported | Removes backing Docker volume and state. |
| EC2 | `AttachVolume` | Supported | Validates instance/volume availability zone. |
| EC2 | `DetachVolume` | Supported | Detaches from instance-backed container. |
| EC2 | `DescribeVolumes` | Supported | Supports filtering and pagination. |
| EC2 | `CreateLaunchTemplate` | Partial | Create-only launch template behavior. |
| Auto Scaling | `CreateAutoScalingGroup` | Supported | Requires launch template image and instance type. |
| Auto Scaling | `DescribeAutoScalingGroups` | Supported | Supports pagination and `IncludeInstances`. |
| Auto Scaling | `UpdateAutoScalingGroup` | Supported | Supports size, launch template, and VPC updates. |
| Auto Scaling | `SetDesiredCapacity` | Supported | Enforces min/max bounds and scales accordingly. |
| Auto Scaling | `DeleteAutoScalingGroup` | Supported | Supports `ForceDelete` instance teardown. |

## Test Coverage

- Core lifecycle coverage lives in:
  - `integration-test/instances_test.go`
  - `integration-test/volumes_test.go`
  - `integration-test/launch_templates_test.go`
  - `integration-test/autoscaling_test.go`
- When adding/changing actions, update this matrix and add or adjust integration
  tests in the same change.
