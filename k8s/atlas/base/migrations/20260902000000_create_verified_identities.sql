-- Create verified_identities table for identity-ekyc-jpki.
--
-- Binds one account (users row) to a verified real person via JPKI or the
-- driver's-licence fallback (Pocket Sign Verify). The person key is the
-- Pocket Sign tenant-scoped User.id (pocket_sign_user_id). No 基本4情報,
-- no raw certificate, no 個人番号 is ever stored here — only the result.
-- The partial unique index (uq_active_pocket_sign_user_id) enforces the
-- invariant: at most one ACTIVE row per pocket_sign_user_id.

CREATE TABLE IF NOT EXISTS verified_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method SMALLINT NOT NULL,
    pocket_sign_user_id TEXT NOT NULL,
    dedupe_strength SMALLINT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL,
    status SMALLINT NOT NULL,
    CONSTRAINT chk_verified_identities_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_verified_identities_method CHECK (method BETWEEN 1 AND 2),
    CONSTRAINT chk_verified_identities_dedupe_strength CHECK (dedupe_strength BETWEEN 1 AND 2),
    CONSTRAINT chk_verified_identities_status CHECK (status BETWEEN 1 AND 2),
    CONSTRAINT chk_verified_identities_pocket_sign_user_id_non_empty CHECK (pocket_sign_user_id <> '')
);

COMMENT ON TABLE verified_identities IS 'Account-level identity verification. One row per (user, verification attempt); the active invariant (at most one ACTIVE row per pocket_sign_user_id) is enforced by the partial unique index.';
COMMENT ON COLUMN verified_identities.id IS 'Unique verification record identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN verified_identities.user_id IS 'Reference to the user account this verification is bound to.';
COMMENT ON COLUMN verified_identities.method IS 'Verification method: 1=JPKI, 2=DRIVER_LICENCE.';
COMMENT ON COLUMN verified_identities.pocket_sign_user_id IS 'Pocket Sign tenant-scoped person key (User.id). The only person-identifying datum stored; never the serial, never the 個人番号.';
COMMENT ON COLUMN verified_identities.dedupe_strength IS 'Dedupe guarantee strength: 1=STRONG (JPKI, stable per-person key), 2=WEAK (driver licence, document-scoped key).';
COMMENT ON COLUMN verified_identities.verified_at IS 'Timestamp when the verification was established.';
COMMENT ON COLUMN verified_identities.status IS 'Freshness lifecycle: 1=ACTIVE (current), 2=NEEDS_REVERIFICATION (revoked/changed/expired per 現況確認).';

-- Partial unique index: at most one ACTIVE row per Pocket Sign person key.
-- status=1 means ACTIVE. A second IDENTITY_VERIFIED account for the same
-- person is rejected by this index with a unique_violation (23505).
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_pocket_sign_user_id
    ON verified_identities(pocket_sign_user_id) WHERE status = 1;
COMMENT ON INDEX uq_active_pocket_sign_user_id IS 'At most one ACTIVE verified identity row per Pocket Sign person key. A second IDENTITY_VERIFIED account for the same person is rejected here.';

CREATE INDEX IF NOT EXISTS idx_verified_identities_user_id ON verified_identities(user_id);
COMMENT ON INDEX idx_verified_identities_user_id IS 'Optimizes lookup of a user''s active verification record.';
