# syntax=docker/dockerfile:1.7
#
# Multi-stage build:
#   1. builder — Go toolchain + pinned buf + protoc plugins; runs `make generate`
#      then `make build` so proto stubs are regenerated from sources every build
#      (architecture rule 9: generated code is never committed).
#   2. runtime — distroless/static; non-root; just the binary + ca-certs.
#
# Final image target: < 30 MB.

# ---- builder ----
FROM golang:1.24-bookworm AS builder

ENV CGO_ENABLED=0 \
    GOFLAGS=-buildvcs=false \
    PATH=/go/bin:/usr/local/go/bin:$PATH

WORKDIR /src

# Cache deps before pulling the full source tree.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Codegen tools pinned per Makefile (architecture rule 9 — never @latest).
ARG BUF_VERSION=v1.55.0
ARG PROTOC_GEN_GO_VERSION=v1.36.0
ARG PROTOC_GEN_GO_GRPC_VERSION=v1.5.1
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION} \
    && go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION} \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}

# Source last so changes don't bust the dep / tool layers above.
COPY . .

# `make build` runs `make generate` first via Makefile dependency.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    make build

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /src/bin/price-service /usr/local/bin/price-service

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/price-service"]
CMD ["serve"]
