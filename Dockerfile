# syntax=docker/dockerfile:1.7
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (c) 2025 Kirill aka Noiseonwires

# Pin to a patch version for reproducibility (adjust as needed)
ARG GO_VERSION=1.26.3

# Builder stage runs on the build platform (host)
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

# BuildKit provides these automatically
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# Build metadata arguments (passed from CI/CD)
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
ARG VERSION=dev

# Pinned tdewolff/minify CLI version used to shrink the embedded Web UI assets.
# Requires Go >= 1.25 (satisfied by GO_VERSION above).
ARG MINIFY_VERSION=v2.24.13

# Debug: Show build arguments
RUN echo "Build arguments:" && \
    echo "  VERSION=${VERSION}" && \
    echo "  GIT_COMMIT=${GIT_COMMIT}" && \
    echo "  BUILD_TIME=${BUILD_TIME}" && \
    echo "  TARGETOS=${TARGETOS}" && \
    echo "  TARGETARCH=${TARGETARCH}"

ENV CGO_ENABLED=0 \
    GO111MODULE=on

WORKDIR /app

# Install build-only deps
RUN apk add --no-cache git ca-certificates tzdata

# (Optional) copy only go.mod/go.sum first for caching
COPY go.mod go.sum ./
RUN go mod download

# Install the asset minifier (Go-based, so no Node toolchain is needed). This
# layer is cached independently of the source COPY below, so it only re-runs
# when MINIFY_VERSION changes. The binary lands in $GOPATH/bin (already on PATH).
RUN go install github.com/tdewolff/minify/v2/cmd/minify@${MINIFY_VERSION}

# Copy rest of source
COPY . .

# Minify the embedded Web UI assets in-place before go:embed bundles them into
# the binary. JS/CSS shrink safely; HTML keeps quotes + end tags so Alpine.js
# attribute expressions and tag structure stay intact. gzip is handled by the
# CDN in front of the container, so assets are not pre-compressed here.
RUN minify -r -i --match '~\.(css|js|html)$' \
      --html-keep-quotes --html-keep-end-tags \
      internal/web/static

# Build the application with version information
# -trimpath removes local paths, -ldflags reduce size and inject metadata
RUN echo "Building with ldflags:" && \
    echo "  -X main.version=${VERSION}" && \
    echo "  -X main.gitCommit=${GIT_COMMIT}" && \
    echo "  -X main.buildTime=${BUILD_TIME}" && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w \
      -X 'main.version=${VERSION}' \
      -X 'main.gitCommit=${GIT_COMMIT}' \
      -X 'main.buildTime=${BUILD_TIME}'" \
    -o /out/gennadium .

# Final minimal runtime image
FROM gcr.io/distroless/static:nonroot

LABEL org.opencontainers.image.title="gennadium" \
    org.opencontainers.image.description="Gennady - AI-powered moderation and engagement bot for Telegram" \
    org.opencontainers.image.source="https://github.com/noiseonwires/gennady" \
    org.opencontainers.image.url="https://github.com/noiseonwires/gennady" \
    org.opencontainers.image.licenses="AGPL-3"

WORKDIR /app

# Time zone database - required for scheduled events to honor the TZ env var.
# distroless/static has no zoneinfo of its own, so copy it from the build stage.
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=build /out/gennadium ./gennadium

# Web UI / webhook HTTP server (see server.listen_port in config.yaml)
EXPOSE 8080

USER nonroot:nonroot

# Prefer ENTRYPOINT for a fixed binary; CMD can pass default args if desired
ENTRYPOINT ["./gennadium"]
# CMD ["--help"]