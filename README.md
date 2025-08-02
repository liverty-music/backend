# Go Backend Scaffold

A modern Go backend scaffold following clean architecture principles with gRPC support, structured logging, and dependency injection.

## Features

- **Connect-RPC Server**: HTTP/gRPC-compatible server with protobuf support
- **Database Migrations**: Atlas-powered versioned migrations with schema generation from Bun models
- **Structured Logging**: Advanced logger with OpenTelemetry integration for distributed tracing
- **Dependency Injection**: Wire-based dependency injection for clean architecture
- **Clean Architecture**: Well-organized project structure following domain-driven design
- **Protobuf Integration**: Ready-to-use with `buf.build/pannpers/scaffold` from BSR

## Project Structure

```
.
├── cmd/
│   └── api/                    # Main application entry point
├── internal/
│   ├── adapter/               # External interface adapters
│   │   └── grpc/             # gRPC handlers and services
│   ├── di/                   # Dependency injection configuration
│   ├── entitiy/              # Domain entities
│   ├── infrastructure/       # Infrastructure concerns
│   │   └── server/           # HTTP and gRPC server implementations
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
git clone https://github.com/pannpers/go-backend-scaffold.git
cd go-backend-scaffold
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
- Access to the protobuf schema from BSR (`buf.build/pannpers/scaffold`)

#### Start the Connect Server

```bash
go run cmd/api/main.go
```

The Connect server will start on port 9090.

#### Example: GetUser

```bash
buf curl --schema buf.build/pannpers/scaffold --protocol connect \
  -d '{"user_id": {"value": "123"}}' \
  http://localhost:9090/pannpers.api.v1.UserService/GetUser
```

#### Example: CreateUser

```bash
buf curl --schema buf.build/pannpers/scaffold --protocol connect \
  -d '{"title": {"value": "Sample Post"}, "author_id": {"value": "user123"}}' \
  http://localhost:9090/pannpers.api.v1.PostService/CreatePost
```

#### Notes

- **Service paths:** Use `/pannpers.api.v1.UserService/` and `/pannpers.api.v1.PostService/` for BSR schema.
- **Protocol:** Always use `--protocol connect` for Connect servers.
- **Schema:** Use BSR schema reference `buf.build/pannpers/scaffold` for direct access to protobuf definitions.
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
import "github.com/pannpers/go-backend-scaffold/pkg/logger"

// Create a logger with default options (JSON format)
logger := logger.New()

// Create a logger with custom options
logger := logger.New(
    logger.WithLevel(slog.LevelDebug),
    logger.WithFormat(logger.FormatText), // Human-readable format
    logger.WithWriter(os.Stderr),
)

// Log with context (automatically includes trace_id and span_id)
ctx := context.Background()
logger.Info(ctx, "User logged in", slog.String("user_id", "123"))
```

#### gRPC Handler Implementation

The scaffold provides a foundation for implementing gRPC services:

```go
// Example handler implementation
type UserHandler struct {
    api.UnimplementedUserServiceServer
}

func (h *UserHandler) GetUser(ctx context.Context, req *api.GetUserRequest) (*api.GetUserResponse, error) {
    // Your business logic here
    return &api.GetUserResponse{
        User: &entity.User{
            Id: &entity.UserId{Value: req.UserId},
            Name: &entity.UserName{Value: "Example User"},
        },
    }, nil
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
- **Protobuf Scaffold**: `buf.build/pannpers/scaffold` for shared protobuf definitions from BSR

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

This project is licensed under the MIT License.
