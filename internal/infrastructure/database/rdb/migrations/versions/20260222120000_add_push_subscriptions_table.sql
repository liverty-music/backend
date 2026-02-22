-- +goose Up
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL
);

COMMENT ON TABLE push_subscriptions IS 'Browser Web Push subscription data for delivering notifications';
COMMENT ON COLUMN push_subscriptions.id IS 'Unique identifier (UUIDv7)';
COMMENT ON COLUMN push_subscriptions.user_id IS 'Reference to the user who owns this subscription';
COMMENT ON COLUMN push_subscriptions.endpoint IS 'Push service endpoint URL provided by the browser';
COMMENT ON COLUMN push_subscriptions.p256dh IS 'ECDH public key for payload encryption (Base64url-encoded)';
COMMENT ON COLUMN push_subscriptions.auth IS 'Authentication secret for payload encryption (Base64url-encoded)';

CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user_id ON push_subscriptions(user_id);
