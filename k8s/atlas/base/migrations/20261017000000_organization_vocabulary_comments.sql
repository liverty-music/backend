-- rename-organization-operations (task 2.5): column, table and index comments
-- use the entity vocabulary (the Organizer's Organization, admin) instead of
-- tenant and operator. Comments only; no schema or data change.

SET search_path TO app, public;

COMMENT ON COLUMN organizers.provider_organizer_id IS 'The identity provider''s ID for the Organizer (the Zitadel ID of the Organizer''s Organization); NULL until provisioning completes';
COMMENT ON INDEX uq_organizers_provider_organizer_id IS 'One Organization maps to at most one Organizer; NULL while provisioning';
COMMENT ON COLUMN series_images.organizer_id IS 'Owning Organizer. Used as the stable per-Organizer segment of the object key.';
COMMENT ON TABLE suppressed_concerts IS 'Moderation suppression set: the natural key of each published concert an admin deleted, consulted by discovery so a deleted concert is not auto-published or re-staged again. Distinct from rejected_concerts, which is analysis-only and never suppresses.';
COMMENT ON COLUMN suppressed_concerts.suppressed_at IS 'Timestamp when the concert was suppressed (i.e. deleted by an admin).';
COMMENT ON COLUMN identity_verifications.provider_user_id IS 'The verification provider''s ID for the user (Pocket Sign person key, User.id, scoped to Liverty Music''s account at the provider). The only person-identifying datum stored; never the serial, never the 個人番号.';
COMMENT ON COLUMN ticket_sales.name IS 'Sale name shown to fans and Organizers (1-100 characters), e.g. ファンクラブ先行 or 一般発売; required on an Organizer''s sale, NULL on a discovered sale';
