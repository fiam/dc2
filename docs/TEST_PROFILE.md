# Test Profile

This document describes the optional test profile used to inject delays into
`RunInstances`. This is intended for integration and end-to-end testing.

## Enable

Provide a YAML file path with either:

- `--test-profile /path/to/profile.yaml`
- `DC2_TEST_PROFILE=/path/to/profile.yaml`

When configured, `dc2` loads the profile at startup. Invalid profiles fail
startup with a validation error.

## File Format

```yaml
version: 1
rules:
  - name: optional-name
    when:
      action: RunInstances
      request:
        market:
          type: spot
      instance:
        type:
          equals: m7g.large
        vcpu:
          gte: 2
          lte: 4
        memory_mib:
          gte: 4096
    delay:
      before:
        allocate: 100ms
        start: 1s
      after:
        allocate: 50ms
        start: 200ms
```

`version` is required and must be `1`.

## Rule Matching

Rules are evaluated for each `RunInstances` call.

- All specified `when` filters must match.
- Omitted filters are wildcards.
- `when.action` currently supports matching `RunInstances`.
- `when.request.market.type` is optional:
  - If omitted, the rule matches all market types.
  - If provided, it is matched case-insensitively.
  - If the request does not set `InstanceMarketOptions.MarketType`, it is
    treated as `on-demand`.
- `when.instance.type` supports one of:
  - `equals: <type>`
  - `glob: <pattern>` (shell-style glob)
- `when.instance.vcpu` and `when.instance.memory_mib` support integer ranges:
  - `gte`, `lte`, `gt`, `lt`

If multiple rules match, delays are added together.

## Delay Hooks

Supported delay hook points:

- `delay.before.allocate`
- `delay.after.allocate`
- `delay.before.start`
- `delay.after.start`

In `RunInstances`, execution order is:

1. `before.allocate`
2. container allocation
3. `after.allocate`
4. `before.start`
5. container start
6. `after.start`

If a request is canceled while waiting on a delay, the request returns an
error and `dc2` performs normal launch cleanup for created resources.

## Notes

- YAML decoding is strict (`known fields` enabled), so unknown keys fail fast.
- This mechanism currently supports delay injection only.
