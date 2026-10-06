# Cloud SQL Access (dev and prod)

Developer access to the dev and prod Cloud SQL instances is documented in a
single runbook in the cloud-provisioning repository:

**[cloud-provisioning: docs/runbooks/cloud-sql-access.md](https://github.com/liverty-music/cloud-provisioning/blob/main/docs/runbooks/cloud-sql-access.md)**

In short: create the ephemeral `db-proxy` Pod with
`kubectl apply -k k8s/tools/db-proxy/overlays/<env>` (from cloud-provisioning),
`kubectl port-forward pod/db-proxy 5432:5432 -n backend`, and log in as your
own Cloud SQL IAM user with `PGPASSWORD=$(gcloud sql generate-login-token)`.
Human IAM users are read-only on the `app` schema.

> For integration tests and local development, use the Docker Compose
> PostgreSQL instance (`docker compose up -d postgres`) instead.
