# Build stage (shared)
FROM golang:1.27-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# --- Server target ---
FROM builder AS build-server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/api

FROM gcr.io/distroless/static:nonroot AS server
COPY --from=build-server /out /main
EXPOSE 8080
ENTRYPOINT ["/main"]

# --- Job target ---
FROM builder AS build-concert-discovery
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/concert-discovery

FROM gcr.io/distroless/static:nonroot AS concert-discovery
COPY --from=build-concert-discovery /out /concert-discovery
ENTRYPOINT ["/concert-discovery"]

# --- Artist Image Sync Job target ---
FROM builder AS build-artist-image-sync
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/artist-image-sync

FROM gcr.io/distroless/static:nonroot AS artist-image-sync
COPY --from=build-artist-image-sync /out /artist-image-sync
ENTRYPOINT ["/artist-image-sync"]

# --- Sales Phase Discovery Job target ---
FROM builder AS build-sales-phase-discovery
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/sales-phase-discovery

FROM gcr.io/distroless/static:nonroot AS sales-phase-discovery
COPY --from=build-sales-phase-discovery /out /sales-phase-discovery
ENTRYPOINT ["/sales-phase-discovery"]

# --- Sales Reminders Job target ---
FROM builder AS build-sales-reminders
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/job/sales-reminders

FROM gcr.io/distroless/static:nonroot AS sales-reminders
COPY --from=build-sales-reminders /out /sales-reminders
ENTRYPOINT ["/sales-reminders"]

# --- Consumer target ---
FROM builder AS build-consumer
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o /out ./cmd/consumer

FROM gcr.io/distroless/static:nonroot AS consumer
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

FROM alpine:3.21 AS media-consumer
RUN apk add --no-cache vips ca-certificates \
    && addgroup -S nonroot && adduser -S -G nonroot nonroot
USER nonroot:nonroot
COPY --from=build-media-consumer /out /media-consumer
ENTRYPOINT ["/media-consumer"]
