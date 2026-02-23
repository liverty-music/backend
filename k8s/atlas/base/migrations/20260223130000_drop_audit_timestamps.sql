-- Drop metadata timestamp columns (created_at / updated_at) from all tables.
-- Business timestamps (minted_at, start_at, open_at, searched_at, used_at, scheduled_at, sent_at) are preserved.
-- Audit logging will be handled separately.

ALTER TABLE users DROP COLUMN created_at, DROP COLUMN updated_at;
ALTER TABLE events DROP COLUMN created_at, DROP COLUMN updated_at;
ALTER TABLE venues DROP COLUMN created_at, DROP COLUMN updated_at;
ALTER TABLE artist_official_site DROP COLUMN created_at, DROP COLUMN updated_at;
ALTER TABLE followed_artists DROP COLUMN created_at;
ALTER TABLE notifications DROP COLUMN created_at, DROP COLUMN updated_at;
