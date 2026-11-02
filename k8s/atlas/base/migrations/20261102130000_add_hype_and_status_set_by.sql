-- infer-hype-and-journey (design "Storage"): a Follow records who set its hype
-- level, and a ticket journey records who set its status.
--
-- Backfill: a hype level other than the starting Nearby can only have come
-- from the fan, so it is 'fan'; a Nearby is 'default'. Every existing journey
-- was set by the fan (or by MarkPaid, which no inference may replace either),
-- so it is 'fan'. Both tables hold hundreds of rows, so the backfill and the
-- CHECK validation run in this one migration without batching; the constant
-- defaults make the ADD COLUMN itself catalog-only.

SET search_path TO app, public;

ALTER TABLE followed_artists ADD COLUMN hype_set_by TEXT NOT NULL DEFAULT 'default';
UPDATE followed_artists SET hype_set_by = 'fan' WHERE hype <> 'nearby';
ALTER TABLE followed_artists ADD CONSTRAINT chk_followed_artists_hype_set_by CHECK (hype_set_by IN ('default', 'fan', 'inference'));
COMMENT ON COLUMN followed_artists.hype_set_by IS 'Who set the hype level: default (the level a new follow starts at), fan (chosen by the fan; never changed by the app), or inference (raised by the app to match a concert the fan''s journey showed interest in)';

ALTER TABLE ticket_journeys ADD COLUMN status_set_by TEXT NOT NULL DEFAULT 'fan';
ALTER TABLE ticket_journeys ADD CONSTRAINT chk_ticket_journeys_status_set_by CHECK (status_set_by IN ('fan', 'platform', 'inference'));
COMMENT ON COLUMN ticket_journeys.status_set_by IS 'Who set the status: fan, platform (Applied for a lottery entry or Paid for a paid order on Liverty Music), or inference (Tracking after the fan added the concert to their calendar; replaces no journey)';
