# Build stage
FROM golang:1.25.7-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s' \
    -pgo=auto \
    -o main ./cmd/api

# Runtime stage
FROM gcr.io/distroless/static:nonroot

# Copy the binary
COPY --from=builder /app/main /main

# Expose port
EXPOSE 8080

# Run the application
ENTRYPOINT ["/main"]
