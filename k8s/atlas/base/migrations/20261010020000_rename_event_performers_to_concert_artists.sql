-- Rename event_performers to concert_artists and key it by the concert
-- (split-concert-from-event, design D6). Performing artists belong to the music
-- kind of event, so the link now references concerts(event_id) instead of the
-- generic events(id). Every existing row has a concerts row (checked in prod on
-- 2026-10-10: 0 of 1620 rows without one), so the new foreign key validates.
--
-- Hand-written as RENAME rather than Atlas's DROP + CREATE so the rows and the
-- organizer-console-api INSERT grant (bound to the table's OID) are kept.

SET search_path TO app, public;

ALTER TABLE event_performers RENAME TO concert_artists;
ALTER TABLE concert_artists RENAME CONSTRAINT event_performers_pkey TO concert_artists_pkey;
ALTER TABLE concert_artists RENAME CONSTRAINT event_performers_artist_id_fkey TO concert_artists_artist_id_fkey;
ALTER TABLE concert_artists RENAME CONSTRAINT event_performers_event_id_not_null TO concert_artists_event_id_not_null;
ALTER TABLE concert_artists RENAME CONSTRAINT event_performers_artist_id_not_null TO concert_artists_artist_id_not_null;
ALTER INDEX idx_event_performers_artist_id RENAME TO idx_concert_artists_artist_id;

ALTER TABLE concert_artists DROP CONSTRAINT event_performers_event_id_fkey;
ALTER TABLE concert_artists ADD CONSTRAINT concert_artists_event_id_fkey
  FOREIGN KEY (event_id) REFERENCES concerts(event_id) ON DELETE CASCADE;

COMMENT ON TABLE concert_artists IS 'M:N relation between concerts and performing artists. Supports festival lineups, co-headliners, and support acts.';
COMMENT ON COLUMN concert_artists.event_id IS 'Reference to the concert (its event id)';
COMMENT ON TABLE concerts IS 'Music-specific event extension, linked 1:1 with events. Its performing artists are in concert_artists.';
