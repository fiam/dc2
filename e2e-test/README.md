# E2E Tests

Long-running end-to-end tests live in this directory.

## Layout

- `compose/<scenario>/docker-compose.yaml`: stack definition for a scenario.
- `*_test.go`: shared harness and scenario assertions.

Each scenario keeps its Compose definition isolated so adding new E2E flows is
mostly adding a new `compose/<scenario>/` directory plus one test.

## Run

From the repository root:

```sh
make test-e2e E2E_TEST_FILTER=TestComposeAutoDetectsWorkloadNetworkByDefault
```

or run all E2E tests:

```sh
make test-e2e
```
