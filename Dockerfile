# Build stage (shared)
FROM golang:1.25.7-alpine AS builder

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
COPY --from=builder /app/configs/zkp/verification_key.json /configs/zkp/verification_key.json
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
