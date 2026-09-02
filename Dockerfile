# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

# Build stage - multi-arch support via TARGETARCH
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine3.24@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder

# Accept build args for cross-compilation
ARG TARGETOS
ARG TARGETARCH
ARG BUILD_TARGET=./cmd/transcoder

WORKDIR /app

RUN apk add --no-cache ca-certificates

# Copy module files first for dependency caching.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy standalone transcoder source code.
COPY . .

# Build the binary for target platform
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o service ${BUILD_TARGET}

# Production image with FFmpeg
# Using Alpine for proper multi-arch support (arm64/amd64)
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# Install FFmpeg for media processing
RUN apk add --no-cache \
    ffmpeg \
    mesa-va-gallium \
    ca-certificates \
    tzdata \
    wget

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/service ./service

# Create temp directory for transcoding
RUN mkdir -p /tmp/transcoder && chmod 755 /tmp/transcoder

# Expose health check port
EXPOSE 3020

# Set default environment variables
# FFmpeg is installed at /usr/bin in Alpine
ENV PORT=3020 \
    LOG_LEVEL=info \
    FFMPEG_PATH=/usr/bin/ffmpeg \
    FFPROBE_PATH=/usr/bin/ffprobe \
    FFMPEG_TEMP_DIR=/tmp/transcoder \
    WORKER_COUNT=3 \
    JOB_TIMEOUT_MINUTES=30

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD /bin/sh -c 'wget -q --spider "http://127.0.0.1:${PORT:-3020}/health" || exit 1'

CMD ["./service"]
