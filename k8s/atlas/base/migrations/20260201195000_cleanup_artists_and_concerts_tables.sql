-- Remove unused columns from artists table
ALTER TABLE artists DROP COLUMN IF EXISTS spotify_id;
ALTER TABLE artists DROP COLUMN IF EXISTS musicbrainz_id;
ALTER TABLE artists DROP COLUMN IF EXISTS genres;
ALTER TABLE artists DROP COLUMN IF EXISTS country;
ALTER TABLE artists DROP COLUMN IF EXISTS image_url;
ALTER TABLE artists DROP COLUMN IF EXISTS created_at;
ALTER TABLE artists DROP COLUMN IF EXISTS updated_at;

-- Remove unused columns from concerts table
ALTER TABLE concerts DROP COLUMN IF EXISTS venue_name;
ALTER TABLE concerts DROP COLUMN IF EXISTS venue_city;
ALTER TABLE concerts DROP COLUMN IF EXISTS venue_country;
ALTER TABLE concerts DROP COLUMN IF EXISTS venue_address;
ALTER TABLE concerts DROP COLUMN IF EXISTS event_date;
ALTER TABLE concerts DROP COLUMN IF EXISTS ticket_url;
ALTER TABLE concerts DROP COLUMN IF EXISTS price;
ALTER TABLE concerts DROP COLUMN IF EXISTS currency;
ALTER TABLE concerts DROP COLUMN IF EXISTS status;
ALTER TABLE concerts DROP COLUMN IF EXISTS created_at;
ALTER TABLE concerts DROP COLUMN IF EXISTS updated_at;
