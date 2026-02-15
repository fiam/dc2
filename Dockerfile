ARG GO_VERSION=1.26.0
ARG ALPINE_VERSION=3.22

FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS go
RUN apk add --no-cache git

FROM go AS sources

ENV CGO_ENABLED=0
ENV GO111MODULE=auto

ARG APP_VERSION
ARG BUILDKIT_VERSION
ARG TARGETOS
ARG TARGETARCH
ARG GOGCFLAGS

WORKDIR /go/src/github.com/fiam/dc2

COPY go.mod go.sum ./
RUN go mod download

FROM sources AS builder
ARG APP_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,target=/go/src/github.com/fiam/dc2 \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    GOFLAGS="-buildvcs=auto" \
    go build \
      -gcflags "${GOGCFLAGS}" \
      -ldflags "-X github.com/fiam/dc2/pkg/dc2/buildinfo.Version=${APP_VERSION}" \
      -o /dc2 ./cmd/dc2

FROM scratch AS dc2
COPY --from=builder /dc2 /dc2
ENTRYPOINT ["/dc2"]

FROM sources AS test
ENV CGO_ENABLED=1
RUN apk add --no-cache gcc libc-dev docker make
COPY <<'EOF' /test.sh
#!/bin/sh
set -e
go_test_parallel="${GO_TEST_PARALLEL:-}"
echo "go test config: timeout=${GO_TEST_TIMEOUT:-10m} go_parallel=${go_test_parallel:-default}"
if [ -n "$go_test_parallel" ]; then
  go_test_parallel_arg="-parallel $go_test_parallel"
else
  go_test_parallel_arg=""
fi
# shellcheck disable=SC2086
go test -timeout "${GO_TEST_TIMEOUT:-10m}" -v $go_test_parallel_arg -race -coverprofile=/tmp/coverage.txt -covermode=atomic ./...
go tool cover -func=/tmp/coverage.txt
EOF
RUN chmod +x /test.sh
WORKDIR /dc2
CMD [ "/test.sh" ]

FROM sources AS lint
ARG GOLANGCI_LINT_VERSION
RUN wget -O- -nv https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s v${GOLANGCI_LINT_VERSION} && mv ./bin/* /usr/local/bin
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,target=/go/src/github.com/fiam/dc2 \
    /usr/local/bin/golangci-lint run --timeout=10m

FROM scratch AS goreleaser
ARG TARGETARCH
COPY linux/${TARGETARCH}/dc2 /dc2
ENTRYPOINT ["/dc2"]
