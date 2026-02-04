# Design: Cloud SQL Go Connector Integration

## Context

The current backend connects to PostgreSQL using standard `pgx` with TCP. This requires a password. For Cloud SQL with IAM Authentication, the password must be an OAuth2 token. Managing this token manually or via sidecar (Auth Proxy) adds complexity. Google provides a Go Connector library that handles this natively.

## Goals / Non-Goals

**Goals:**

- Enable password-less authentication using IAM.
- Support both Private Service Connect (Private IP) and Public IP (for local dev).
- Remove mandatory `DATABASE_PASSWORD` check.

**Non-Goals:**

- Removing `pgx`. We will keep `pgx` but use the Connector as a dialer.

## Decisions

1.  **Library**: Use `cloud.google.com/go/cloudsqlconn`.
2.  **Configuration**:
    - Add `INSTANCE_CONNECTION_NAME` (required for connector in non-local envs).
    - Remove `UseIAM` and `IPType` explicit config.
    - Add `local` to allowed environments (default).
3.  **Implementation**:
    - In `rdb.New`:
      - If `Environment == "local"`, use standard `pgx` dialer (requires Password if not trust).
      - If `Environment != "local"`, initialize `cloudsqlconn.Dialer` with IAM Auth and Private IP.
    - Use `poolConfig.ConnConfig.DialFunc` to inject the connector's dialer.
    - Cleanup: Ensure Dialer is closed when app shuts down.

## Risks / Trade-offs

- **Dependency**: Adds direct dependency on Google Cloud libraries to the infrastructure layer (acceptable as this is `infrastructure/database/rdb`).
- **Local Dev**: Local developers need `gcloud auth application-default login` for credentials.
