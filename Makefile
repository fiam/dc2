
.DEFAULT_GOAL := help

GO_VERSION ?= 1.26.0
ALPINE_VERSION ?= 3.22
GOLANGCI_LINT_VERSION ?= 2.9.0
YAMLLINT_IMAGE ?= cytopia/yamllint@sha256:3e9eb827ab2b12a5ea5f49d4257bb3aca94bba9f1ba427c8bc7f2456385a5204
APP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
GO_TEST_TIMEOUT ?= 10m
GO_TEST_FLAGS ?=
GO_TEST_PACKAGES ?= ./...
GO_TEST_PARALLEL ?=
GO_TEST_COVERPROFILE ?= /tmp/coverage.txt
GO_TEST_COVERPKG ?=
DC2_TEST_MODE ?= host
GO_TEST_UNIT_PACKAGES ?= $(shell go list ./... | grep -Ev '^github.com/fiam/dc2/(integration-test|e2e-test)$$')
GO_TEST_INTEGRATION_PACKAGES ?= ./integration-test
GO_TEST_E2E_PACKAGES ?= ./e2e-test
GO_TEST_E2E_TIMEOUT ?= 20m
GO_TEST_E2E_FLAGS ?=
E2E_TEST_FILTER ?=
INSTANCE_TYPE_CATALOG_OUTPUT ?= pkg/dc2/instancetype/data/instance_types.json
INSTANCE_TYPE_CATALOG_ARGS ?=

GOGCFLAGS :=

ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1mUsage:\n  make \033[36m<target>\033[0m\n"} \
	/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-40s\033[0m %s\n", $$1, $$2 } /^##@/ \
	{ printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: image
image: ## Build the docker image
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		--build-arg APP_VERSION=$(APP_VERSION) \
		. --target dc2 -t dc2

.PHONY: run
run: ## Run the docker compose stack
	docker compose up --build

.PHONY: test
test: ## Run unit + integration(host) tests
	@$(MAKE) test-unit
	@$(MAKE) test-integration

.PHONY: test-unit
test-unit: GO_TEST_PACKAGES := $(GO_TEST_UNIT_PACKAGES)
test-unit: test-packages ## Run non-integration tests

.PHONY: test-integration
test-integration: DC2_TEST_MODE := host
test-integration: GO_TEST_PACKAGES := $(GO_TEST_INTEGRATION_PACKAGES)
test-integration: test-packages ## Run integration tests in host mode

.PHONY: test-integration-in-container
test-integration-in-container: DC2_TEST_MODE := container
test-integration-in-container: GO_TEST_PACKAGES := $(GO_TEST_INTEGRATION_PACKAGES)
test-integration-in-container: test-packages ## Run integration tests in container mode

.PHONY: test-packages
test-packages: ## Run tests for GO_TEST_PACKAGES
	@echo "go test config: timeout=$(GO_TEST_TIMEOUT) dc2_mode=$(DC2_TEST_MODE) parallel=$(GO_TEST_PARALLEL) flags=$(GO_TEST_FLAGS) packages=$(GO_TEST_PACKAGES) coverpkg=$(GO_TEST_COVERPKG) coverprofile=$(GO_TEST_COVERPROFILE)"
	go_test_flags='$(GO_TEST_FLAGS)'; \
	go_test_packages='$(GO_TEST_PACKAGES)'; \
	go_test_parallel='$(GO_TEST_PARALLEL)'; \
	go_test_coverpkg='$(GO_TEST_COVERPKG)'; \
	go_test_coverprofile='$(GO_TEST_COVERPROFILE)'; \
	go_test_parallel_arg=''; \
	go_test_coverpkg_arg=''; \
	if [ -n "$$go_test_parallel" ]; then go_test_parallel_arg="-parallel $$go_test_parallel"; fi; \
	if [ -n "$$go_test_coverpkg" ]; then go_test_coverpkg_arg="-coverpkg $$go_test_coverpkg"; fi; \
	mkdir -p "$$(dirname "$$go_test_coverprofile")"; \
	DC2_TEST_MODE="$(DC2_TEST_MODE)" go test \
		-timeout "$(GO_TEST_TIMEOUT)" \
		-v \
		-race \
		$$go_test_coverpkg_arg \
		-coverprofile "$$go_test_coverprofile" \
		-covermode=atomic \
		$$go_test_parallel_arg \
		$$go_test_flags \
		$$go_test_packages
	go tool cover -func="$(GO_TEST_COVERPROFILE)"

.PHONY: test-in-container
test-in-container: test-integration-in-container ## Run integration tests in container mode

.PHONY: test-e2e
test-e2e: GO_TEST_TIMEOUT := $(GO_TEST_E2E_TIMEOUT)
test-e2e: GO_TEST_PACKAGES := $(GO_TEST_E2E_PACKAGES)
test-e2e: GO_TEST_FLAGS := $(strip $(GO_TEST_E2E_FLAGS) $(if $(E2E_TEST_FILTER),-run $(E2E_TEST_FILTER),))
test-e2e: image
test-e2e: test-packages ## Run long-running end-to-end tests (docker compose). Use E2E_TEST_FILTER='<regex>' to run a subset.

.PHONY: lint
lint: lint-go lint-yaml ## Run linters

.PHONY: lint-go
lint-go: ## Run Go linters
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		--build-arg GOLANGCI_LINT_VERSION=$(GOLANGCI_LINT_VERSION) \
		. --target lint --output type=cacheonly

.PHONY: lint-yaml
lint-yaml: ## Run YAML linter
	docker run --rm \
		-v "$(ROOT_DIR):/workdir" \
		-w /workdir \
		$(YAMLLINT_IMAGE) \
		-c .yamllint.yaml .

.PHONY: refresh-instance-type-catalog
refresh-instance-type-catalog: refresh-instance-type-catalog-in-docker ## Refresh EC2 instance type catalog via containerized uv script

.PHONY: refresh-instance-type-catalog-in-docker
refresh-instance-type-catalog-in-docker: ## Refresh EC2 instance type catalog via Docker target that runs uv script
	docker build -t dc2-instance-type-catalog-generator --target instance-type-catalog-generator .
	@aws_mount=""; \
	if [ -d "$$HOME/.aws" ]; then aws_mount="-v $$HOME/.aws:/root/.aws:ro"; fi; \
	docker run --rm \
		-v "$(ROOT_DIR):/workspace" \
		$$aws_mount \
		-e AWS_PROFILE \
		-e AWS_REGION \
		-e AWS_DEFAULT_REGION \
		-e AWS_ACCESS_KEY_ID \
		-e AWS_SECRET_ACCESS_KEY \
		-e AWS_SESSION_TOKEN \
		dc2-instance-type-catalog-generator \
		--output /workspace/$(INSTANCE_TYPE_CATALOG_OUTPUT) \
		$(INSTANCE_TYPE_CATALOG_ARGS)
