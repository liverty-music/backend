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
- **Database Migrations**: Atlas-powered versioned migrations with schema alignment to entity definitions
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

- Go 1.25 or later
- Atlas CLI (binary installation required)
- golangci-lint (binary installation required)
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

3. Install Tools:

This project uses binary installations for tools like Atlas and golangci-lint to avoid Go module dependency conflicts.

```bash
# Install Atlas CLI
curl -sSf https://atlasgo.sh | sh

# Install golangci-lint (Binary)
# See https://golangci-lint.run/usage/install/#local-installation
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.64.5
```

4. Start the database (optional, for local development):

```bash
# Start PostgreSQL container
podman compose up -d postgres

# Initialize database schema
# This applies all migrations from internal/infrastructure/database/rdb/migrations/
atlas migrate apply --env local
```

**Note**: The database schema is managed by Atlas migrations. On first setup, you must manually apply migrations using the command above.

### Running the Application

#### HTTP Server

```bash
go run cmd/api/main.go
```

The HTTP server will start on port 8080.

#### gRPC Server

The gRPC server is configured to run on port 9090 with reflection enabled for development tools like `grpcurl`.

### Development Commands

#### Running Tests

```bash
# Run all tests
go test ./...

# Run tests for a specific package
go test ./pkg/config

# Run tests with coverage
go test -cover ./...
```

#### Code Quality

```bash
# Run static analysis
go vet ./...

# Run linter (requires golangci-lint binary)
golangci-lint run ./...
```

### Gemini Integration Testing

To verify the Gemini-based concert extraction with real GCP resources:

1. **Set Environment Variables**:

```bash
export GCP_PROJECT_ID="liverty-music-dev"
export GCP_LOCATION="global"
export GCP_GEMINI_MODEL="gemini-3-flash-preview"
export GCP_VERTEX_AI_SEARCH_DATA_STORE="projects/liverty-music-dev/locations/global/collections/default_collection/dataStores/official-artist-site"
```

2. **Run Test**:

```bash
# Run the integration test for UVERworld
# Requires Google Application Default Credentials (ADC)
go test -v -count=1 -run TestGeminiConcertSearcher_Search_Real ./internal/infrastructure/gcp/gemini/...
```

### Music API Integration Testing

To verify the Last.fm and MusicBrainz client integrations against live APIs:

1. **Set Last.fm API Key**:
   Create a `testdata/.env.test` file in `internal/infrastructure/music/lastfm/` or set the environment variable:
   ```bash
   export LASTFM_API_KEY="your_real_key_here"
   ```

2. **Run Tests**:
   MusicBrainz integration tests do not require an API key but are rate-limited (1 request per second).
   ```bash
   # Run all music integration tests
   go test -v -tags=integration ./internal/infrastructure/music/...
   ```

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

To generate a new migration file from your models, use `go tool atlas` with the configured arguments:

```bash
# This command compares your SQL schema (schema.sql) with the database state.
atlas migrate diff --env local <migration_name>

# Example:
atlas migrate diff --env local create_users_table
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
├── schema.sql           # Base schema file
└── versions/            # Versioned migration files
```

### Development

#### Linting

This project uses `golangci-lint` for linting. Install the binary as described in Prerequisites, then run:

```bash
# This will run all configured linters
golangci-lint run ./...
```

#### Logger Usage

The scaffold includes a powerful structured logger with OpenTelemetry integration:

```go
import "github.com/pannpers/go-logging/logging"

// Create a logger with default options (JSON format)
logger, err := logging.New()

// Create a logger with custom options
logger, err := logging.New(
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
- **SQL Support**: `github.com/jackc/pgx/v5` for PostgreSQL database access with raw SQL support
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
