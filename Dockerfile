# Build stage (shared)
FROM golang:1.27-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# Downgrade protobuf's global-registry conflict policy from panic to ignore.
#
# The Pocket Sign generated package
# (buf.build/gen/go/pocketsign/apis/.../pocketsign/shared/options/v1) and the
# Zitadel Go SDK (github.com/zitadel/zitadel-go/v3/.../protoc/v2) BOTH register a
# custom extension at field number 50001 on google.protobuf.MethodOptions — a
# vendor-range collision neither side owns. Any binary that links both (every
# server/consumer/job here goes through the shared DI) otherwise panics at
# package-init before main() runs. These are server-side annotation options that
# neither SDK nor our code reflects on at runtime, so the collision is harmless
# (note: lookups by number resolve to the LAST registration, not the first).
#
# `ignore` rather than `warn`: both accept the collision identically, but `warn`
# prints plain text to stderr during init, which GKE ingests as severity=ERROR
# and trips the per-workload ERROR-log alerts on every start.
#
# protobuf reads this env var during its own init, so it MUST be a process env
# (it cannot be set from main()) — hence the `ENV` on every runtime stage below.
# The k8s ConfigMaps in cloud-provisioning set the same value and take
# precedence over this `ENV`; keep the two in sync.
# See https://protobuf.dev/reference/go/faq#namespace-conflict

# --- Static binaries (every target except series-image-consumer) ---
# One `go build` for all eight CGO-free binaries: the packages they share
# (internal/..., the generated SDKs, every dependency) compile once instead of
# once per image, and Bake builds this stage once for all eight targets. With
# several main packages and a directory `-o`, go build writes each binary as
# /out/<last path element>. No main package carries a default.pgo, so
# `-pgo=auto` changes nothing today; it stays so a profile can be dropped in.
FROM builder AS build-static
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out/ \
    ./cmd/api \
    ./cmd/consumer \
    ./cmd/identity \
    ./cmd/job/concert-discovery \
    ./cmd/job/artist-image-sync \
    ./cmd/job/official-site-refresh \
    ./cmd/job/sales-phase-discovery \
    ./cmd/job/sales-reminders

# --- Server target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS server
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/api /main
EXPOSE 8080
ENTRYPOINT ["/main"]

# --- Job target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS concert-discovery
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/concert-discovery /concert-discovery
ENTRYPOINT ["/concert-discovery"]

# --- Artist Image Sync Job target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS artist-image-sync
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/artist-image-sync /artist-image-sync
ENTRYPOINT ["/artist-image-sync"]

# --- Official Site Refresh Job target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS official-site-refresh
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/official-site-refresh /official-site-refresh
ENTRYPOINT ["/official-site-refresh"]

# --- Sales Phase Discovery Job target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS sales-phase-discovery
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/sales-phase-discovery /sales-phase-discovery
ENTRYPOINT ["/sales-phase-discovery"]

# --- Sales Reminders Job target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS sales-reminders
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/sales-reminders /sales-reminders
ENTRYPOINT ["/sales-reminders"]

# --- Consumer target ---
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS consumer
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/consumer /consumer
ENTRYPOINT ["/consumer"]

# --- Identity service target ---
# The only image that holds identity-provider credentials; it has no database.
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS identity
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-static /out/identity /identity
ENTRYPOINT ["/identity"]

# --- Series Image Consumer target (libvips / CGO) ---
# Unlike every other target, the series-image-consumer links libvips (a C library),
# so it builds with CGO enabled and the `vips` build tag, and it runs on a base
# that ships the libvips shared objects — distroless/static (no libc/libvips)
# cannot run it. The build- and run-time Alpine releases MUST provide the same
# libvips major so the soname matches; keep both pinned to the same Alpine as
# the golang builder image.
FROM builder AS build-series-image-consumer
RUN apk add --no-cache vips-dev gcc musl-dev pkgconfig
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
    -tags vips \
    -ldflags='-w -s' \
    -o /out ./cmd/consumer/series-image-consumer

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS series-image-consumer
RUN apk add --no-cache vips ca-certificates \
    && addgroup -S nonroot && adduser -S -G nonroot nonroot
USER nonroot:nonroot
ENV GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore
COPY --from=build-series-image-consumer /out /series-image-consumer
ENTRYPOINT ["/series-image-consumer"]

# --- Series Image Consumer test target (libvips / CGO) ---
# CI runs `go vet`/`go test` for the vips-tagged series-image processing code
# (internal/adapter/event/*_vips*.go) through THIS stage rather than on a
# bare GitHub-hosted runner, so the tests exercise the exact same libvips
# build (same builder base image + same `apk add vips-dev ...` line) that
# ships in the series-image-consumer runtime image above. Building from
# build-series-image-consumer (not `builder`) reuses its already-installed
# vips-dev/gcc/musl-dev/pkgconfig layer instead of re-installing them.
# `docker build --target test-series-image-consumer` fails the build (and so the
# CI job) if either command exits non-zero.
FROM build-series-image-consumer AS test-series-image-consumer
RUN CGO_ENABLED=1 go vet -tags vips ./internal/adapter/event/... \
    && CGO_ENABLED=1 go test -tags vips -v ./internal/adapter/event/...
