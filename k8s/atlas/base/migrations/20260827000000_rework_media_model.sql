-- Rework the media model for organizer-event-authoring.
--
-- Replaces the combined `series_media` table (added by 20260826050000) with a
-- normalised `media` table plus a `series_media` join table, and drops the
-- denormalised `series.cover_image_url`. The served object key is derived at
-- runtime as `cdn/{organizer_id}/{media_id}` and the URL is composed from
-- ORGANIZER_MEDIA_CDN_BASE, so no URL is persisted.
--
-- The organizer authoring cover feature is not yet serving (no CDN backend, no
-- uploads), so the combined `series_media` table is empty and is dropped
-- without a data migration.

-- Drop the combined series_media (id, series_id) introduced by 20260826050000.
DROP TABLE IF EXISTS series_media;

-- Media kind enum. IMAGE is the only value at MVP; extend later via
-- ALTER TYPE media_kind ADD VALUE.
CREATE TYPE media_kind AS ENUM ('IMAGE');
COMMENT ON TYPE media_kind IS 'Kind of organizer media asset: IMAGE (cover photo etc.). Extend via ALTER TYPE ADD VALUE.';

-- Media objects owned by an organizer.
-- The row id is BOTH the object-key basename and the creation-time source
-- (UUIDv7), so there is no object_key column and no created_at column —
-- both are derivable from the id. A new upload mints a new media id, which
-- cache-busts the served URL automatically.
CREATE TABLE IF NOT EXISTS media (
    id            UUID         PRIMARY KEY,
    organizer_id  UUID         NOT NULL REFERENCES organizers(id),
    kind          media_kind   NOT NULL,
    attributes    JSONB        NOT NULL DEFAULT '{}',
    CONSTRAINT chk_media_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE media IS 'Normalised media objects uploaded by an organizer. The row id is the UUIDv7 that forms the object-key basename (cdn/{org}/{id}); content_type is stored in attributes.';
COMMENT ON COLUMN media.id IS 'Unique media identifier (UUIDv7, application-generated). Doubles as the cache-busting version token in the object key; a new upload mints a new id.';
COMMENT ON COLUMN media.organizer_id IS 'Owning organizer. Used as the stable tenant segment of the object key.';
COMMENT ON COLUMN media.kind IS 'Media asset kind (IMAGE at MVP).';
COMMENT ON COLUMN media.attributes IS 'Kind-specific metadata as JSONB. For IMAGE: {"content_type": "image/jpeg"}. May include width/height later.';

-- Join table between a series and its media objects.
-- At MVP each series carries at most one cover image (uq_series_media_series).
-- The display_order column is reserved for future gallery support.
CREATE TABLE IF NOT EXISTS series_media (
    series_id     UUID   NOT NULL REFERENCES series(id)  ON DELETE CASCADE,
    media_id      UUID   NOT NULL REFERENCES media(id)   ON DELETE CASCADE,
    display_order INT    NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, media_id)
);

COMMENT ON TABLE series_media IS 'Join table between a series and its media objects. One cover per series at MVP (uq_series_media_series).';
COMMENT ON COLUMN series_media.series_id IS 'Parent series this media object is attached to.';
COMMENT ON COLUMN series_media.media_id IS 'Referenced media object.';
COMMENT ON COLUMN series_media.display_order IS 'Sort position within the series gallery (reserved for future gallery support; 0 = cover).';

-- MVP invariant: at most one media object (the cover) per series.
CREATE UNIQUE INDEX IF NOT EXISTS uq_series_media_series ON series_media(series_id);
COMMENT ON INDEX uq_series_media_series IS 'At most one media object (the cover) per series at MVP; drop or relax when galleries are introduced.';

-- Drop the denormalised cover URL column; the served URL is now derived at read
-- time from the series_media join and the media row.
ALTER TABLE series DROP COLUMN IF EXISTS cover_image_url;
COMMENT ON TABLE series IS 'Parent aggregation above events. Owns metadata shared across every event in a tour, festival, or multi-day single-venue run. Cover image URL is derived from the series_media join at read time.';
