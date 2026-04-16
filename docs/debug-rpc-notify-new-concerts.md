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

## Related

- Spec: `openspec/specs/push-notification-service/spec.md` (Requirement: NotifyNewConcerts debug RPC for deterministic invocation).
- Use case: `internal/usecase/push_notification_uc.go` (`NotifyNewConcerts`).
- Handler: `internal/adapter/rpc/push_notification_handler.go` (`NotifyNewConcerts`).
