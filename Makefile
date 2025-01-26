
.DEFAULT_GOAL := help

GO_VERSION ?= 1.23.3
ALPINE_VERSION ?= 3.20
GOLANGCI_LINT_VERSION ?= 1.63.4

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
		--build-arg GO_VERSION=$(GO_VERSION)
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		. --target dc2 -t dc2

.PHONY: run
run: image ## Run the docker image
	docker run -it --rm dc2

.PHONY: test
test: ## Run tests
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		. --target test -t test
	docker run --rm \
		--mount type=bind,source="$(ROOT_DIR)",target=/dc2 \
		--mount type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock \
		test

.PHONY: lint
lint: ## Run linters
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
		--build-arg GOLANGCI_LINT_VERSION=$(GOLANGCI_LINT_VERSION) \
		. --target lint --output type=cacheonly
