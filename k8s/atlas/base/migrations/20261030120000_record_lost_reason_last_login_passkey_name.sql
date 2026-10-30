-- publish-events-through-outbox (design "Migration Plan"): a Lost lottery
-- entry records why it lost, a user records when they last signed in, and a
-- passkey keeps the label the identity provider gave it.
--
-- The read-only prod check on 2026-10-30 found 0 Lost lottery entries, so the
-- backfill below changes nothing in prod; it marks any Lost entry as not
-- drawn, because a capture failure was never stored.

SET search_path TO app, public;

ALTER TABLE users ADD COLUMN last_login_at TIMESTAMPTZ;
COMMENT ON COLUMN users.last_login_at IS 'When the user last signed in, as reported by the identity provider''s sign-in notice. NULL until the first sign-in is recorded; only moves forward.';

ALTER TABLE lottery_entries ADD COLUMN lost_reason SMALLINT;
UPDATE lottery_entries SET lost_reason = 1 WHERE state = 3;
ALTER TABLE lottery_entries ADD CONSTRAINT chk_lottery_entries_lost_reason CHECK ((state = 3 AND lost_reason IN (1, 2)) OR (state <> 3 AND lost_reason IS NULL));
COMMENT ON COLUMN lottery_entries.lost_reason IS 'Why a Lost entry lost: 1=not drawn, 2=the card authorization could not be captured. NULL unless state is 3 (Lost).';

ALTER TABLE passkeys ADD COLUMN name TEXT;
ALTER TABLE passkeys ADD CONSTRAINT chk_passkeys_name_len CHECK (name IS NULL OR char_length(name) BETWEEN 1 AND 200);
COMMENT ON COLUMN passkeys.name IS 'The label the identity provider gave the passkey, shown in the passkey email. NULL when the identity provider gave none or the row predates recording it.';
