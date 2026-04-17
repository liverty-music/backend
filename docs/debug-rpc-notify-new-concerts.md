# Debug RPC: `PushNotificationService.NotifyNewConcerts`

## Purpose

Triggers the push notification delivery pipeline for a specific set of concerts, bypassing the NATS `CONCERT.created` event bus. Use this for:

- Deterministic integration testing of the delivery pipeline without waiting for Gemini-driven concert discovery.
- Operator-initiated re-delivery when a legitimate batch of notifications failed to send (e.g., NATS outage, consumer pod restart loop).

Invokes the same `PushNotificationUseCase.NotifyNewConcerts` code path as the consumer, so hype filtering and webpush delivery behave identically.

## Environment gate

Available only when `ENVIRONMENT != "production"`. In production the RPC returns `PERMISSION_DENIED` before touching any state.

## Authentication

JWT required. Unauthenticated calls return `UNAUTHENTICATED`. The session identity is only used for auth presence; the RPC itself does not resolve to a specific user.

## Request

```
PushNotificationService.NotifyNewConcerts
  artist_id:  ArtistId   (required)
  concert_ids: []EventId (required, min 1, max 1000)
```

Every supplied `concert_id` must be an event UUID that belongs to the specified artist. Unknown IDs or cross-artist IDs reject the whole request with `INVALID_ARGUMENT` — no partial delivery.

## Response

Empty (`NotifyNewConcertsResponse`). Success means the delivery loop executed; individual per-subscription failures are logged + recorded as metrics inside the use case, not surfaced as an RPC error.

## Example invocation

```bash
grpcurl \
  -H "authorization: Bearer ${JWT}" \
  -d '{
    "artist_id":   {"value": "<artist-uuid>"},
    "concert_ids": [{"value": "<event-uuid-1>"}, {"value": "<event-uuid-2>"}]
  }' \
  dev-api.example:443 \
  liverty_music.rpc.push_notification.v1.PushNotificationService/NotifyNewConcerts
```

## How to verify delivery succeeded

An HTTP 200 RPC response means **the delivery pipeline ran** — it does NOT mean notifications were delivered. Per-subscription delivery results are only visible in server-app logs and metrics.

### Step 1: Tail server-app logs in parallel

```bash
kubectl logs -n backend -l app=server -f --tail=0
```

### Step 2: Call the RPC (see example above)

### Step 3: Check per-subscription log lines

For each push subscription, the use case emits exactly one of:

| Log pattern | Meaning |
|---|---|
| `RecordPushSend` with `"success"` | Push delivered to the push service (FCM/Mozilla). Does not guarantee device delivery, but the push service accepted it. |
| `RecordPushSend` with `"gone"` | Subscription endpoint returned 410 Gone. The stale row was auto-deleted from DB. Re-subscribe from the browser. |
| `"failed to send push notification"` with `responseBody` attr | Push service rejected the request. The `responseBody` field contains the upstream diagnostic message (e.g., "Your client does not have permission" = PGA blocking; see Troubleshooting). |

### Troubleshooting

| responseBody content | Root cause | Fix |
|---|---|---|
| "Your client does not have permission to get URL ... from this server. That's all we know." | GCP Private Google Access restricted VIP is blocking a non-VPC-SC service (e.g., FCM) | Switch PGA DNS zone from `restricted.googleapis.com` to `private.googleapis.com` in cloud-provisioning |
| "push subscription has unsubscribed or expired." | Browser unsubscribed or FCM token expired | Expected for stale subscriptions; 410 handler auto-cleans |
| Empty body with 401 status | VAPID JWT rejected by push service | Verify VAPID key pair consistency (Secret Manager private ↔ ConfigMap public ↔ frontend .env public) |
| Timeout / connection refused | Pod cannot reach `fcm.googleapis.com` at all | Check DNS resolution + Cloud NAT egress + network policies |

## Related

- Spec: `openspec/specs/push-notification-service/spec.md` (Requirement: NotifyNewConcerts debug RPC for deterministic invocation).
- Use case: `internal/usecase/push_notification_uc.go` (`NotifyNewConcerts`).
- Handler: `internal/adapter/rpc/push_notification_handler.go` (`NotifyNewConcerts`).
