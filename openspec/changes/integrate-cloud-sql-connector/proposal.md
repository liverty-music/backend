# Integrate Cloud SQL Go Connector

## Why

Currently, the backend application uses standard `pgx` without IAM authentication support. This forces the use of a static password (`DATABASE_PASSWORD`) which complicates security and operations. Google Cloud recommends using the Cloud SQL Go Connector for Go applications to enable secure, IAM-native authentication without managing secrets or sidecars.

## What Changes

We will integrate `cloud.google.com/go/cloudsqlconn` into the backend reference implementation (clean architecture). This involves:

1.  Modifying the `rdb` package to support the Connector dialer.
2.  Updating `config` to support optional/auto IAM Auth.
3.  Removing the requirement for `DATABASE_PASSWORD`.

## Capabilities

### New Capabilities

- `cloud-sql-connector`: Enables secure, password-less IAM authentication to Cloud SQL instances.

### Modified Capabilities

- `database-configuration`: Updates configuration schema to support IAM Auth toggle and remove mandatory password.

## Impact

- `backend/internal/infrastructure/database/rdb`: Major refactor to support custom dialer.
- `backend/pkg/config`: Config schema changes.
- `cloud-provisioning`: Will allow removal of `DATABASE_PASSWORD` secret.
