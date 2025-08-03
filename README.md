# Liverty Music Backend

The backend service for Liverty Music - a concert notification platform that transforms concert discovery from active search to personalized, automated alerts. Built with modern Go following clean architecture principles, featuring gRPC support, structured logging, and dependency injection.

**Mission**: Provide a "passive experience where the information you want 'finds you'" for passionate music fans who attend 10+ concerts annually.

## Features

### Core Business Features
- **Artist Subscription System**: Users register favorite artists for personalized notifications
- **Concert Data Management**: Comprehensive concert information with venue, pricing, and status tracking
- **Smart Notifications**: Multi-type notifications (announced, tickets available, reminders, cancellations)
- **Multilingual Support**: English/Japanese notification delivery
- **Geographic Filtering**: Location-based concert discovery and alerts

### Technical Features
- **Connect-RPC Server**: HTTP/gRPC-compatible server with protobuf support
- **Database Migrations**: Atlas-powered versioned migrations with schema generation from Bun models
- **Structured Logging**: Advanced logger with OpenTelemetry integration for distributed tracing
- **Dependency Injection**: Wire-based dependency injection for clean architecture
- **Clean Architecture**: Well-organized project structure following domain-driven design
- **Protobuf Integration**: Ready-to-use with `buf.build/liverty-music/schema` from BSR

## Project Structure

```
.
├── cmd/
│   └── api/                    # Main application entry point
├── internal/
│   ├── adapter/               # External interface adapters
│   │   └── rpc/              # Connect-RPC handlers and services
│   ├── di/                   # Dependency injection configuration
│   ├── entity/               # Domain entities (User, Artist, Concert, Notification)
│   ├── infrastructure/       # Infrastructure concerns
│   │   ├── database/         # Database implementations
│   │   └── server/           # HTTP and Connect-RPC server implementations
│   └── usecase/              # Business logic and use cases
├── pkg/
│   └── logger/               # Structured logging with OpenTelemetry
└── go.mod                    # Go module definition
```

## Prerequisites

- Go 1.24 or later
- Atlas CLI (for database migrations)
- PostgreSQL (for database development)
- Protocol Buffers compiler (for gRPC development)

## Getting Started

### Installation

1. Clone the repository:

```bash
git clone https://github.com/liverty-music/backend.git
cd backend
```

2. Install dependencies:

```bash
go mod download
```

3. Install Atlas CLI:

```bash
# Install Atlas CLI
curl -sSf https://atlasgo.sh | sh
```

4. Start the database (optional, for local development):

```bash
podman compose up -d postgres
```

### Running the Application

#### HTTP Server

```bash
go run cmd/api/main.go
```

The HTTP server will start on port 8080.

#### gRPC Server

The gRPC server is configured to run on port 9090 with reflection enabled for development tools like `grpcurl`.

### Testing the API with buf curl

You can test your Connect API endpoints using [buf curl](https://docs.buf.build/reference/curl), which allows you to invoke RPCs using your protobuf schema.

#### Prerequisites

- [buf CLI](https://docs.buf.build/installation)
- The Connect server running locally (see below)
- Access to the protobuf schema from BSR (`buf.build/liverty-music/schema`)

#### Start the Connect Server

```bash
go run cmd/api/main.go
```

The Connect server will start on port 9090.

#### Example: GetUser

```bash
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"user_id": {"value": "123"}}' \
  http://localhost:9090/liverty.api.v1.UserService/GetUser
```

#### Example: GetArtist

```bash
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"artist_id": {"value": "artist123"}}' \
  http://localhost:9090/liverty.api.v1.ArtistService/GetArtist
```

#### Example: GetConcert

```bash
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"concert_id": {"value": "concert456"}}' \
  http://localhost:9090/liverty.api.v1.ConcertService/GetConcert
```

#### Notes

- **Service paths:** Use `/liverty.api.v1.UserService/`, `/liverty.api.v1.ArtistService/`, `/liverty.api.v1.ConcertService/`, `/liverty.api.v1.NotificationService/` for BSR schema.
- **Protocol:** Always use `--protocol connect` for Connect servers.
- **Schema:** Use BSR schema reference `buf.build/liverty-music/schema` for direct access to protobuf definitions.
- **No need for `--http2-prior-knowledge`**: The Connect server works with plain HTTP/1.1 for buf curl.

### Database Migrations

This project uses Atlas for database schema management with versioned migrations.

#### Generating a New Migration (Recommended)

This project uses `mise` to streamline the migration workflow. To generate a new migration file from your model changes, simply run:

```bash
# This single command will automatically:
# 1. Generate schema.sql from your Bun models.
# 2. Compare it with the current database state and create a new migration file.
mise run migrate <migration_name>

# Example:
mise run migrate create_users_table
```

#### Atlas Migration Commands

```bash
# Generate migration from schema changes
atlas migrate diff --env local

# Validate migrations
atlas migrate validate --env local

# Apply migrations (local development only)
atlas migrate apply --env local
```

#### Migration Directory Structure

```
internal/infrastructure/database/rdb/migrations/
├── generate_schema.go    # Schema generation script
├── schema.sql           # Base schema file
└── versions/            # Versioned migration files
```

### Development

#### Linting

This project uses `golangci-lint` for linting. You can run the linter using `mise`:

```bash
# This will run all configured linters
mise run lint
```

#### Logger Usage

The scaffold includes a powerful structured logger with OpenTelemetry integration:

```go
import "github.com/liverty-music/backend/pkg/logging"

// Create a logger with default options (JSON format)
logger := logging.NewSlogLogger()

// Create a logger with custom options
logger := logging.NewSlogLogger(
    logging.WithLevel(slog.LevelDebug),
    logging.WithFormat(logging.FormatText), // Human-readable format
    logging.WithWriter(os.Stderr),
)

// Log with context (automatically includes trace_id and span_id)
ctx := context.Background()
logger.Info(ctx, "User logged in", slog.String("user_id", "123"))
```

#### Connect-RPC Handler Implementation

The service provides a foundation for implementing Connect-RPC services:

```go
// Example handler implementation
type UserHandler struct {
    userUsecase usecase.UserUsecase
}

func (h *UserHandler) GetUser(ctx context.Context, req *connect.Request[api.GetUserRequest]) (*connect.Response[api.GetUserResponse], error) {
    user, err := h.userUsecase.Get(ctx, req.Msg.UserId.Value)
    if err != nil {
        return nil, err
    }
    
    return connect.NewResponse(&api.GetUserResponse{
        User: mapper.UserToProto(user),
    }), nil
}
```

#### Dependency Injection

The project uses Google Wire for dependency injection:

```go
// Initialize HTTP server
server, err := di.InitializeAPI()

// Initialize gRPC server
grpcServer, err := di.InitializeGRPCServer()
```

## Architecture

This scaffold follows clean architecture principles:

- **Entities**: Core business objects (`internal/entity`)
- **Use Cases**: Business logic and rules (`internal/usecase`)
- **Adapters**: External interface implementations (`internal/adapter`)
- **Infrastructure**: Technical concerns like servers and databases (`internal/infrastructure`)

## Dependencies

- **Connect-RPC**: `connectrpc.com/connect` for HTTP/gRPC-compatible APIs
- **Atlas**: Database migration tool with versioned migrations
- **Bun ORM**: `github.com/uptrace/bun` for PostgreSQL database access
- **Wire**: `github.com/google/wire` for dependency injection
- **OpenTelemetry**: `go.opentelemetry.io/otel` for distributed tracing
- **Protobuf Schema**: `buf.build/liverty-music/schema` for shared protobuf definitions from BSR

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

This project is licensed under the MIT License.
