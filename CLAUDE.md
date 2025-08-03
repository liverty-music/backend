# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the backend service for Liverty Music, a concert notification platform that provides personalized music event alerts to passionate music fans. The service implements clean architecture with gRPC/Connect-RPC, structured logging, and dependency injection. The project uses protobuf definitions from the `github.com/liverty-music/schema` repository and follows domain-driven design principles.

Liverty Music aims to transform concert discovery from an active search to an automated, personalized experience - providing a "passive experience where the information you want 'finds you'" for users who attend 10+ concerts annually.

### Core Business Features

1. **Artist Registration System**
   - Users can register favorite artists for personalized notifications
   - Support for multiple artist identifiers (Spotify, MusicBrainz)
   - Subscription management with activation/deactivation

2. **Concert Data Management**
   - Comprehensive concert information storage (venue, date, tickets, pricing)
   - Multi-location support with country/city filtering
   - Concert status tracking (scheduled, sold out, cancelled, completed)

3. **Notification Infrastructure**
   - Multi-type notifications (announced, tickets available, reminders, cancellations)
   - Multilingual support (English/Japanese)
   - Scheduled delivery with status tracking
   - Push notification integration ready

4. **Data Collection & Matching**
   - Scalable data ingestion from multiple concert sources
   - Intelligent user-artist matching for relevant notifications
   - Geographic and preference-based filtering

## Development Commands

### Running the Application
```bash
# Start the server (HTTP on :8080, gRPC/Connect on :9090)
go run cmd/api/main.go
```

### Testing
```bash
# Run all tests
go test ./...

# Run tests for a specific package
go test ./pkg/config
go test ./pkg/logging
go test ./pkg/apperr
```

### Code Generation
```bash
# Generate Wire dependency injection code (when wire.go is modified)
wire internal/di/

# Generate protobuf code (if working with proto files)
buf generate

# Generate database schema from Bun models
go run internal/infrastructure/database/rdb/migrations/generate_schema.go
```

### Database Migrations with Atlas
```bash
# Generate migration from schema changes
atlas migrate diff --env local

# Validate migrations
atlas migrate validate --env local

# Apply migrations (for local development only)
atlas migrate apply --env local
```

### API Testing with buf curl
```bash
# Test GetUser endpoint
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"user_id": {"value": "123"}}' \
  http://localhost:9090/liverty.api.v1.UserService/GetUser

# Test GetArtist endpoint  
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"artist_id": {"value": "artist123"}}' \
  http://localhost:9090/liverty.api.v1.ArtistService/GetArtist

# Test GetConcert endpoint
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"concert_id": {"value": "concert456"}}' \
  http://localhost:9090/liverty.api.v1.ConcertService/GetConcert

# Test Health Check endpoint
buf curl --schema buf.build/grpc/health --protocol connect \
  -d '{"service": ""}' \
  http://localhost:9090/grpc.health.v1.Health/Check
```

## Architecture

### Directory Structure

This project follows the [Go project layout standards](https://github.com/golang-standards/project-layout) for the project root organization:

```
├── cmd/                    # Main applications for this project
├── internal/               # Private application and library code
├── pkg/                    # Library code that's ok to use by external applications
├── api/                    # OpenAPI/Swagger specs, JSON schema files, protocol definition files
├── compose.yml             # Docker Compose configuration
├── Dockerfile              # Container build instructions
├── go.mod                  # Go module definition
└── README.md              # Project documentation
```

The `internal/` directory follows **Clean Architecture** principles with clear separation of concerns:

```
internal/
├── adapter/               # Interface Adapters Layer
│   └── rpc/              # Connect-RPC handlers (controllers)
│       └── mapper/       # Data transformation between layers
├── di/                   # Dependency Injection
│   ├── app.go           # Application initialization
│   ├── provider.go      # Wire providers
│   ├── wire.go          # Wire provider definitions
│   └── wire_gen.go      # Generated DI code
├── entity/               # Enterprise Business Rules Layer
│   ├── user.go          # User domain entity (notification recipients)
│   ├── artist.go        # Artist domain entity
│   ├── concert.go       # Concert domain entity
│   ├── notification.go  # Notification domain entity
│   └── mocks.go         # Entity mocks for testing
├── infrastructure/       # Frameworks & Drivers Layer
│   ├── database/        # Database implementations
│   │   └── rdb/         # Relational database (PostgreSQL)
│   │       └── migrations/ # Atlas migration files
│   │           ├── generate_schema.go # Schema generation script
│   │           ├── schema.sql        # Base schema file
│   │           └── versions/         # Versioned migration files
│   └── server/          # Server implementations
│       └── connect.go   # Connect-RPC server setup
└── usecase/             # Application Business Rules Layer
    ├── user.go          # User use case implementations
    ├── user_test.go     # User use case tests
    ├── artist.go        # Artist use case implementations
    ├── artist_test.go   # Artist use case tests
    ├── concert.go       # Concert use case implementations
    ├── concert_test.go  # Concert use case tests
    ├── notification.go  # Notification use case implementations
    └── notification_test.go # Notification use case tests
```

### Clean Architecture Layers
- **cmd/**: Application entry points (main.go)
- **internal/adapter/**: Interface adapters (Connect-RPC handlers)
- **internal/di/**: Dependency injection with Google Wire
- **internal/entity/**: Domain entities and business objects
- **internal/infrastructure/**: Infrastructure concerns (servers, databases)
- **internal/usecase/**: Business logic and use cases
- **pkg/**: Reusable packages (config, logging, apperr, telemetry)

### Key Dependencies
- **Connect-RPC**: [`connectrpc.com/connect`](https://connectrpc.com/connect) for HTTP/gRPC-compatible APIs
- **Wire**: [`github.com/google/wire`](https://github.com/google/wire) for compile-time dependency injection  
- **Protobuf**: Uses [`github.com/liverty-music/schema`](https://github.com/liverty-music/schema) for shared definitions
- **Database**: Bun ORM with PostgreSQL support via [`github.com/uptrace/bun`](https://github.com/uptrace/bun)
- **Logging**: Custom structured logging with OpenTelemetry integration
- **Tracing**: OpenTelemetry distributed tracing with [`connectrpc.com/otelconnect`](https://connectrpc.com/otelconnect)
- **Health Checks**: [`connectrpc.com/grpchealth`](https://connectrpc.com/grpchealth) for gRPC-compatible health monitoring

### Configuration Management
The project uses environment variables for configuration with prefix support:
- Default prefix: `APP_` (e.g., `APP_SERVER_PORT=8080`)
- Configuration is managed in `pkg/config/` with comprehensive validation
- Supports .env files and runtime environment variables
- See `pkg/config/README.md` for detailed configuration options

### Dependency Injection
- Uses Google Wire for compile-time DI
- Main DI configuration in `internal/di/wire.go`
- Generated code in `internal/di/wire_gen.go` (regenerate with `wire internal/di/`)
- App initialization creates server and manages resource lifecycle

### Error Handling
- Custom error package `pkg/apperr/` provides structured error handling
- Includes error codes, HTTP status mapping, and context preservation
- Use `apperr` for consistent error responses across the application

### Logging
- Custom logging package `pkg/logging/` with OpenTelemetry integration
- Supports both JSON and text formats
- Automatic trace_id and span_id injection when using context
- Configurable log levels (debug, info, warn, error)

## Service Implementation

### Connect-RPC Handlers
Handlers are in `internal/adapter/rpc/` and implement the generated service interfaces:
- **User Service**: `user_handler.go` - User management and subscription endpoints (`/liverty.api.v1.UserService/`)
- **Artist Service**: `artist_handler.go` - Artist management endpoints (`/liverty.api.v1.ArtistService/`)
- **Concert Service**: `concert_handler.go` - Concert management endpoints (`/liverty.api.v1.ConcertService/`)
- **Notification Service**: `notification_handler.go` - Notification management endpoints (`/liverty.api.v1.NotificationService/`)
- **Health Check**: `health_handler.go` - Database connectivity health checks (`/grpc.health.v1.Health/`)
- Use Connect protocol, not plain gRPC
- Handlers are bound to interfaces via Wire in `internal/di/wire.go`
- **Interceptor chain**: Tracing → Access Logging → Error Handling

### Health Monitoring
- gRPC-compatible health check endpoint at `/grpc.health.v1.Health/Check`
- Verifies database connectivity by pinging the PostgreSQL connection
- Returns `SERVING` when healthy, `NOT_SERVING` when database is unreachable
- Compatible with Kubernetes liveness/readiness probes and load balancers
- Structured logging of health check results with service context

### Database Integration
- Uses Bun ORM with PostgreSQL driver
- Database configuration via environment variables (see config package)
- Connection management handled in `internal/infrastructure/database/rdb/`
- Schema migrations managed with Atlas following versioned migrations strategy

### Database Migrations
The project uses Atlas for database schema management:
- **Migration Directory**: `internal/infrastructure/database/rdb/migrations/`
- **Schema Generation**: Run `go run internal/infrastructure/database/rdb/migrations/generate_schema.go` to generate DDL from Bun models
- **Versioned Migrations**: Atlas creates versioned migration files in `versions/` directory  
- **CI Integration**: GitHub Actions validates migrations but does not apply them
- **Configuration**: `atlas.hcl` defines environments and database connections
- **Commands**:
  - `atlas migrate diff --env local` - Generate migration from schema changes
  - `atlas migrate validate --env local` - Validate migration files
  - `atlas migrate apply --env local` - Apply migrations (local development only)

### Distributed Tracing
The project includes OpenTelemetry distributed tracing support:
- **Automatic tracing**: All Connect-RPC requests are automatically traced
- **Context propagation**: Trace context flows through the entire request lifecycle
- **Configurable export**: Supports both local development and production modes
- **OTLP support**: Compatible with Jaeger, Zipkin, and other OTLP-compatible backends

#### Telemetry Configuration
Environment variables for tracing configuration:
- `APP_TELEMETRY_OTLP_ENDPOINT`: OTLP exporter endpoint (optional)
- `APP_TELEMETRY_SERVICE_NAME`: Service name for traces (default: go-backend-scaffold)
- `APP_TELEMETRY_SERVICE_VERSION`: Service version for traces (default: 1.0.0)

#### Usage Examples
```bash
# Development mode (local tracing only, no export)
go run cmd/api/main.go

# Production mode with Jaeger
APP_TELEMETRY_OTLP_ENDPOINT=http://jaeger:14268 \
APP_TELEMETRY_SERVICE_NAME=my-service \
APP_TELEMETRY_SERVICE_VERSION=1.0.0 \
go run cmd/api/main.go

# Production mode with OTLP collector
APP_TELEMETRY_OTLP_ENDPOINT=http://otel-collector:4318 \
APP_TELEMETRY_SERVICE_NAME=backend-api \
go run cmd/api/main.go
```

#### Trace Features
- **Span creation**: Each RPC request gets its own span with proper metadata
- **Error tracking**: Failed requests are marked with error status and details
- **Service topology**: Automatic service relationship mapping
- **Performance monitoring**: Request duration and throughput metrics

## Development Notes

- The project follows Go module conventions with `github.com/liverty-music/backend` as module name
- Wire dependency injection requires regeneration when `wire.go` is modified
- Connect-RPC handlers use HTTP/1.1 compatible protocol (no need for HTTP/2)
- Configuration supports multiple environments (development, staging, production)
- Graceful shutdown is implemented in main.go with proper resource cleanup
- All domain entities support internationalization for multi-language notifications
- Database schema is designed for high-volume concert data ingestion and user notifications