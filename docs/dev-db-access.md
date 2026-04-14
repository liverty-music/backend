# Dev DB Access (Cloud SQL via port-forward)

To connect directly to the **dev Cloud SQL instance** (e.g., for ad-hoc queries, schema inspection, or data debugging), use the Cloud SQL Auth Proxy Pod deployed in the dev GKE cluster.

> **This is for the dev Cloud SQL instance only.** For integration tests and local development, use the Docker Compose PostgreSQL instance (`docker compose up -d postgres`).

**Step 1 — Forward the proxy port:**

```bash
kubectl port-forward deployment/cloud-sql-proxy 5432:5432 -n backend
```

Keep this terminal open. The tunnel closes when you exit.

**Step 2 — Connect with psql:**

```bash
psql "host=localhost port=5432 user=backend-app@liverty-music-dev.iam dbname=liverty-music sslmode=disable options='-c search_path=app'"
```

No password is required — authentication is handled by IAM via the proxy.

**Connection parameters:**

| Parameter | Value |
|-----------|-------|
| Host | `localhost` |
| Port | `5432` |
| User | `backend-app@liverty-music-dev.iam` |
| Database | `liverty-music` |
| Schema | `app` |
| SSL Mode | `disable` (proxy handles encryption) |
