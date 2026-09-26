# Build stage (shared)
FROM golang:1.27-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# Downgrade protobuf's global-registry conflict policy from panic to warn.
#
# The Pocket Sign generated package
# (buf.build/gen/go/pocketsign/apis/.../pocketsign/shared/options/v1) and the
# Zitadel Go SDK (github.com/zitadel/zitadel-go/v3/.../protoc/v2) BOTH register a
# custom extension at field number 50001 on google.protobuf.MethodOptions — a
# vendor-range collision neither side owns. Any binary that links both (every
# server/consumer/job here goes through the shared DI) otherwise panics at
# package-init before main() runs. These are server-side annotation options that
# our client code never reflects on at runtime, so first-registration-wins is
# harmless; `warn` keeps the collision observable in logs. protobuf reads this
# env var during its own init, so it MUST be a process env baked into the image
# (it cannot be set from main()) — hence the `ENV` on every runtime stage below.
# See https://protobuf.dev/reference/go/faq#namespace-conflict

# --- Server target ---
FROM builder AS build-server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/api

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS server
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-server /out /main
EXPOSE 8080
ENTRYPOINT ["/main"]

# --- Job target ---
FROM builder AS build-concert-discovery
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/concert-discovery

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS concert-discovery
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-concert-discovery /out /concert-discovery
ENTRYPOINT ["/concert-discovery"]

# --- Artist Image Sync Job target ---
FROM builder AS build-artist-image-sync
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/artist-image-sync

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS artist-image-sync
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-artist-image-sync /out /artist-image-sync
ENTRYPOINT ["/artist-image-sync"]

# --- Sales Phase Discovery Job target ---
FROM builder AS build-sales-phase-discovery
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/sales-phase-discovery

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS sales-phase-discovery
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-sales-phase-discovery /out /sales-phase-discovery
ENTRYPOINT ["/sales-phase-discovery"]

# --- Sales Reminders Job target ---
FROM builder AS build-sales-reminders
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/sales-reminders

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS sales-reminders
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-sales-reminders /out /sales-reminders
ENTRYPOINT ["/sales-reminders"]

# --- Consumer target ---
FROM builder AS build-consumer
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/consumer

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS consumer
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-consumer /out /consumer
ENTRYPOINT ["/consumer"]

# --- Media Consumer target (libvips / CGO) ---
# Unlike every other target, the media-consumer links libvips (a C library),
# so it builds with CGO enabled and the `vips` build tag, and it runs on a base
# that ships the libvips shared objects — distroless/static (no libc/libvips)
# cannot run it. The build- and run-time Alpine releases MUST provide the same
# libvips major so the soname matches; keep both pinned to the same Alpine as
# the golang builder image.
FROM builder AS build-media-consumer
RUN apk add --no-cache vips-dev gcc musl-dev pkgconfig
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
    -tags vips \
    -ldflags='-w -s' \
    -o /out ./cmd/consumer/media-consumer

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS media-consumer
RUN apk add --no-cache vips ca-certificates \
    && addgroup -S nonroot && adduser -S -G nonroot nonroot
USER nonroot:nonroot
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
COPY --from=build-media-consumer /out /media-consumer
ENTRYPOINT ["/media-consumer"]

# --- Media Consumer test target (libvips / CGO) ---
# CI runs `go vet`/`go test` for the vips-tagged media-processing code
# (internal/adapter/event/*_vips*.go) through THIS stage rather than on a
# bare GitHub-hosted runner, so the tests exercise the exact same libvips
# build (same builder base image + same `apk add vips-dev ...` line) that
# ships in the media-consumer runtime image above. Building from
# build-media-consumer (not `builder`) reuses its already-installed
# vips-dev/gcc/musl-dev/pkgconfig layer instead of re-installing them.
# `docker build --target test-media-consumer` fails the build (and so the
# CI job) if either command exits non-zero.
FROM build-media-consumer AS test-media-consumer
RUN CGO_ENABLED=1 go vet -tags vips ./internal/adapter/event/... \
    && CGO_ENABLED=1 go test -tags vips -v ./internal/adapter/event/...
