-- Add fanart.tv image data columns to artists table.
-- fanart stores the raw API response as JSONB for flexible access to all image types.
-- fanart_synced_at tracks when the data was last refreshed from the external API.

ALTER TABLE artists ADD COLUMN fanart JSONB;
ALTER TABLE artists ADD COLUMN fanart_synced_at TIMESTAMPTZ;

COMMENT ON COLUMN artists.fanart IS 'Cached fanart.tv API response containing community-curated artist images (thumb, background, logo, banner)';
COMMENT ON COLUMN artists.fanart_synced_at IS 'Timestamp of the last successful fanart.tv API sync for this artist';
