ARG GO_VERSION=1.24.2
ARG ALPINE_VERSION=3.20

FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS go

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
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,target=/go/src/github.com/fiam/dc2 \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -gcflags "${GOGCFLAGS}" -o /dc2 ./cmd/dc2

FROM scratch AS dc2
COPY --from=builder /dc2 /dc2
ENTRYPOINT ["/dc2"]

FROM sources AS test
ENV CGO_ENABLED=1
RUN apk add --no-cache gcc libc-dev docker
COPY <<EOF /test.sh
#!/bin/sh
set -e
go test -v -race -coverprofile=/tmp/coverage.txt -covermode=atomic ./...
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
