# Spec: Cloud SQL Connector

## Purpose

Secure and efficient connectivity to Google Cloud SQL using the Cloud SQL Go Connector.

## Requirements

### Requirement: Authenticate via Cloud SQL Connector

The application SHALL use the Cloud SQL Go Connector for database connectivity when `ENVIRONMENT` is NOT `local`.

#### Scenario: Non-local Environments

- **WHEN** `ENVIRONMENT` is `development`, `staging`, or `production`
- **AND** `DATABASE_INSTANCE_CONNECTION_NAME` is provided
- **THEN** application initializes `cloudsqlconn.Dialer`
- **AND** `pgx` uses this dialer to connect
- **AND** connection uses IAM Auth and Private IP

### Requirement: Standard Connection for Local

The application SHALL use standard `pgx` connection when running locally.

#### Scenario: Local Environment

- **WHEN** `ENVIRONMENT` is `local`
- **THEN** application uses standard TCP connection
- **AND** Uses `DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_PASSWORD` (if provided)

### Requirement: Configuration Validation

The application SHALL validate that `InstanceConnectionName` is present if `ENVIRONMENT` is not local.

#### Scenario: Missing Connection Name

- **WHEN** `ENVIRONMENT` is not `local`
- **AND** `DATABASE_INSTANCE_CONNECTION_NAME` is empty
- **THEN** application fails to start with descriptive error
