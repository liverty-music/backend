-- Add the series_media table (organizer-event-authoring).
--
-- One row per stored media object. The row id is the object's version token: the
-- served object lives at `series/<series_id>/cover/<id>` (extension-less; the
-- content type is carried by the GCS object metadata), so replacing a cover
-- mints a new id, yields a new immutable URL, and lets caches serve forever. The
-- MVP stores a single cover per series (uq_series_media_series); a later
-- authoring extension relaxes that and adds purpose/sort_order for galleries.
-- series.cover_image_url denormalizes the current cover's served URL for reads.
CREATE TABLE IF NOT EXISTS series_media (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    CONSTRAINT chk_series_media_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE series_media IS 'First-party media objects for a series (one cover per series at MVP). The row id is the object version token and the object key basis (series/<series_id>/cover/<id>).';
COMMENT ON COLUMN series_media.id IS 'Unique media identifier (UUIDv7). Also the cache-busting version token embedded in the object key; a new upload mints a new id.';
COMMENT ON COLUMN series_media.series_id IS 'Parent series this media object belongs to.';

CREATE UNIQUE INDEX IF NOT EXISTS uq_series_media_series ON series_media(series_id);
COMMENT ON INDEX uq_series_media_series IS 'At most one media object (the cover) per series at MVP; relaxed when galleries are introduced.';
