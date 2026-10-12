-- Correct the object-key layout in the series_images table comment: originals
-- live at {org}/{id} in the internal bucket (no internal/ prefix), variants
-- under cdn/{org}/{id}/ in the served bucket.
COMMENT ON TABLE series_images IS 'Images uploaded by an organizer for its Series. The row id is the UUIDv7 that forms the object-key basename ({org}/{id} in the internal bucket, cdn/{org}/{id}/ in the served bucket); content_type is stored in attributes.';
